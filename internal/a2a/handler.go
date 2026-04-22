package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
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
	tasks     map[string]*Task              // active tasks by ID
	cancels   map[string]context.CancelFunc // per-task cancel functions
	meta      WorkspaceMeta
	maxTasks  int
	timeout   time.Duration
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

// Timeout returns the configured task timeout.
func (h *TaskHandler) Timeout() time.Duration {
	return h.timeout
}

// NewTaskHandler creates a handler bound to a specific workspace.
func NewTaskHandler(workspace string, a *agent.Agent, reg *tool.Registry, opts ...HandlerOption) *TaskHandler {
	h := &TaskHandler{
		workspace: workspace,
		agent:     a,
		registry:  reg,
		tasks:     make(map[string]*Task),
		cancels:   make(map[string]context.CancelFunc),
		meta:      detectWorkspaceMeta(workspace),
		maxTasks:  5,
		timeout:   5 * time.Minute,
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

	// Check concurrency limit.
	h.mu.Lock()

	// Prune old completed tasks.
	h.cleanupExpiredTasksLocked()

	active := 0
	for _, t := range h.tasks {
		if !t.Status.IsTerminal() {
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
		Status:    TaskStatus{State: TaskStateSubmitted},
		Skill:     skill,
		History:   []Message{input},
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
	}
	h.tasks[task.ID] = task
	h.mu.Unlock()

	// Create cancellable context for this task.
	taskCtx, cancel := context.WithTimeout(context.Background(), h.timeout)
	h.mu.Lock()
	h.cancels[task.ID] = cancel
	h.mu.Unlock()

	// Execute asynchronously.
	go h.execute(taskCtx, task, perm)

	snap := task.Snapshot()
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

	// Append the new user message to history.
	task.History = append(task.History, input)

	// Transition to working state while still holding the lock,
	// preventing CancelTask from racing between the check and goroutine start.
	task.Status = TaskStatus{State: TaskStateWorking}
	task.UpdatedAt = time.Now()
	// Re-create the done channel since it was closed when the previous
	// execution reached input-required (a pseudo-terminal state).
	task.done = make(chan struct{})

	// Re-create the cancel context for the resumed execution.
	taskCtx, cancel := context.WithTimeout(context.Background(), h.timeout)
	h.cancels[taskID] = cancel
	h.mu.Unlock()

	perm, ok := skillPermissions[task.Skill]
	if !ok {
		return nil, fmt.Errorf("unknown skill: %s", task.Skill)
	}

	// Resume execution.
	go h.execute(taskCtx, task, perm)

	snap := task.Snapshot()
	return &snap, nil
}

func (h *TaskHandler) execute(ctx context.Context, t *Task, perm *SkillPermission) {
	h.updateStatus(t, TaskStateWorking, "")

	var result string
	var err error

	switch t.Skill {
	case SkillFileSearch, SkillGitOps, SkillCommandExec:
		// If agent is available, use agent for all skills (smarter routing).
		// Fall back to direct tool execution only if no agent.
		if h.agent != nil {
			result, err = h.executeAgent(ctx, perm, t.Skill, t.History[0])
		} else {
			result, err = h.executeDirectTool(ctx, perm, t.Skill, t.History[0])
		}
	case SkillCodeEdit, SkillCodeReview, SkillFullTask:
		result, err = h.executeAgent(ctx, perm, t.Skill, t.History[0])
	default:
		err = fmt.Errorf("unsupported skill: %s", t.Skill)
	}

	// Check if task was canceled *before* execution completed.
	canceled := ctx.Err() == context.Canceled

	// Clean up cancel func.
	h.mu.Lock()
	if c, ok := h.cancels[t.ID]; ok {
		c()
		delete(h.cancels, t.ID)
	}
	h.mu.Unlock()

	if canceled {
		h.updateStatus(t, TaskStateCanceled, "canceled by client")
		return
	}

	if err != nil {
		h.updateStatus(t, TaskStateFailed, err.Error())
		return
	}

	t.Artifacts = []Artifact{{
		ArtifactID: generateID(),
		Parts: []Part{{
			Kind: "text",
			Text: result,
		}},
	}}
	h.updateStatus(t, TaskStateCompleted, "")
}

// executeDirectTool runs a tool directly without spinning up a full agent loop.
func (h *TaskHandler) executeDirectTool(ctx context.Context, perm *SkillPermission, skill string, msg Message) (string, error) {
	text := extractText(msg)
	if text == "" {
		return "", fmt.Errorf("empty input")
	}

	if h.registry == nil {
		return "", fmt.Errorf("no tool registry available")
	}

	// Pick the best tool for the skill.
	toolName := pickToolForSkill(skill, text)

	// Enforce permission whitelist (nil/empty = all tools allowed).
	if !isToolAllowed(toolName, perm.AllowedTools) {
		return "", fmt.Errorf("skill %s is not allowed to use tool %s", skill, toolName)
	}

	t, ok := h.registry.Get(toolName)
	if !ok {
		return "", fmt.Errorf("tool %s not found", toolName)
	}

	input := buildToolInput(toolName, text)
	result, err := t.Execute(ctx, input)
	if err != nil {
		return "", err
	}

	return result.Content, nil
}

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
	a := agent.NewAgent(h.agent.Provider(), h.agent.ToolRegistry(), h.agent.SystemPrompt(), maxIter)

	prompt := buildAgentPrompt(skill, text)

	var buf strings.Builder
	err := a.RunStream(ctx, prompt, func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventText {
			buf.WriteString(event.Text)
		}
	})
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (h *TaskHandler) updateStatus(t *Task, state TaskState, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t.Status = TaskStatus{State: state}
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
}

// GetTask returns a task by ID.
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
	defer h.mu.Unlock()
	t, ok := h.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	if t.Status.IsTerminal() {
		return fmt.Errorf("task already in terminal state: %s", t.Status.State)
	}
	t.Status = TaskStatus{State: TaskStateCanceled}
	t.UpdatedAt = time.Now()
	// Cancel the underlying context to stop tool/agent execution.
	if cancel, ok := h.cancels[id]; ok {
		cancel()
		delete(h.cancels, id)
	}
	return nil
}

// RequestInput puts a task into input-required state and returns.
// The caller should then wait for the client to send a follow-up message.
func (h *TaskHandler) RequestInput(id string, prompt string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	if t.Status.State != TaskStateWorking {
		return fmt.Errorf("can only request input from working state, current: %s", t.Status.State)
	}
	t.Status = TaskStatus{State: TaskStateInputRequired}
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
	ReadOnly      bool
	MaxIterations int // 0 = unlimited
}

var skillPermissions = map[string]*SkillPermission{
	SkillFileSearch:  {AllowedTools: []string{"read_file", "list_directory", "search_files", "glob"}, ReadOnly: true, MaxIterations: 0},
	SkillGitOps:      {AllowedTools: []string{"git_status", "git_diff", "git_log"}, ReadOnly: true, MaxIterations: 0},
	SkillCommandExec: {AllowedTools: []string{"run_command"}, ReadOnly: false, MaxIterations: 0},
	SkillCodeEdit:    {AllowedTools: []string{"read_file", "write_file", "edit_file", "search_files"}, ReadOnly: false, MaxIterations: 0},
	SkillCodeReview:  {AllowedTools: []string{"read_file", "list_directory", "search_files", "git_diff"}, ReadOnly: true, MaxIterations: 0},
	SkillFullTask:    {AllowedTools: nil, ReadOnly: false, MaxIterations: 0}, // nil = all tools, 0 = unlimited
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

func extractText(msg Message) string {
	var parts []string
	for _, p := range msg.Parts {
		if p.Kind == "text" {
			parts = append(parts, p.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func pickToolForSkill(skill string, input string) string {
	switch skill {
	case SkillFileSearch:
		if strings.Contains(input, "*") || strings.Contains(input, ".") {
			return "glob"
		}
		return "search_files"
	case SkillGitOps:
		input = strings.ToLower(input)
		if strings.Contains(input, "diff") {
			return "git_diff"
		}
		if strings.Contains(input, "log") {
			return "git_log"
		}
		return "git_status"
	case SkillCommandExec:
		return "run_command"
	default:
		return "search_files"
	}
}

func buildToolInput(toolName, text string) json.RawMessage {
	switch toolName {
	case "search_files":
		input, _ := json.Marshal(map[string]interface{}{"pattern": text, "max_results": 50})
		return input
	case "glob":
		input, _ := json.Marshal(map[string]interface{}{"pattern": text})
		return input
	case "git_status", "git_diff", "git_log":
		input, _ := json.Marshal(map[string]interface{}{})
		return input
	case "run_command":
		input, _ := json.Marshal(map[string]interface{}{"command": text})
		return input
	case "list_directory":
		input, _ := json.Marshal(map[string]interface{}{"path": "."})
		return input
	case "read_file":
		input, _ := json.Marshal(map[string]interface{}{"path": text})
		return input
	default:
		input, _ := json.Marshal(map[string]interface{}{"query": text})
		return input
	}
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

// cleanupExpiredTasksLocked removes terminal tasks older than maxCompletedAge.
// Must be called with h.mu held.
func (h *TaskHandler) cleanupExpiredTasksLocked() {
	now := time.Now()
	for id, t := range h.tasks {
		if t.Status.IsTerminal() && now.Sub(t.UpdatedAt) > maxCompletedAge {
			delete(h.tasks, id)
			delete(h.cancels, id)
		}
	}
}

var taskSeq uint64

func generateID() string {
	n := atomic.AddUint64(&taskSeq, 1)
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), n)
}
