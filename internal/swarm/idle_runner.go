package swarm

import (
	"context"
	"fmt"
	runtimedebug "runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/util"
)

// runTeammateLoop is the core idle loop for a teammate.
// It processes messages from the inbox AND periodically polls the team's
// task board for pending tasks to claim.
// When a "task" message arrives or a pending task is claimed, it uses the
// agent to execute it.
// On "shutdown" or context cancellation, it exits.
func runTeammateLoop(
	ctx context.Context,
	tm *Teammate,
	team *Team,
	agent AgentRunner,
	mgr *Manager,
	onEvent func(Event),
	taskTimeout time.Duration,
) {
	// Panic recovery: ensure we always mark the teammate as done.
	defer teammatePanicGuard(tm, team, mgr, onEvent)()

	tm.mu.Lock()
	tm.StartedAt = time.Now()
	tm.mu.Unlock()

	// Poll ticker: how often to check the task board for pending tasks.
	pollInterval := mgr.cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	poller := time.NewTicker(pollInterval)
	defer poller.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case msg, ok := <-tm.Inbox:
			if !ok {
				// inbox closed
				return
			}
			if handleInboxMsg(ctx, tm, team, agent, mgr, onEvent, taskTimeout, msg) {
				return
			}

		case <-poller.C:
			if ctx.Err() != nil {
				return
			}
			// Only poll when idle — skip if already working on something.
			if tm.getStatus() != TeammateIdle {
				continue
			}
			tryClaimPendingTask(ctx, tm, team, agent, mgr, onEvent, taskTimeout)
		}
	}
}

// teammatePanicGuard returns the deferred shutdown closure for the teammate
// loop: on panic it logs, rolls the in-flight board task back (#1688), marks
// the teammate shutting down, and emits teammate_shutdown; in all cases it
// signals this goroutine has exited by closing tm.done exactly once.
func teammatePanicGuard(tm *Teammate, team *Team, mgr *Manager, onEvent func(Event)) func() {
	return func() {
		if r := recover(); r != nil {
			debug.Log("swarm", "teammate panic recovered teammate=%s error=%v stack=%s", tm.ID, r, string(runtimedebug.Stack()))
			// #1688 case 1: roll the in-flight task BACK on the board - the
			// old recovery only marked the teammate done, leaving the task
			// stuck in_progress forever (claim only scans pending), so every
			// BlockedBy dependent was blocked by allBlockersComplete forever.
			rollbackClaimedTask(mgr, team, tm)
			tm.setStatus(TeammateShuttingDown)
			if onEvent != nil {
				onEvent(Event{
					Type:       "teammate_shutdown",
					TeamID:     team.ID,
					TeammateID: tm.ID,
					Error:      fmt.Errorf("teammate panic: %v", r),
					Timestamp:  time.Now(),
				})
			}
		}
		// Signal that this goroutine has exited.
		tm.mu.Lock()
		if tm.done != nil {
			select {
			case <-tm.done:
			default:
				close(tm.done)
			}
		}
		tm.mu.Unlock()
	}
}

// handleInboxMsg processes one inbox message. It returns true when the
// teammate loop should exit (shutdown message or cancelled context).
func handleInboxMsg(
	ctx context.Context,
	tm *Teammate,
	team *Team,
	agent AgentRunner,
	mgr *Manager,
	onEvent func(Event),
	taskTimeout time.Duration,
	msg MailMessage,
) bool {
	switch msg.Type {
	case "shutdown":
		return true
	case "task_available":
		if ctx.Err() != nil {
			return true
		}
		// Hint: try claiming immediately instead of waiting for poller.
		if tm.getStatus() == TeammateIdle {
			tryClaimPendingTask(ctx, tm, team, agent, mgr, onEvent, taskTimeout)
		}
	case "task", "message", "":
		if ctx.Err() != nil {
			return true
		}
		handleMessage(ctx, tm, team, agent, mgr, onEvent, taskTimeout, msg)
	}
	return false
}

// handleMessage processes an inbox message (task or general message).
func handleMessage(
	ctx context.Context,
	tm *Teammate,
	team *Team,
	agent AgentRunner,
	mgr *Manager,
	onEvent func(Event),
	taskTimeout time.Duration,
	msg MailMessage,
) {
	if agent == nil {
		if onEvent != nil {
			onEvent(Event{
				Type:       "teammate_error",
				TeamID:     team.ID,
				TeammateID: tm.ID,
				Error:      fmt.Errorf("teammate %q has no agent (factory or toolBuilder may be nil)", tm.ID),
				Timestamp:  time.Now(),
			})
		}
		// Reply to caller so they don't block forever on ReplyTo channel.
		if msg.ReplyTo != nil {
			select {
			case msg.ReplyTo <- TaskResult{Error: fmt.Errorf("teammate %q has no agent", tm.ID)}:
			case <-ctx.Done():
			}
		}
		return
	}

	tm.mu.Lock()
	tm.CurrentTaskID = "" // direct messages carry no board ID (#1688)
	tm.mu.Unlock()
	tm.setStatus(TeammateWorking)
	tm.setCurrentTask(util.Truncate(msg.Content, 100))

	if onEvent != nil {
		onEvent(Event{
			Type:         "teammate_working",
			TeamID:       team.ID,
			TeammateID:   tm.ID,
			TeammateName: tm.Name,
			Timestamp:    time.Now(),
		})
	}

	result, taskErr := executeTask(ctx, agent, msg, tm, onEvent, team, taskTimeout)

	// Send result back to caller if they requested it.
	if msg.ReplyTo != nil {
		select {
		case msg.ReplyTo <- TaskResult{Output: result, Error: taskErr}:
		case <-ctx.Done():
			// Caller cancelled or teammate is shutting down; don't block forever
			// on an unbuffered/abandoned ReplyTo channel.
		}
	}

	tm.setLastResult(result)

	debug.Log("swarm", "teammate %s setLastResult len=%d", tm.ID, len(result))

	// If context was cancelled (e.g. CancelAll), don't revert to idle.
	if ctx.Err() != nil {
		tm.setStatus(TeammateShuttingDown)
		return
	}
	tm.setStatus(TeammateIdle)
	tm.setCurrentTask("")

	if onEvent != nil {
		onEvent(Event{
			Type:         "teammate_idle",
			TeamID:       team.ID,
			TeammateID:   tm.ID,
			TeammateName: tm.Name,
			Result:       util.Truncate(result, 500),
			Error:        taskErr, // #1497: idle derived success from ev.Error; leaving it unset made failed tasks render green on tunnel clients
			Timestamp:    time.Now(),
		})
	}
}

// tryClaimPendingTask looks for a pending task on the team's task board and
// atomically claims it (pending → in_progress) via ExpectedStatus.
// This is the key bridge between swarm_task_create and teammate auto-pickup.
func tryClaimPendingTask(
	ctx context.Context,
	tm *Teammate,
	team *Team,
	agent AgentRunner,
	mgr *Manager,
	onEvent func(Event),
	taskTimeout time.Duration,
) {
	// No agent → nothing to do.
	if agent == nil {
		return
	}

	// Get the team's task manager (nil if no task board created yet).
	tmMgr := mgr.GetTaskManager(team.ID)
	if tmMgr == nil {
		return
	}

	pending := task.StatusPending
	for _, tk := range tmMgr.List() {
		if tk.Status != pending {
			continue
		}
		// Skip tasks assigned to a specific teammate that isn't us.
		// Those are delivered directly to the assignee's inbox by swarm_task_create.
		if !claimableBy(tm, tk) {
			continue
		}
		// Skip tasks with unmet dependencies — all BlockedBy tasks must be
		// completed before this task can be claimed. This prevents premature
		// execution of tasks that depend on other tasks' output.
		if !allBlockersComplete(tmMgr, tk) {
			continue
		}

		// Atomically claim: only succeeds if status is still pending.
		claimed, err := claimTask(tmMgr, tk.ID, tm.ID)
		if err != nil {
			// Another teammate beat us — continue to next task.
			continue
		}
		if onEvent != nil {
			onEvent(Event{Type: "team_board_updated", TeamID: team.ID, Timestamp: time.Now()})
		}

		executeClaimedTask(ctx, tm, team, agent, onEvent, taskTimeout, tmMgr, claimed)

		// Claimed and completed one task — break to let the next poll pick up more.
		return
	}
}

// claimableBy reports whether a pending task may be claimed by teammate tm:
// tasks assigned to another teammate are delivered directly to that
// assignee's inbox by swarm_task_create and must not be claimed off the board.
func claimableBy(tm *Teammate, tk task.Task) bool {
	assignee, ok := tk.Metadata["assignee"]
	return !ok || assignee == "" || assignee == tm.ID
}

// claimTask atomically claims a pending board task for owner via an
// ExpectedStatus CAS (pending → in_progress). A non-nil error means the task
// was concurrently modified (usually: another teammate claimed it first).
func claimTask(tmMgr *task.Manager, taskID, owner string) (task.Task, error) {
	pending := task.StatusPending
	inProgress := task.StatusInProgress
	return tmMgr.Update(taskID, task.UpdateOptions{
		ExpectedStatus: &pending,
		Status:         &inProgress,
		Owner:          &owner,
	})
}

// executeClaimedTask runs a claimed board task through the teammate working
// lifecycle. Terminal transitions (including context cancellation) are
// handled after the agent run below.
func executeClaimedTask(
	ctx context.Context,
	tm *Teammate,
	team *Team,
	agent AgentRunner,
	onEvent func(Event),
	taskTimeout time.Duration,
	tmMgr *task.Manager,
	claimed task.Task,
) {
	// Build prompt from the claimed task.
	prompt := buildTaskPrompt(claimed)

	tm.mu.Lock()
	tm.CurrentTaskID = claimed.ID // #1688: panic rollback needs the board ID
	tm.mu.Unlock()
	tm.setStatus(TeammateWorking)
	tm.setCurrentTask(util.Truncate(claimed.Subject, 100))

	if onEvent != nil {
		onEvent(Event{
			Type:         "teammate_working",
			TeamID:       team.ID,
			TeammateID:   tm.ID,
			TeammateName: tm.Name,
			Timestamp:    time.Now(),
		})
	}

	// Execute the task via agent.
	msg := MailMessage{Content: prompt, Type: "task"}
	result, taskErr := executeTask(ctx, agent, msg, tm, onEvent, team, taskTimeout)

	tm.setLastResult(result)

	// If context was cancelled (e.g. CancelAll), don't mark a terminal task
	// state — roll the claim back to pending and shut down instead.
	if ctx.Err() != nil {
		pending := task.StatusPending
		owner := ""
		tmMgr.Update(claimed.ID, task.UpdateOptions{Status: &pending, Owner: &owner})
		if onEvent != nil {
			onEvent(Event{Type: "team_board_updated", TeamID: team.ID, Timestamp: time.Now()})
		}
		tm.setStatus(TeammateShuttingDown)
		return
	}

	finishClaimedTask(tmMgr, tm, team, onEvent, claimed, result, taskErr)
}

// finishClaimedTask drives a finished (non-cancelled) board task to its
// terminal status: completed on success, completed(permanent_error) on
// permanent LLM failure, and the #1295 capped retry-or-park on transient
// failure; then returns the teammate to idle and emits the idle event.
func finishClaimedTask(
	tmMgr *task.Manager,
	tm *Teammate,
	team *Team,
	onEvent func(Event),
	claimed task.Task,
	result string,
	taskErr error,
) {
	if taskErr != nil {
		// Check if the error is permanent (quota exhaustion or auth failure).
		// If so, don't revert to pending — another teammate would hit the
		// same permanent failure, creating a wasteful retry loop.
		fc := provider.ClassifyLLMError(taskErr)
		if fc == provider.FailureQuota || fc == provider.FailureAuth {
			// Mark task as completed with error metadata so it's not re-claimed.
			// Using "completed" instead of adding a new "failed" status keeps the
			// task board consistent — the metadata records the permanent failure.
			errMsg := util.Truncate(taskErr.Error(), 200)
			_ = parkTaskCompleted(tmMgr, claimed.ID, nil, map[string]string{
				"permanent_error": fc.String(),
				"error":           errMsg,
			})
			debug.Log("swarm", "teammate %s task %s permanently failed (%s): %v",
				tm.ID, claimed.ID, fc, taskErr)
		} else {
			// Transient error — revert to pending so another teammate can
			// retry, BUT capped (#1295): without a limit, a poison task
			// (one that deterministically hits the same 5xx/EOF/DNS/timeout)
			// was re-claimed every tick forever, burning a full LLM
			// retry+fallback chain per attempt and starving later tasks.
			parkTransientFailure(tmMgr, tm, claimed, taskErr)
		}
	} else {
		_ = parkTaskCompleted(tmMgr, claimed.ID, nil, nil)
	}
	if onEvent != nil {
		onEvent(Event{Type: "team_board_updated", TeamID: team.ID, Timestamp: time.Now()})
	}
	tm.setStatus(TeammateIdle)
	tm.setCurrentTask("")
	// #2579: clear CurrentTaskID once the task is terminal (success,
	// park, or error) — a stale ID lets a later panic rollback flip an
	// already-completed task back to pending for re-execution.
	tm.mu.Lock()
	tm.CurrentTaskID = ""
	tm.mu.Unlock()

	if onEvent != nil {
		onEvent(Event{
			Type:         "teammate_idle",
			TeamID:       team.ID,
			TeammateID:   tm.ID,
			TeammateName: tm.Name,
			Result:       util.Truncate(result, 500),
			Error:        taskErr, // #1497: same as the inbox-task idle event above
			Timestamp:    time.Now(),
		})
	}
}

// parkTransientFailure applies the #1295 transient-retry policy to a task
// that failed with a non-permanent error: under the cap it goes back to
// pending (with retry_attempts bumped) for another teammate to retry; at the
// cap it is parked as completed(max_retries) so a poison task cannot loop.
func parkTransientFailure(tmMgr *task.Manager, tm *Teammate, claimed task.Task, taskErr error) {
	attempts := nextRetryAttempts(claimed.Metadata)
	if attempts >= maxTransientTaskRetries {
		errMsg := util.Truncate(taskErr.Error(), 200)
		_ = parkTaskCompleted(tmMgr, claimed.ID, nil, map[string]string{
			"permanent_error": "max_retries_exceeded",
			"error":           errMsg,
			"retry_attempts":  strconv.Itoa(attempts),
		})
		debug.Log("swarm", "teammate %s task %s exceeded %d transient retries, parking as completed(max_retries): %v",
			tm.ID, claimed.ID, maxTransientTaskRetries, taskErr)
		return
	}
	pending := task.StatusPending
	owner := ""
	tmMgr.Update(claimed.ID, task.UpdateOptions{
		Status:   &pending,
		Owner:    &owner,
		Metadata: map[string]string{"retry_attempts": strconv.Itoa(attempts)},
	})
}

// nextRetryAttempts reads the retry_attempts metadata key and returns the
// incremented attempt count (#1295). Shared by the taskErr path and the
// panic-rollback path so the cap semantics cannot drift apart.
func nextRetryAttempts(meta map[string]string) int {
	attempts := 0
	if v, ok := meta["retry_attempts"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			attempts = n
		}
	}
	return attempts + 1
}

// parkTaskCompleted marks a board task completed with metadata, optionally
// guarded by an ExpectedStatus CAS (#2579). It is the shared terminal shape
// for every "completed" park: success, permanent LLM failure, the #1295
// transient-retry cap, and the #2118 panic-rollback cap.
func parkTaskCompleted(board *task.Manager, taskID string, expected *task.TaskStatus, meta map[string]string) error {
	completed := task.StatusCompleted
	opts := task.UpdateOptions{Status: &completed, Metadata: meta}
	if expected != nil {
		opts.ExpectedStatus = expected
	}
	_, err := board.Update(taskID, opts)
	return err
}

// maxTransientTaskRetries caps how many times a transiently-failing task
// bounces back to pending before it is parked (#1295).
const maxTransientTaskRetries = 3

// buildTaskPrompt constructs the agent prompt from a task.
func buildTaskPrompt(tk task.Task) string {
	var sb strings.Builder
	sb.WriteString("You have claimed a task from the team's task board.\n\n")
	sb.WriteString(fmt.Sprintf("Task: %s\n", tk.Subject))
	if tk.Description != "" {
		sb.WriteString(fmt.Sprintf("Description: %s\n", tk.Description))
	}
	if tk.ActiveForm != "" {
		sb.WriteString(fmt.Sprintf("Active form: %s\n", tk.ActiveForm))
	}
	for k, v := range tk.Metadata {
		sb.WriteString(fmt.Sprintf("%s: %s\n", k, v))
	}
	sb.WriteString("\nComplete this task now and return the final result. The teammate runner will update the task board when you finish.")
	return sb.String()
}

// rollbackClaimedTask reverts the teammate's in-flight board task to
// pending (#1688 case 1: panic recovery used to leave it in_progress
// forever - claim only scans pending, so BlockedBy dependents deadlocked).
func rollbackClaimedTask(mgr *Manager, team *Team, tm *Teammate) {
	if mgr == nil || team == nil {
		return
	}
	tm.mu.Lock()
	taskID := tm.CurrentTaskID
	tm.mu.Unlock()
	if taskID == "" {
		return
	}
	board := mgr.GetTaskManager(team.ID)
	if board == nil {
		return
	}
	// #2118: the panic path used to only flip Status back to pending,
	// bypassing the #1295 retry cap entirely (the cap is only read on the
	// taskErr path). A deterministically panicking task was then re-claimed
	// by each surviving teammate in turn - bounded only by the team size
	// (16), dismantling the whole team at one teammate per iteration.
	// Mirror the taskErr path: increment retry_attempts and park the task
	// as completed(max_retries) once the cap is hit.
	current, ok := board.Get(taskID)
	if !ok {
		debug.Log("swarm", "panic rollback: task=%s vanished from board", taskID)
		return
	}
	// #2579: guard both updates with ExpectedStatus=in_progress — the
	// rollback must only ever act on a task THIS teammate is still running.
	// A completed (or otherwise terminal) task fails the guard and the
	// rollback is a no-op, mirroring the claim's guard semantics.
	inProgress := task.StatusInProgress
	attempts := nextRetryAttempts(current.Metadata)
	if attempts >= maxTransientTaskRetries {
		if uerr := parkTaskCompleted(board, taskID, &inProgress, map[string]string{
			"permanent_error": "max_retries_exceeded",
			"error":           "teammate panicked repeatedly",
			"retry_attempts":  strconv.Itoa(attempts),
		}); uerr != nil {
			debug.Log("swarm", "panic rollback failed task=%s err=%v", taskID, uerr)
		}
		debug.Log("swarm", "panic rollback: task=%s exceeded %d retries (teammate panic), parking as completed(max_retries)", taskID, maxTransientTaskRetries)
		return
	}
	pending := task.StatusPending
	owner := ""
	if _, uerr := board.Update(taskID, task.UpdateOptions{ExpectedStatus: &inProgress, Status: &pending, Owner: &owner, Metadata: map[string]string{"retry_attempts": strconv.Itoa(attempts)}}); uerr != nil {
		debug.Log("swarm", "panic rollback failed task=%s err=%v", taskID, uerr)
	}
}

// streamCollector accumulates a teammate's streamed run: the full text
// output, turn-level text buffering (flushed on tool boundaries), and the
// last tool name used to attribute tool results. Each provider event is
// mirrored to the teammate event log and the team event bus.
type streamCollector struct {
	tm           *Teammate
	team         *Team
	onEvent      func(Event)
	output       strings.Builder
	textBuf      strings.Builder // accumulate text chunks into turn-level events
	lastToolName string
}

// flushText emits buffered text chunks as a single turn-level text event.
func (sc *streamCollector) flushText() {
	if sc.textBuf.Len() == 0 {
		return
	}
	text := sc.textBuf.String()
	sc.textBuf.Reset()
	sc.tm.appendEvent(TeammateEvent{Type: TeammateEventText, Text: text})
}

// handle dispatches one provider stream event to its typed handler.
func (sc *streamCollector) handle(event provider.StreamEvent) {
	switch event.Type {
	case provider.StreamEventText:
		sc.handleText(event)
	case provider.StreamEventReasoning:
		sc.handleReasoning(event)
	case provider.StreamEventToolCallDone:
		sc.handleToolCallDone(event)
	case provider.StreamEventToolResult:
		sc.handleToolResult(event)
	case provider.StreamEventError:
		sc.handleStreamError(event)
	}
}

func (sc *streamCollector) handleText(event provider.StreamEvent) {
	sc.output.WriteString(event.Text)
	sc.textBuf.WriteString(event.Text)
	if sc.onEvent != nil {
		sc.onEvent(Event{
			Type:         "teammate_text",
			TeamID:       sc.team.ID,
			TeammateID:   sc.tm.ID,
			TeammateName: sc.tm.Name,
			Result:       event.Text,
			Timestamp:    time.Now(),
		})
	}
}

func (sc *streamCollector) handleReasoning(event provider.StreamEvent) {
	if strings.TrimSpace(event.Text) == "" {
		return
	}
	sc.tm.appendEvent(TeammateEvent{Type: TeammateEventReasoning, Text: event.Text})
	if sc.onEvent != nil {
		sc.onEvent(Event{
			Type:         "teammate_reasoning",
			TeamID:       sc.team.ID,
			TeammateID:   sc.tm.ID,
			TeammateName: sc.tm.Name,
			Result:       event.Text,
			Timestamp:    time.Now(),
		})
	}
}

func (sc *streamCollector) handleToolCallDone(event provider.StreamEvent) {
	sc.flushText()
	debug.Log("swarm", "teammate %s tool call done", sc.tm.ID)
	sc.lastToolName = event.Tool.Name
	sc.tm.appendEvent(TeammateEvent{
		Type:     TeammateEventToolCall,
		ToolName: event.Tool.Name,
		ToolID:   event.Tool.ID,
		ToolArgs: string(event.Tool.Arguments),
	})
	if sc.onEvent != nil {
		sc.onEvent(Event{
			Type:         "teammate_tool_call",
			TeamID:       sc.team.ID,
			TeammateID:   sc.tm.ID,
			TeammateName: sc.tm.Name,
			CurrentTool:  event.Tool.Name,
			ToolID:       event.Tool.ID,
			ToolArgs:     string(event.Tool.Arguments),
			Timestamp:    time.Now(),
		})
	}
}

func (sc *streamCollector) handleToolResult(event provider.StreamEvent) {
	sc.flushText()
	sc.tm.appendEvent(TeammateEvent{
		Type:     TeammateEventToolResult,
		ToolName: sc.lastToolName,
		ToolID:   event.Tool.ID,
		Result:   event.Result,
		IsError:  event.IsError,
	})
	if sc.onEvent != nil {
		// #1012: the result text belongs in Result - it was written to
		// ToolArgs (which the Event doc says is for teammate_tool_call
		// arguments), leaving desktop consumers reading an empty Result.
		// The two tunnel consumers read ToolArgs "wrongly" in the same
		// way, cancelling out; they are fixed together here.
		sc.onEvent(Event{
			Type:         "teammate_tool_result",
			TeamID:       sc.team.ID,
			TeammateID:   sc.tm.ID,
			TeammateName: sc.tm.Name,
			CurrentTool:  sc.lastToolName,
			ToolID:       event.Tool.ID,
			Result:       event.Result,
			IsError:      event.IsError,
			Timestamp:    time.Now(),
		})
	}
}

func (sc *streamCollector) handleStreamError(event provider.StreamEvent) {
	sc.flushText()
	sc.output.WriteString(fmt.Sprintf("\n[error: %v]", event.Error))
	sc.tm.appendEvent(TeammateEvent{
		Type:    TeammateEventError,
		Text:    fmt.Sprintf("%v", event.Error),
		IsError: true,
	})
}

// executeTask runs the agent on a task message and collects the output.
func executeTask(
	ctx context.Context,
	agent AgentRunner,
	msg MailMessage,
	tm *Teammate,
	onEvent func(Event),
	team *Team,
	timeout time.Duration,
) (string, error) {
	// When timeout > 0, create a sub-context with deadline. When timeout == 0, no deadline.
	var subCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		subCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		subCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	prompt := msg.Content
	if msg.Summary != "" {
		prompt = fmt.Sprintf("Summary: %s\n%s", msg.Summary, msg.Content)
	}

	sc := &streamCollector{tm: tm, team: team, onEvent: onEvent}
	err := agent.RunStream(subCtx, prompt, sc.handle)
	sc.flushText()

	if err != nil {
		debug.Log("swarm", "teammate %s RunStream error: %v output_len=%d", tm.ID, err, sc.output.Len())
		appendRunFailureNote(&sc.output, err, subCtx)
	}

	debug.Log("swarm", "teammate %s task complete output_len=%d", tm.ID, sc.output.Len())
	return sc.output.String(), err
}

// appendRunFailureNote appends the human-readable failure note matching why
// RunStream returned: deadline, cancellation, or any other error.
func appendRunFailureNote(output *strings.Builder, err error, subCtx context.Context) {
	switch {
	case subCtx.Err() == context.DeadlineExceeded:
		output.WriteString("\n[timeout: task exceeded time limit]")
	case subCtx.Err() == context.Canceled:
		output.WriteString("\n[cancelled]")
	default:
		output.WriteString(fmt.Sprintf("\n[error: %v]", err))
	}
}

// allBlockersComplete returns true if every task listed in tk.BlockedBy has
// status "completed". Tasks with no BlockedBy entries return true.
func allBlockersComplete(tmMgr *task.Manager, tk task.Task) bool {
	if len(tk.BlockedBy) == 0 {
		return true
	}
	for _, blockerID := range tk.BlockedBy {
		blocker, ok := tmMgr.Get(blockerID)
		if !ok {
			// Blocker doesn't exist (deleted?) — treat as unmet dependency.
			return false
		}
		if blocker.Status != task.StatusCompleted {
			return false
		}
	}
	return true
}
