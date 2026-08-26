package a2a

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

// SkillID constants — generic across all ggcode instances.
const (
	SkillCodeEdit    = "code-edit"
	SkillFileSearch  = "file-search"
	SkillCommandExec = "command-exec"
	SkillGitOps      = "git-ops"
	SkillCodeReview  = "code-review"
	SkillFullTask    = "full-task"
)

// DefaultSkills returns the fixed set of skills every ggcode instance advertises.
func DefaultSkills() []Skill {
	return []Skill{
		{ID: SkillCodeEdit, Name: "Code Editing", Description: "Read, write, and edit source files with diff support", Tags: []string{"code", "edit"}},
		{ID: SkillFileSearch, Name: "File Search", Description: "Search files by name pattern, content, or glob", Tags: []string{"search", "files"}},
		{ID: SkillCommandExec, Name: "Command Execution", Description: "Run shell commands with timeout and output capture", Tags: []string{"shell", "run"}},
		{ID: SkillGitOps, Name: "Git Operations", Description: "Git status, diff, log, and branch operations", Tags: []string{"git", "vcs"}},
		{ID: SkillCodeReview, Name: "Code Review", Description: "Review code changes and provide feedback", Tags: []string{"review", "quality"}},
		{ID: SkillFullTask, Name: "Full Task Execution", Description: "Execute a complete coding task end-to-end", Tags: []string{"task", "complete"}},
	}
}

// TaskHandler processes incoming A2A tasks.
type TaskHandler struct {
	mu        sync.Mutex
	workspace string
	agent     *agent.Agent
	registry  *tool.Registry
	tasks     map[string]*Task // active tasks by ID
	// cancels maps task ID to the installed cancel entry. The generation
	// number uniquely identifies the goroutine that installed the entry so a
	// stale execute goroutine can detect that its entry has been replaced by
	// continueTask (fix #327: reflect pointer comparison of CancelFuncs is
	// meaningless — WithTimeout closures share the same code pointer).
	cancels   map[string]cancelEntry
	cancelGen uint64 // incremented per install; guarded by mu
	meta      WorkspaceMeta
	maxTasks  int
	timeout   time.Duration

	// messageIndex maps message.MessageID → task ID for idempotent retries
	// (#565 G): a client that times out and re-sends the same payload
	// previously spawned a second, independent task — for side-effecting
	// skills (code-edit / full-task) that double-executed the work.
	messageIndex map[string]string

	// Event callbacks for observability (TUI, daemon follow, IM).
	onTaskEvent func(event TaskEventMessage)

	// Push notification callback: server injects this to fire HTTP callbacks
	// to registered push configs when a task status changes.
	pushNotifier func(taskID string, payload StreamResponse)
}

// TaskEventMessage describes an A2A task lifecycle event.
type TaskEventMessage struct {
	Type    string // "start", "complete", "fail", "cancel"
	TaskID  string
	Skill   string
	Message string // human-readable summary
	Error   string // only for "fail"
}

// HandlerOption configures a TaskHandler.
type HandlerOption func(*TaskHandler)

// WithMaxTasks sets the concurrent task limit.
func WithMaxTasks(n int) HandlerOption {
	return func(h *TaskHandler) { h.maxTasks = n }
}

// WithTimeout sets the per-task timeout.
func WithTimeout(d time.Duration) HandlerOption {
	return func(h *TaskHandler) { h.timeout = d }
}

// WithOnTaskEvent sets the callback for task lifecycle events.
func WithOnTaskEvent(fn func(TaskEventMessage)) HandlerOption {
	return func(h *TaskHandler) { h.onTaskEvent = fn }
}

// SetOnTaskEvent sets the callback at runtime.
func (h *TaskHandler) SetOnTaskEvent(fn func(TaskEventMessage)) {
	h.mu.Lock()
	h.onTaskEvent = fn
	h.mu.Unlock()
}

// ActiveTasks returns snapshots of currently running A2A tasks.
func (h *TaskHandler) ActiveTasks() []Task {
	h.mu.Lock()
	defer h.mu.Unlock()
	var result []Task
	for _, t := range h.tasks {
		if !t.Status.State.IsTerminal() {
			result = append(result, t.Snapshot())
		}
	}
	return result
}

// Timeout returns the configured task timeout.
func (h *TaskHandler) Timeout() time.Duration {
	return h.timeout
}

// NewTaskHandler creates a handler bound to a specific workspace.
func NewTaskHandler(workspace string, a *agent.Agent, reg *tool.Registry, opts ...HandlerOption) *TaskHandler {
	h := &TaskHandler{
		workspace:    workspace,
		agent:        a,
		registry:     reg,
		tasks:        make(map[string]*Task),
		cancels:      make(map[string]cancelEntry),
		messageIndex: make(map[string]string),
		meta:         detectWorkspaceMeta(workspace),
		maxTasks:     5,
		timeout:      5 * time.Minute,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// WorkspaceMeta holds dynamically detected workspace properties.
type WorkspaceMeta struct {
	Workspace  string   `json:"workspace"`
	Languages  []string `json:"languages,omitempty"`
	Frameworks []string `json:"frameworks,omitempty"`
	HasGit     bool     `json:"has_git"`
	HasTests   bool     `json:"has_tests"`
	ProjName   string   `json:"project_name"`
}

// Handle processes an incoming message as an A2A task.
// If params.TaskID is set, it continues an existing task (multi-turn).
// Otherwise it creates a new task.
func (h *TaskHandler) Handle(ctx context.Context, skill string, input Message, existingTaskID string) (*Task, error) {
	// Continue existing task (multi-turn / input-required flow).
	if existingTaskID != "" {
		return h.continueTask(ctx, existingTaskID, input)
	}

	if skill == "" {
		skill = SkillFullTask
	}

	perm, ok := skillPermissions[skill]
	if !ok {
		return nil, fmt.Errorf("unknown skill: %s", skill)
	}

	// Validate skill availability at runtime.
	if skill == SkillGitOps && !h.meta.HasGit {
		return nil, fmt.Errorf("workspace has no git repository")
	}

	// Idempotent retry (#565 G): same messageId re-sent (timeout + client
	// retry) returns the ORIGINAL task instead of spawning a duplicate that
	// would double-execute side effects for code-edit / full-task skills.
	// Check in the same critical section as messageIndex insertion (#1094).
	if input.MessageID != "" {
		h.mu.Lock()
		if tid, ok := h.messageIndex[input.MessageID]; ok {
			if t, exists := h.tasks[tid]; exists {
				snap := t.Snapshot()
				h.mu.Unlock()
				return &snap, nil
			}
			delete(h.messageIndex, input.MessageID)
		}
		h.mu.Unlock()
	}

	// Check concurrency limit.
	h.mu.Lock()

	// Prune old completed tasks.
	h.cleanupExpiredTasksLocked()

	active := 0
	for _, t := range h.tasks {
		// Exclude InputRequired from concurrent count - it is a pseudo-terminal
		// state that does not consume execution capacity. Fixes #1077.
		if !t.Status.IsTerminal() && t.Status.State != TaskStateInputRequired {
			active++
		}
	}
	if active >= h.maxTasks {
		h.mu.Unlock()
		return nil, fmt.Errorf("too many concurrent tasks (%d/%d)", active, h.maxTasks)
	}

	task := &Task{
		ID:        generateID(),
		ContextID: generateID(),
		Status:    TaskStatus{State: TaskStateSubmitted, Timestamp: time.Now()},
		Skill:     skill,
		History:   []Message{input},
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
	}
	h.tasks[task.ID] = task
	if input.MessageID != "" {
		h.messageIndex[input.MessageID] = task.ID
	}
	h.mu.Unlock()

	// Create cancellable context for this task.
	taskCtx, cancel := context.WithTimeout(context.Background(), h.timeout)
	h.mu.Lock()
	gen := h.installCancelLocked(task.ID, cancel)
	h.mu.Unlock()

	// Execute asynchronously. Pass the installed generation so the goroutine
	// can later clean up only if the map entry is still its own (fix #166/#327:
	// continueTask replaces the entry on resume; an ownership-blind cleanup
	// would kill or zombie the resumed task's context).
	safego.Go("a2a.execute", func() { h.execute(taskCtx, task, perm, gen) })

	// Snapshot must be taken under the lock to avoid racing with
	// updateStatus in the execute goroutine.
	h.mu.Lock()
	snap := task.Snapshot()
	h.mu.Unlock()
	return &snap, nil
}

// continueTask resumes an existing task that is in input-required state.
func (h *TaskHandler) continueTask(ctx context.Context, taskID string, input Message) (*Task, error) {
	h.mu.Lock()
	task, ok := h.tasks[taskID]
	if !ok {
		h.mu.Unlock()
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if task.Status.State != TaskStateInputRequired {
		h.mu.Unlock()
		return nil, fmt.Errorf("task %s is not in input-required state (current: %s)", taskID, task.Status.State)
	}

	// Check permissions BEFORE mutating state to avoid rollback on failure.
	// If skill lookup fails, we return early without leaving the task in Working
	// state with an unclosed done channel. Fixes #1078.
	perm, ok := skillPermissions[task.Skill]
	if !ok {
		h.mu.Unlock()
		return nil, fmt.Errorf("unknown skill: %s", task.Skill)
	}

	// Append the new user message to history.
	task.History = append(task.History, input)

	// Transition to working state while still holding the lock,
	// preventing CancelTask from racing between the check and goroutine start.
	task.Status = TaskStatus{State: TaskStateWorking, Timestamp: time.Now()}
	task.UpdatedAt = time.Now()
	// Re-create the done channel since it was closed when the previous
	// execution reached input-required (a pseudo-terminal state).
	task.done = make(chan struct{})

	// Cancel the old context before creating a new one.
	// The original execute() goroutine may still reach cleanupCancel,
	// which would otherwise cancel this new context.
	if old, ok := h.cancels[taskID]; ok {
		old.cancel()
		delete(h.cancels, taskID)
	}

	// Re-create the cancel context for the resumed execution.
	taskCtx, cancel := context.WithTimeout(context.Background(), h.timeout)
	gen := h.installCancelLocked(taskID, cancel)
	h.mu.Unlock()

	// Resume execution. Pass the newly installed generation (see execute call
	// in handleSendMessageSend for the ownership rationale).
	safego.Go("a2a.execute", func() { h.execute(taskCtx, task, perm, gen) })

	// Snapshot must be taken under the lock to avoid racing with
	// updateStatus in the execute goroutine.
	h.mu.Lock()
	snap := task.Snapshot()
	h.mu.Unlock()
	return &snap, nil
}

func (h *TaskHandler) execute(ctx context.Context, t *Task, perm *SkillPermission, installedGen uint64) {
	h.updateStatus(t, TaskStateWorking, "")

	// Recover from panics to avoid leaking the task in Working state.
	// Without this, safego.Recover silently swallows panics and the task
	// remains Working forever, leaking a concurrency slot. Fixes #1080.
	defer func() {
		if r := recover(); r != nil {
			debug.Log("a2a", "execute goroutine panic: %v", r)
			h.updateStatus(t, TaskStateFailed, fmt.Sprintf("internal error: %v", r))
			h.mu.Lock()
			if t.done != nil {
				close(t.done)
				t.done = nil
			}
			h.mu.Unlock()
			h.cleanupCancelIf(t.ID, installedGen)
		}
	}()

	var result string
	var err error

	// #1094: take a snapshot of history to avoid data race
	// with updateStatus which holds h.mu while appending to History.
	h.mu.Lock()
	historySnap := make([]Message, len(t.History))
	copy(historySnap, t.History)
	h.mu.Unlock()

	switch t.Skill {
	case SkillFileSearch, SkillGitOps, SkillCommandExec:
		// Use the latest user message (not History[0]) so that follow-up
		// messages in input-required flows are actually delivered.
		lastIdx := len(historySnap) - 1
		if len(historySnap) > 0 {
			if h.agent == nil {
				err = fmt.Errorf("agent required for skill %s", t.Skill)
			} else {
				result, err = h.executeAgent(ctx, perm, t.Skill, historySnap[lastIdx])
			}
		} else {
			err = fmt.Errorf("no message history for skill %s", t.Skill)
		}
	case SkillCodeEdit, SkillCodeReview, SkillFullTask:
		if len(historySnap) > 0 {
			result, err = h.executeAgent(ctx, perm, t.Skill, historySnap[len(historySnap)-1])
		} else {
			err = fmt.Errorf("no message history for skill %s", t.Skill)
		}
	default:
		err = fmt.Errorf("unsupported skill: %s", t.Skill)
	}

	// Check if task was canceled *before* execution completed.
	// When continueTask cancels this context to resume the task, we must NOT
	// mark it as Canceled — the task was resumed, not cancelled by the client.
	canceled := ctx.Err() == context.Canceled

	if canceled {
		// Only mark as Canceled if the task is still in Working state.
		// If continueTask already moved it to Working with a new context,
		// this old goroutine should silently exit without overriding the state.
		h.mu.Lock()
		currentState := t.Status.State
		_, hasActiveCancel := h.cancels[t.ID]
		h.mu.Unlock()
		if currentState == TaskStateWorking {
			// If OUR context was cancelled but another cancel exists in the
			// map, continueTask installed a NEW context for the resumed task
			// and we are the stale goroutine. Exit WITHOUT calling
			// cleanupCancel — that would call the NEW cancel (destroying the
			// resumed task's context) and delete it from the map (leaving the
			// resumed task uncancelable). Just return and let G2 proceed.
			if hasActiveCancel {
				return
			}
			h.updateStatus(t, TaskStateCanceled, "canceled by client")
			h.cleanupCancelIf(t.ID, installedGen)
		}
		return
	}

	if err != nil {
		h.updateStatus(t, TaskStateFailed, err.Error())
		h.cleanupCancelIf(t.ID, installedGen)
		return
	}

	h.cleanupCancelIf(t.ID, installedGen)

	// If RequestInput was called during execution, the task is already in
	// input-required state. Don't override it with completed — the client
	// needs to send a follow-up message via continueTask.
	h.mu.Lock()
	currentState := t.Status.State
	h.mu.Unlock()
	// If the task is no longer Working (e.g., it was canceled or put into
	// input-required state while execution was finishing), don't override.
	if currentState != TaskStateWorking {
		return
	}

	h.mu.Lock()
	t.Artifacts = []Artifact{{
		ArtifactID: generateID(),
		Parts: []Part{{
			Kind: "text",
			Text: result,
		}},
	}}
	h.mu.Unlock()
	h.updateStatus(t, TaskStateCompleted, "")
}

// executeDirectTool runs a tool directly without spinning up a full agent loop.
// executeAgent runs a full agent loop with restricted permissions.
func (h *TaskHandler) executeAgent(ctx context.Context, perm *SkillPermission, skill string, msg Message) (string, error) {
	text := extractText(msg)
	if text == "" {
		return "", fmt.Errorf("empty input")
	}

	if h.agent == nil {
		return "", fmt.Errorf("no agent available for skill %s", skill)
	}

	// Create a restricted agent for A2A tasks.
	// Iteration limit is controlled by the task timeout, not max iterations.
	// Only enforce MaxIterations if it's explicitly set (> 0).
	maxIter := perm.MaxIterations

	// #565 A: enforce the skill allowlist on the AGENT path too. The
	// whitelist was previously only checked in executeDirectTool, while the
	// production wiring (root.go) ALWAYS has an agent — so every real
	// request ran this loop with the FULL registry and a read-only skill
	// (file-search) could invoke write_file / run_command. nil/empty
	// AllowedTools = unrestricted (full-task).
	reg := h.agent.ToolRegistry()
	if len(perm.AllowedTools) > 0 {
		reg = restrictRegistry(reg, perm.AllowedTools)
	}
	a := agent.NewAgent(h.agent.Provider(), reg, h.agent.SystemPrompt(), maxIter)

	prompt := buildAgentPrompt(skill, text)

	var buf strings.Builder
	err := a.RunStream(ctx, prompt, func(event provider.StreamEvent) {
		defer safego.Recover("a2a.executeAgent.streamCallback")
		if event.Type == provider.StreamEventText {
			buf.WriteString(event.Text)
		}
	})
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// cancelEntry pairs a cancel func with the generation that installed it.
type cancelEntry struct {
	gen    uint64
	cancel context.CancelFunc
}

// installCancelLocked installs a cancel for a task and returns its unique
// generation number. Must be called with h.mu held.
func (h *TaskHandler) installCancelLocked(taskID string, cancel context.CancelFunc) uint64 {
	h.cancelGen++
	gen := h.cancelGen
	h.cancels[taskID] = cancelEntry{gen: gen, cancel: cancel}
	return gen
}

// cleanupCancelIf removes and calls the cancel func for a task only when the
// map entry is still the one this run installed (identified by generation
// number, fix #166/#327). If continueTask has already swapped in a new cancel
// for the resumed run, this is a stale goroutine exiting — touching the new
// cancel would kill or zombie the resumed task, so we leave it untouched.
// Generation numbers are used instead of reflect pointer comparison because
// context.WithTimeout cancels are closures sharing the same code pointer,
// making reflect.ValueOf(fn).Pointer() comparison always-true (issue #327).
func (h *TaskHandler) cleanupCancelIf(taskID string, installedGen uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if entry, ok := h.cancels[taskID]; ok && entry.gen == installedGen {
		entry.cancel()
		delete(h.cancels, taskID)
	}
}

func (h *TaskHandler) updateStatus(t *Task, state TaskState, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Don't override an existing terminal state with a different one.
	// This prevents CancelTask's Canceled from being silently overwritten
	// by execute()'s Completed when the two race.
	if t.Status.State.IsTerminal() && state != t.Status.State {
		debug.Log("a2a", "task %s already terminal (%s), not overriding with %s", t.ID, t.Status.State, state)
		return
	}
	// #447: input-required is pseudo-terminal — only continueTask may move
	// a task out of it. execute()'s check-then-act window (read state under
	// lock, release, then updateStatus) let Completed/Failed overwrite an
	// input-required state set by RequestInput in between, breaking the
	// client's follow-up flow.
	if t.Status.State == TaskStateInputRequired && state != TaskStateInputRequired {
		debug.Log("a2a", "task %s is input-required, not overriding with %s", t.ID, state)
		return
	}
	t.Status = TaskStatus{State: state, Timestamp: time.Now()}
	t.UpdatedAt = time.Now()
	if message != "" {
		t.History = append(t.History, Message{
			Role: "agent",
			Parts: []Part{{
				Kind: "text",
				Text: message,
			}},
		})
	}
	if state.IsTerminal() && t.done != nil {
		close(t.done)
		t.done = nil
	}
	debug.Log("a2a", "task %s → %s", t.ID, state)

	// Fire event callback (outside lock — copy fn ref).
	if fn := h.onTaskEvent; fn != nil {
		eventType := ""
		switch state {
		case TaskStateWorking:
			eventType = "start"
		case TaskStateCompleted:
			eventType = "complete"
		case TaskStateFailed:
			eventType = "fail"
		case TaskStateCanceled:
			eventType = "cancel"
		case TaskStateInputRequired:
			eventType = "input-required"
		}
		if eventType != "" {
			msg := TaskEventMessage{
				Type:   eventType,
				TaskID: t.ID,
				Skill:  t.Skill,
			}
			switch eventType {
			case "start":
				startMsg := ""
				if len(t.History) > 0 {
					startMsg = truncateText(extractText(t.History[0]), 60)
				}
				msg.Message = fmt.Sprintf("A2A task started [%s] %s", t.Skill, startMsg)
			case "complete":
				msg.Message = fmt.Sprintf("A2A task completed [%s]", t.Skill)
			case "fail":
				msg.Message = fmt.Sprintf("A2A task failed [%s]", t.Skill)
				msg.Error = message
			case "cancel":
				msg.Message = fmt.Sprintf("A2A task canceled [%s]", t.Skill)
			case "input-required":
				msg.Message = fmt.Sprintf("A2A task waiting for input [%s]", t.Skill)
			}
			// Call async to avoid deadlock (callback may call back into handler).
			msgCopy := msg
			safego.Go("a2a.taskEvent", func() { fn(msgCopy) })
		}
	}

	// Fire push notification callbacks (independent of onTaskEvent).
	// Async per #1031: the production notifier (server.firePushNotifications)
	// runs validatePushCallbackURL - DNS resolution with a 3s timeout per
	// config — before delivery. Calling it synchronously under h.mu blocks
	// every task operation (GetTask/ListTasks/CancelTask/new runs) for up to
	// 3s x N on each status transition. Same async pattern as onTaskEvent
	// above and CancelTask's snapshot-then-fire.
	if pn := h.pushNotifier; pn != nil {
		snapshot := t.Snapshot()
		taskID := t.ID
		final := state.IsTerminal()
		safego.Go("a2a.pushNotify", func() {
			pn(taskID, StreamResponse{
				StatusUpdate: &TaskStatusUpdateEvent{
					TaskID: taskID,
					Status: snapshot.Status,
					Final:  final,
				},
			})
		})
	}
}

func (h *TaskHandler) SetPushNotifier(fn func(taskID string, payload StreamResponse)) {
	h.mu.Lock()
	h.pushNotifier = fn
	h.mu.Unlock()
}

// GetTask returns a snapshot of the current state of a task.
func (h *TaskHandler) GetTask(id string) (*Task, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.tasks[id]
	if !ok {
		return nil, false
	}
	snap := t.Snapshot()
	return &snap, true
}

// ListTasks returns a page of tasks and a next-page token (cursor pagination).
// A stale/invalid pageToken (e.g. the task it pointed at was cleaned up by
// cleanupExpiredTasksLocked) returns an error instead of silently restarting
// from the first page — which previously produced a nextToken identical to
// the stale one and looped forever in nextToken==""-terminated pagination
// clients (fix #258).
func (h *TaskHandler) ListTasks(pageToken string, pageSize int) ([]Task, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Collect all task IDs in creation order.
	ids := make([]string, 0, len(h.tasks))
	for id := range h.tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	start := 0
	if pageToken != "" {
		found := false
		for i, id := range ids {
			if id == pageToken {
				start = i
				found = true
				break
			}
		}
		if !found {
			return nil, "", fmt.Errorf("invalid page token %q: task no longer exists", pageToken)
		}
	}

	end := start + pageSize
	if end > len(ids) {
		end = len(ids)
	}

	result := make([]Task, 0, end-start)
	for _, id := range ids[start:end] {
		snap := h.tasks[id].Snapshot()
		result = append(result, snap)
	}

	var nextToken string
	if end < len(ids) {
		nextToken = ids[end]
	}
	return result, nextToken, nil
}

// GetTaskDone returns the notification channel for a task.
// The channel is closed when the task reaches a terminal state.
// Returns nil if the task doesn't exist.
func (h *TaskHandler) GetTaskDone(id string) <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.tasks[id]
	if !ok {
		return nil
	}
	return t.done
}

// CancelTask cancels a running task by canceling its context.
func (h *TaskHandler) CancelTask(id string) error {
	h.mu.Lock()
	t, ok := h.tasks[id]
	if !ok {
		h.mu.Unlock()
		return fmt.Errorf("task not found: %s", id)
	}
	if t.Status.State == TaskStateCanceled {
		// Already canceled — idempotent success.
		h.mu.Unlock()
		return nil
	}
	if t.Status.IsTerminal() {
		err := fmt.Errorf("task already in terminal state: %s", t.Status.State)
		h.mu.Unlock()
		return err
	}
	t.Status = TaskStatus{State: TaskStateCanceled, Timestamp: time.Now()}
	t.UpdatedAt = time.Now()
	// Close the done channel so any waiter on Done() unblocks.
	// updateStatus would normally handle this, but CancelTask already
	// holds the lock (updateStatus would self-deadlock on sync.Mutex).
	if t.done != nil {
		close(t.done)
		t.done = nil
	}
	// Cancel the underlying context to stop tool/agent execution.
	if entry, ok := h.cancels[id]; ok {
		entry.cancel()
		delete(h.cancels, id)
	}
	// #598: fire the same dual callbacks every other terminal transition
	// sends (updateStatus fires onTaskEvent + pushNotifier). CancelTask
	// previously set Canceled/closed done/cancelled the ctx but never
	// notified — webhook/SSE subscribers watched the task go silent
	// forever, and the execute goroutine's rescue path (which requires
	// state==Working) skipped because the state had already changed.
	// Snapshot under the lock, fire outside it (callbacks may re-enter).
	taskID := t.ID
	skill := t.Skill
	snapshot := t.Snapshot()
	eventFn := h.onTaskEvent
	pushFn := h.pushNotifier
	h.mu.Unlock()
	if eventFn != nil {
		msg := TaskEventMessage{
			Type:    "cancel",
			TaskID:  taskID,
			Skill:   skill,
			Message: fmt.Sprintf("A2A task canceled [%s]", skill),
		}
		safego.Go("a2a.taskEvent", func() { eventFn(msg) })
	}
	if pushFn != nil {
		// Async per #1049: same DNS blocking issue as #1031 updateStatus.
		// Snapshot before async: taskID, snapshot.Status, and Final are fixed.
		safego.Go("a2a.pushNotify", func() {
			pushFn(taskID, StreamResponse{
				StatusUpdate: &TaskStatusUpdateEvent{
					TaskID: taskID,
					Status: snapshot.Status,
					Final:  true,
				},
			})
		})
	}
	return nil
}

// RequestInput puts a task into input-required state and returns.
// The caller should then wait for the client to send a follow-up message.
// Notifies push subscribers and onTaskEvent callbacks (per #1050).
func (h *TaskHandler) RequestInput(id string, prompt string) error {
	h.mu.Lock()
	t, ok := h.tasks[id]
	if !ok {
		h.mu.Unlock()
		return fmt.Errorf("task not found: %s", id)
	}
	if t.Status.State != TaskStateWorking {
		h.mu.Unlock()
		return fmt.Errorf("can only request input from working state, current: %s", t.Status.State)
	}
	t.Status = TaskStatus{State: TaskStateInputRequired, Timestamp: time.Now()}
	t.UpdatedAt = time.Now()
	if prompt != "" {
		t.History = append(t.History, Message{
			Role: "agent",
			Parts: []Part{{
				Kind: "text",
				Text: prompt,
			}},
		})
	}
	// Close the done channel so SSE clients and Done() waiters unblock.
	// Input-required is a pseudo-terminal state: the SSE handler will send
	// a non-final status update (IsTerminal() == false) and then end the
	// stream, signalling the client to send a follow-up message.
	// continueTask re-creates the channel before resuming execution.
	if t.done != nil {
		close(t.done)
		t.done = nil
	}
	// Snapshot under the lock, fire outside it (callbacks may re-enter).
	taskID := t.ID
	skill := t.Skill
	snapshot := t.Snapshot()
	eventFn := h.onTaskEvent
	pushFn := h.pushNotifier
	h.mu.Unlock()
	debug.Log("a2a", "task %s → input-required", taskID)

	// Notify onTaskEvent (async, avoids callback under lock).
	if eventFn != nil {
		msg := TaskEventMessage{
			Type:    "input-required",
			TaskID:  taskID,
			Skill:   skill,
			Message: fmt.Sprintf("A2A task waiting for input [%s]", skill),
		}
		safego.Go("a2a.taskEvent", func() { eventFn(msg) })
	}
	// Notify push subscribers (async per #1050, same DNS blocking issue as #1031).
	if pushFn != nil {
		safego.Go("a2a.pushNotify", func() {
			pushFn(taskID, StreamResponse{
				StatusUpdate: &TaskStatusUpdateEvent{
					TaskID: taskID,
					Status: snapshot.Status,
					Final:  false,
				},
			})
		})
	}
	return nil
}

// ActiveTaskCount returns the number of non-terminal tasks.
func (h *TaskHandler) ActiveTaskCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, t := range h.tasks {
		if !t.Status.IsTerminal() {
			count++
		}
	}
	return count
}

// WorkspaceMetadata returns the detected workspace metadata.
func (h *TaskHandler) WorkspaceMetadata() WorkspaceMeta {
	return h.meta
}

// ---------------------------------------------------------------------------
// Skill permissions
// ---------------------------------------------------------------------------

// SkillPermission defines what a skill can do.
type SkillPermission struct {
	AllowedTools  []string // nil = all tools allowed
	MaxIterations int      // 0 = unlimited
}

var skillPermissions = map[string]*SkillPermission{
	SkillFileSearch:  {AllowedTools: []string{"read_file", "list_directory", "search_files", "glob", "code_search"}, MaxIterations: 0},
	SkillGitOps:      {AllowedTools: []string{"git_status", "git_diff", "git_log"}, MaxIterations: 0},
	SkillCommandExec: {AllowedTools: []string{"run_command"}, MaxIterations: 0},
	SkillCodeEdit:    {AllowedTools: []string{"read_file", "write_file", "edit_file", "search_files"}, MaxIterations: 0},
	SkillCodeReview:  {AllowedTools: []string{"read_file", "list_directory", "search_files", "git_diff"}, MaxIterations: 0},
	SkillFullTask:    {AllowedTools: nil, MaxIterations: 0}, // nil = all tools, 0 = unlimited
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isToolAllowed(toolName string, allowed []string) bool {
	if len(allowed) == 0 {
		return true // nil/empty = all tools allowed
	}
	for _, a := range allowed {
		if a == toolName {
			return true
		}
	}
	return false
}

// restrictRegistry builds a registry exposing only the tools named in
// allowed (fix #565 A). Tool instances are shared with src — the same
// ownership model as the previous direct pass-through; the agent package
// never CloseAlls a registry it did not create — but only the allowlisted
// subset is reachable by the agent loop. Unknown names are skipped.
func restrictRegistry(src *tool.Registry, allowed []string) *tool.Registry {
	restricted := tool.NewRegistry()
	if src == nil {
		return restricted
	}
	for _, name := range allowed {
		t, ok := src.Get(name)
		if !ok {
			continue // tool not registered on this instance — nothing to expose
		}
		_ = restricted.Register(t)
	}
	return restricted
}

func extractText(msg Message) string {
	var parts []string
	for _, p := range msg.Parts {
		if p.Kind == "text" {
			parts = append(parts, p.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func buildAgentPrompt(skill string, text string) string {
	switch skill {
	case SkillCodeReview:
		return "Review the following code and provide detailed feedback:\n\n" + text
	case SkillCodeEdit:
		return "Make the following code changes:\n\n" + text
	case SkillFullTask:
		return text
	default:
		return text
	}
}

// maxCompletedAge is the maximum time to keep terminal tasks in memory.
const maxCompletedAge = 30 * time.Minute

// maxInputRequiredAge is the maximum time to keep abandoned input-required tasks.
// These are not terminal (the client may still send follow-up input), but if
// left unattended they leak memory and cancel funcs indefinitely.
const maxInputRequiredAge = 2 * time.Hour

// cleanupExpiredTasksLocked removes terminal tasks older than maxCompletedAge.
// Also removes abandoned input-required tasks older than maxInputRequiredAge.
// Must be called with h.mu held.
func (h *TaskHandler) cleanupExpiredTasksLocked() {
	now := time.Now()
	for id, t := range h.tasks {
		if t.Status.IsTerminal() && now.Sub(t.UpdatedAt) > maxCompletedAge {
			delete(h.tasks, id)
			delete(h.cancels, id)
		}
		// Clean up abandoned input-required tasks that haven't been updated
		// within maxInputRequiredAge. These are not terminal but if the client
		// disconnected without follow-up, they leak memory forever.
		if t.Status.State == TaskStateInputRequired && now.Sub(t.UpdatedAt) > maxInputRequiredAge {
			delete(h.tasks, id)
			delete(h.cancels, id)
		}
	}

	// Reap messageId idempotency entries whose task is gone (#565 G) so the
	// index cannot outlive its tasks or grow unboundedly.
	for mid, tid := range h.messageIndex {
		if _, ok := h.tasks[tid]; !ok {
			delete(h.messageIndex, mid)
		}
	}
}

var taskSeq uint64

func generateID() string {
	n := atomic.AddUint64(&taskSeq, 1)
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), n)
}
