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
	defer func() {
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
	}()

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
			switch msg.Type {
			case "shutdown":
				return
			case "task_available":
				if ctx.Err() != nil {
					return
				}
				// Hint: try claiming immediately instead of waiting for poller.
				if tm.getStatus() == TeammateIdle {
					tryClaimPendingTask(ctx, tm, team, agent, mgr, onEvent, taskTimeout)
				}
			case "task", "message", "":
				if ctx.Err() != nil {
					return
				}
				handleMessage(ctx, tm, team, agent, mgr, onEvent, taskTimeout, msg)
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

	// Find a pending task.
	pending := task.StatusPending
	inProgress := task.StatusInProgress

	for _, tk := range tmMgr.List() {
		if tk.Status != pending {
			continue
		}
		// Skip tasks assigned to a specific teammate that isn't us.
		// Those are delivered directly to the assignee's inbox by swarm_task_create.
		if assignee, ok := tk.Metadata["assignee"]; ok && assignee != "" && assignee != tm.ID {
			continue
		}
		// Skip tasks with unmet dependencies — all BlockedBy tasks must be
		// completed before this task can be claimed. This prevents premature
		// execution of tasks that depend on other tasks' output.
		if !allBlockersComplete(tmMgr, tk) {
			continue
		}

		// Atomically claim: only succeeds if status is still pending.
		owner := tm.ID
		claimed, err := tmMgr.Update(tk.ID, task.UpdateOptions{
			ExpectedStatus: &pending,
			Status:         &inProgress,
			Owner:          &owner,
		})
		if err != nil {
			// Another teammate beat us — continue to next task.
			continue
		}
		if onEvent != nil {
			onEvent(Event{Type: "team_board_updated", TeamID: team.ID, Timestamp: time.Now()})
		}

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
		// Mark task status based on whether agent execution succeeded.
		if taskErr != nil {
			// Check if the error is permanent (quota exhaustion or auth failure).
			// If so, don't revert to pending — another teammate would hit the
			// same permanent failure, creating a wasteful retry loop.
			fc := provider.ClassifyLLMError(taskErr)
			if fc == provider.FailureQuota || fc == provider.FailureAuth {
				// Mark task as completed with error metadata so it's not re-claimed.
				// Using "completed" instead of adding a new "failed" status keeps the
				// task board consistent — the metadata records the permanent failure.
				// #2786: parking also cascades the failure to BlockedBy dependents.
				parkTaskFailed(tmMgr, claimed.ID, fc.String(), util.Truncate(taskErr.Error(), 200), parkOpts{})
				debug.Log("swarm", "teammate %s task %s permanently failed (%s): %v",
					tm.ID, claimed.ID, fc, taskErr)
			} else {
				// Transient error — revert to pending so another teammate can
				// retry, BUT capped (#1295): without a limit, a poison task
				// (one that deterministically hits the same 5xx/EOF/DNS/timeout)
				// was re-claimed every tick forever, burning a full LLM
				// retry+fallback chain per attempt and starving later tasks.
				attempts := 0
				if v, ok := claimed.Metadata["retry_attempts"]; ok {
					if n, err := strconv.Atoi(v); err == nil {
						attempts = n
					}
				}
				attempts++
				if attempts >= maxTransientTaskRetries {
					// #2786: parking cascades the failure to BlockedBy dependents.
					parkTaskFailed(tmMgr, claimed.ID, "max_retries_exceeded", util.Truncate(taskErr.Error(), 200),
						parkOpts{extra: map[string]string{"retry_attempts": strconv.Itoa(attempts)}})
					debug.Log("swarm", "teammate %s task %s exceeded %d transient retries, parking as completed(max_retries): %v",
						tm.ID, claimed.ID, maxTransientTaskRetries, taskErr)
				} else {
					pending := task.StatusPending
					owner := ""
					tmMgr.Update(claimed.ID, task.UpdateOptions{
						Status:   &pending,
						Owner:    &owner,
						Metadata: map[string]string{"retry_attempts": strconv.Itoa(attempts)},
					})
				}
			}
		} else {
			completed := task.StatusCompleted
			// #2786: guard with in_progress — if the task was parked by a
			// cascade while we were still running (its own dependency failed),
			// a bare status overwrite would resurrect it as a clean success
			// and re-unlock its dependents.
			inProgressStatus := task.StatusInProgress
			if _, uerr := tmMgr.Update(claimed.ID, task.UpdateOptions{ExpectedStatus: &inProgressStatus, Status: &completed}); uerr != nil {
				debug.Log("swarm", "teammate %s task %s result discarded: %v", tm.ID, claimed.ID, uerr)
			}
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

		// Claimed and completed one task — break to let the next poll pick up more.
		return
	}
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
	attempts := 0
	if v, ok := current.Metadata["retry_attempts"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			attempts = n
		}
	}
	attempts++
	if attempts >= maxTransientTaskRetries {
		// #2786: parking cascades the failure to BlockedBy dependents.
		parkTaskFailed(board, taskID, "max_retries_exceeded", "teammate panicked repeatedly",
			parkOpts{extra: map[string]string{"retry_attempts": strconv.Itoa(attempts)}, expected: &inProgress})
		debug.Log("swarm", "panic rollback: task=%s exceeded %d retries (teammate panic), parking as completed(max_retries)", taskID, maxTransientTaskRetries)
		return
	}
	pending := task.StatusPending
	owner := ""
	if _, uerr := board.Update(taskID, task.UpdateOptions{ExpectedStatus: &inProgress, Status: &pending, Owner: &owner, Metadata: map[string]string{"retry_attempts": strconv.Itoa(attempts)}}); uerr != nil {
		debug.Log("swarm", "panic rollback failed task=%s err=%v", taskID, uerr)
	}
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

	var output strings.Builder
	var textBuf strings.Builder // accumulate text chunks into turn-level events
	lastToolName := ""
	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		text := textBuf.String()
		textBuf.Reset()
		tm.appendEvent(TeammateEvent{Type: TeammateEventText, Text: text})
	}
	err := agent.RunStream(subCtx, prompt, func(event provider.StreamEvent) {
		switch event.Type {
		case provider.StreamEventText:
			output.WriteString(event.Text)
			textBuf.WriteString(event.Text)
			if onEvent != nil {
				onEvent(Event{
					Type:         "teammate_text",
					TeamID:       team.ID,
					TeammateID:   tm.ID,
					TeammateName: tm.Name,
					Result:       event.Text,
					Timestamp:    time.Now(),
				})
			}
		case provider.StreamEventReasoning:
			if strings.TrimSpace(event.Text) == "" {
				return
			}
			tm.appendEvent(TeammateEvent{Type: TeammateEventReasoning, Text: event.Text})
			if onEvent != nil {
				onEvent(Event{
					Type:         "teammate_reasoning",
					TeamID:       team.ID,
					TeammateID:   tm.ID,
					TeammateName: tm.Name,
					Result:       event.Text,
					Timestamp:    time.Now(),
				})
			}
		case provider.StreamEventToolCallDone:
			flushText()
			debug.Log("swarm", "teammate %s tool call done", tm.ID)
			lastToolName = event.Tool.Name
			tm.appendEvent(TeammateEvent{
				Type:     TeammateEventToolCall,
				ToolName: event.Tool.Name,
				ToolID:   event.Tool.ID,
				ToolArgs: string(event.Tool.Arguments),
			})
			if onEvent != nil {
				onEvent(Event{
					Type:         "teammate_tool_call",
					TeamID:       team.ID,
					TeammateID:   tm.ID,
					TeammateName: tm.Name,
					CurrentTool:  event.Tool.Name,
					ToolID:       event.Tool.ID,
					ToolArgs:     string(event.Tool.Arguments),
					Timestamp:    time.Now(),
				})
			}
		case provider.StreamEventToolResult:
			flushText()
			tm.appendEvent(TeammateEvent{
				Type:     TeammateEventToolResult,
				ToolName: lastToolName,
				ToolID:   event.Tool.ID,
				Result:   event.Result,
				IsError:  event.IsError,
			})
			if onEvent != nil {
				// #1012: the result text belongs in Result - it was written to
				// ToolArgs (which the Event doc says is for teammate_tool_call
				// arguments), leaving desktop consumers reading an empty Result.
				// The two tunnel consumers read ToolArgs "wrongly" in the same
				// way, cancelling out; they are fixed together here.
				onEvent(Event{
					Type:         "teammate_tool_result",
					TeamID:       team.ID,
					TeammateID:   tm.ID,
					TeammateName: tm.Name,
					CurrentTool:  lastToolName,
					ToolID:       event.Tool.ID,
					Result:       event.Result,
					IsError:      event.IsError,
					Timestamp:    time.Now(),
				})
			}
		case provider.StreamEventError:
			flushText()
			output.WriteString(fmt.Sprintf("\n[error: %v]", event.Error))
			tm.appendEvent(TeammateEvent{
				Type:    TeammateEventError,
				Text:    fmt.Sprintf("%v", event.Error),
				IsError: true,
			})
		}
	})
	flushText()

	if err != nil {
		debug.Log("swarm", "teammate %s RunStream error: %v output_len=%d", tm.ID, err, output.Len())
		if subCtx.Err() == context.DeadlineExceeded {
			output.WriteString("\n[timeout: task exceeded time limit]")
		} else if subCtx.Err() == context.Canceled {
			output.WriteString("\n[cancelled]")
		} else {
			output.WriteString(fmt.Sprintf("\n[error: %v]", err))
		}
	}

	debug.Log("swarm", "teammate %s task complete output_len=%d", tm.ID, output.Len())
	return output.String(), err
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

// parkTaskFailed marks a permanently-failed task as completed with
// permanent_error metadata so it is never re-claimed, clears its owner, and
// cascades the failure to BlockedBy dependents (#2786). expected, when
// non-nil, guards the update like the claim/rollback paths (#2579).
func parkTaskFailed(board *task.Manager, taskID, reason, errMsg string, opts parkOpts) {
	meta := map[string]string{"permanent_error": reason, "error": errMsg}
	for k, v := range opts.extra {
		meta[k] = v
	}
	completed := task.StatusCompleted
	emptyOwner := ""
	if _, err := board.Update(taskID, task.UpdateOptions{
		ExpectedStatus: opts.expected,
		Status:         &completed,
		Owner:          &emptyOwner,
		Metadata:       meta,
	}); err != nil {
		debug.Log("swarm", "park failed task=%s err=%v", taskID, err)
		return
	}
	cascadeFailedDependency(board, taskID)
}

// parkOpts carries the optional parking variants: extra metadata entries and
// the #2579 ExpectedStatus guard used by the panic-rollback path.
type parkOpts struct {
	extra    map[string]string
	expected *task.TaskStatus
}

// cascadeFailedDependency parks every non-terminal task that (transitively)
// depends on the given failed task. The parking paths mark failed tasks
// completed so the board stays consistent (#1295), but allBlockersComplete
// only reads Status — a parked failure therefore "unblocked" its BlockedBy
// dependents, and teammates executed them against output that never existed
// (#2786). Propagating the failure along the reverse-dependency closure
// follows reliable-orchestration semantics (OrchestraBench, arXiv:2608.05263):
// a permanently failed node must fail its downstream, not unlock it.
func cascadeFailedDependency(board *task.Manager, failedID string) {
	failed := map[string]bool{failedID: true}
	queue := []string{failedID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, tk := range board.List() {
			if failed[tk.ID] || !containsString(tk.BlockedBy, id) {
				continue
			}
			if tk.Status != task.StatusPending && tk.Status != task.StatusInProgress {
				continue // already terminal (e.g. genuinely succeeded) — leave it
			}
			failed[tk.ID] = true
			queue = append(queue, tk.ID)
			completed := task.StatusCompleted
			emptyOwner := ""
			if _, err := board.Update(tk.ID, task.UpdateOptions{
				Status: &completed,
				Owner:  &emptyOwner,
				Metadata: map[string]string{
					"permanent_error":   "dependency_failed",
					"failed_dependency": id,
					"error":             fmt.Sprintf("blocked by failed task %s", id),
				},
			}); err != nil {
				debug.Log("swarm", "cascade park dependent task=%s err=%v", tk.ID, err)
			} else {
				debug.Log("swarm", "cascade: task=%s parked (dependency_failed) due to failed task=%s", tk.ID, id)
			}
		}
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
