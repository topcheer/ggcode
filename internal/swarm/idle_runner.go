package swarm

import (
	"context"
	"errors"
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

func runTeammateLoop(
	ctx context.Context,
	tm *Teammate,
	team *Team,
	agent AgentRunner,
	mgr *Manager,
	onEvent func(Event),
	taskTimeout time.Duration,
) {

	defer func() {
		if r := recover(); r != nil {
			debug.Log("swarm", "teammate panic recovered teammate=%s error=%v stack=%s", tm.ID, r, string(runtimedebug.Stack()))

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

		// #4367: record CurrentTaskID IMMEDIATELY after the claim succeeds,
		// before any code that can panic (probe consumption, onEvent external
		// callback, prompt building). The deferred rollbackClaimedTask reads
		// this field; with the old late write, a panic in that window rolled
		// back a no-op (ID still "" from the previous terminal clear) and the
		// task leaked in_progress forever (#1688 regression).

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

				return
			}
			switch msg.Type {
			case "shutdown":
				return
			case "task_available":
				if ctx.Err() != nil {
					return
				}

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

			if tm.getStatus() != TeammateIdle {
				continue
			}
			tryClaimPendingTask(ctx, tm, team, agent, mgr, onEvent, taskTimeout)
		}
	}
}

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

		if msg.ReplyTo != nil {
			select {
			case msg.ReplyTo <- TaskResult{Error: fmt.Errorf("teammate %q has no agent", tm.ID)}:
			case <-ctx.Done():
			}
		}
		return
	}

	tm.mu.Lock()
	tm.CurrentTaskID = ""
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

	if msg.ReplyTo != nil {
		select {
		case msg.ReplyTo <- TaskResult{Output: result, Error: taskErr}:
		case <-ctx.Done():

		}
	}

	tm.setLastResult(result)

	debug.Log("swarm", "teammate %s setLastResult len=%d", tm.ID, len(result))

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
			Error:        taskErr,
			Timestamp:    time.Now(),
		})
	}
}

func tryClaimPendingTask(
	ctx context.Context,
	tm *Teammate,
	team *Team,
	agent AgentRunner,
	mgr *Manager,
	onEvent func(Event),
	taskTimeout time.Duration,
) {

	if agent == nil {
		return
	}

	if !teammateClaimAllowed(tm.ID) {
		return
	}

	tmMgr := mgr.GetTaskManager(team.ID)
	if tmMgr == nil {
		return
	}

	pending := task.StatusPending
	inProgress := task.StatusInProgress
	eligible := make([]task.Task, 0, 8)
	for _, tk := range tmMgr.List() {
		if tk.Status != pending {
			continue
		}

		if assignee, ok := tk.Metadata["assignee"]; ok && assignee != "" && assignee != tm.ID {
			continue
		}

		if !allBlockersComplete(tmMgr, tk) {
			continue
		}
		eligible = append(eligible, tk)
	}
	if len(eligible) == 0 {
		return
	}
	now := time.Now()
	for _, tk := range eligible {
		markStarved(tmMgr, tk, now)
	}
	sortClaimable(eligible, now)

	for _, tk := range eligible {

		owner := tm.ID
		claimed, err := tmMgr.Update(tk.ID, task.UpdateOptions{
			ExpectedStatus: &pending,
			Status:         &inProgress,
			Owner:          &owner,
		})
		if err != nil {

			continue
		}

		tm.mu.Lock()
		tm.CurrentTaskID = claimed.ID
		tm.mu.Unlock()

		teammateConsumeProbe(tm.ID)
		if onEvent != nil {
			onEvent(Event{Type: "team_board_updated", TeamID: team.ID, Timestamp: time.Now()})
		}

		prompt := buildTaskPrompt(claimed) + depFindingsPrompt(tmMgr, claimed)

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

		msg := MailMessage{Content: prompt, Type: "task"}
		result, taskErr := executeTask(ctx, agent, msg, tm, onEvent, team, taskTimeout)

		tm.setLastResult(result)

		if ctx.Err() != nil {
			ctxCancelRollbackTask(tmMgr, claimed, tm)
			if onEvent != nil {
				onEvent(Event{Type: "team_board_updated", TeamID: team.ID, Timestamp: time.Now()})
			}
			tm.setStatus(TeammateShuttingDown)
			return
		}

		if taskErr != nil {

			fc := provider.ClassifyLLMError(taskErr)
			if fc == provider.FailureQuota || fc == provider.FailureAuth {

				completed := task.StatusCompleted
				errMsg := util.Truncate(taskErr.Error(), 200)
				meta := map[string]string{"permanent_error": fc.String(), "error": errMsg}
				meta = attachAttribution(meta, buildFailureAttribution(tm, claimed.ID, taskErr))
				if _, uerr := tmMgr.Update(claimed.ID, task.UpdateOptions{

					// #4672: CAS guard - an external terminal write (team review
					// approve, manual /task, RestoreJSON) during execution must not
					// be overwritten; mirrors rollbackClaimedTask (#2579).

					ExpectedStatus: &inProgress,
					Status:         &completed,
					Metadata:       meta,
				}); uerr != nil {
					debug.Log("swarm", "teammate %s permanent-failure park of %s failed: %v", tm.ID, claimed.ID, uerr)
				}
				debug.Log("swarm", "teammate %s task %s permanently failed (%s): %v",
					tm.ID, claimed.ID, fc, taskErr)
			} else {

				attempts := 0
				if v, ok := claimed.Metadata["retry_attempts"]; ok {
					if n, err := strconv.Atoi(v); err == nil {
						attempts = n
					}
				}
				attempts++
				if attempts >= maxTransientTaskRetries {
					completed := task.StatusCompleted
					errMsg := util.Truncate(taskErr.Error(), 200)
					meta := map[string]string{
						"permanent_error": "max_retries_exceeded",
						"error":           errMsg,
						"retry_attempts":  strconv.Itoa(attempts),
					}
					meta = attachAttribution(meta, buildFailureAttribution(tm, claimed.ID, taskErr))
					if _, uerr := tmMgr.Update(claimed.ID, task.UpdateOptions{
						ExpectedStatus: &inProgress,
						Status:         &completed,
						Metadata:       meta,
					}); uerr != nil {
						debug.Log("swarm", "teammate %s max-retries park of %s failed: %v", tm.ID, claimed.ID, uerr)
					}
					debug.Log("swarm", "teammate %s task %s exceeded %d transient retries, parking as completed(max_retries): %v",
						tm.ID, claimed.ID, maxTransientTaskRetries, taskErr)
				} else {
					pending := task.StatusPending
					owner := ""
					if _, uerr := tmMgr.Update(claimed.ID, task.UpdateOptions{
						ExpectedStatus: &inProgress,
						Status:         &pending,
						Owner:          &owner,
						Metadata:       map[string]string{"retry_attempts": strconv.Itoa(attempts)},
					}); uerr != nil {
						debug.Log("swarm", "teammate %s transient rollback of %s failed: %v", tm.ID, claimed.ID, uerr)
					}
				}
			}
		} else {
			completed := task.StatusCompleted
			if _, uerr := tmMgr.Update(claimed.ID, task.UpdateOptions{
				ExpectedStatus: &inProgress,
				Status:         &completed,
			}); uerr != nil {
				debug.Log("swarm", "teammate %s completion of %s failed: %v", tm.ID, claimed.ID, uerr)
			}

			persistTaskResult(tmMgr, claimed.ID, result)
		}
		if onEvent != nil {
			onEvent(Event{Type: "team_board_updated", TeamID: team.ID, Timestamp: time.Now()})
		}
		tm.setStatus(TeammateIdle)
		tm.setCurrentTask("")

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
				Error:        taskErr,
				Timestamp:    time.Now(),
			})
		}

		return
	}
}

const maxTransientTaskRetries = 3

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
		completed := task.StatusCompleted
		meta := map[string]string{
			"permanent_error": "max_retries_exceeded",
			"error":           "teammate panicked repeatedly",
			"retry_attempts":  strconv.Itoa(attempts),
		}
		meta = attachAttribution(meta, buildFailureAttribution(tm, taskID, fmt.Errorf("teammate panicked repeatedly")))
		if _, uerr := board.Update(taskID, task.UpdateOptions{
			ExpectedStatus: &inProgress,
			Status:         &completed,
			Metadata:       meta,
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

func ctxCancelRollbackTask(tmMgr *task.Manager, claimed task.Task, tm *Teammate) {
	inProgress := task.StatusInProgress
	pending := task.StatusPending
	owner := ""
	attempts := 0
	if v, ok := claimed.Metadata["retry_attempts"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			attempts = n
		}
	}
	attempts++
	if _, uerr := tmMgr.Update(claimed.ID, task.UpdateOptions{
		ExpectedStatus: &inProgress,
		Status:         &pending,
		Owner:          &owner,
		Metadata:       map[string]string{"retry_attempts": strconv.Itoa(attempts)},
	}); uerr != nil {
		debug.Log("swarm", "teammate %s ctx-cancel rollback of %s skipped: %v (task changed externally)", tm.ID, claimed.ID, uerr)
	}
}

func executeTask(
	ctx context.Context,
	agent AgentRunner,
	msg MailMessage,
	tm *Teammate,
	onEvent func(Event),
	team *Team,
	timeout time.Duration,
) (string, error) {

	var subCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		subCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		subCtx, cancel = context.WithCancel(ctx)
	}

	subCtx = util.WithActor(subCtx, tm.ID)
	defer cancel()

	prompt := msg.Content
	if msg.Summary != "" {
		prompt = fmt.Sprintf("Summary: %s\n%s", msg.Summary, msg.Content)
	}

	var output strings.Builder
	var textBuf strings.Builder
	lastToolName := ""

	var streamErr error
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
			if streamErr == nil {
				streamErr = event.Error
			}
			tm.appendEvent(TeammateEvent{
				Type:    TeammateEventError,
				Text:    fmt.Sprintf("%v", event.Error),
				IsError: true,
			})
		}
	})
	flushText()

	if err == nil && streamErr != nil {
		err = streamErr
	}

	if err != nil {
		debug.Log("swarm", "teammate %s RunStream error: %v output_len=%d", tm.ID, err, output.Len())
		if subCtx.Err() == context.DeadlineExceeded {
			output.WriteString("\n[timeout: task exceeded time limit]")
		} else if subCtx.Err() == context.Canceled {
			output.WriteString("\n[cancelled]")
		} else if streamErr == nil {

			output.WriteString(fmt.Sprintf("\n[error: %v]", err))
		}
	}

	debug.Log("swarm", "teammate %s task complete output_len=%d", tm.ID, output.Len())

	recordTeammateTaskResult(tm.ID, err != nil && !errors.Is(subCtx.Err(), context.Canceled))
	return output.String(), err
}

func allBlockersComplete(tmMgr *task.Manager, tk task.Task) bool {
	if len(tk.BlockedBy) == 0 {
		return true
	}
	for _, blockerID := range tk.BlockedBy {
		blocker, ok := tmMgr.Get(blockerID)
		if !ok {

			return false
		}
		if blocker.Status != task.StatusCompleted {
			return false
		}

		if _, parked := blocker.Metadata["permanent_error"]; parked {
			return false
		}
	}
	return true
}
