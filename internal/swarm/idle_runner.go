package swarm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
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
	}()

	tm.mu.Lock()
	tm.StartedAt = time.Now()
	tm.mu.Unlock()

	// Poll ticker: how often to check the task board for pending tasks.
	pollInterval := mgr.cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = 1 * time.Second
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
			case "task", "message", "":
				handleMessage(ctx, tm, team, agent, mgr, onEvent, taskTimeout, msg)
			}

		case <-poller.C:
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
		return
	}

	tm.setStatus(TeammateWorking)
	tm.setCurrentTask(truncate(msg.Content, 100))

	if onEvent != nil {
		onEvent(Event{
			Type:         "teammate_working",
			TeamID:       team.ID,
			TeammateID:   tm.ID,
			TeammateName: tm.Name,
			Timestamp:    time.Now(),
		})
	}

	result := executeTask(ctx, agent, msg, tm, onEvent, team, taskTimeout)

	// Send result back to caller if they requested it.
	if msg.ReplyTo != nil {
		msg.ReplyTo <- TaskResult{Output: result}
	}

	tm.setStatus(TeammateIdle)
	tm.setCurrentTask("")

	if onEvent != nil {
		onEvent(Event{
			Type:         "teammate_idle",
			TeamID:       team.ID,
			TeammateID:   tm.ID,
			TeammateName: tm.Name,
			Result:       truncate(result, 500),
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

		// Build prompt from the claimed task.
		prompt := buildTaskPrompt(claimed)

		tm.setStatus(TeammateWorking)
		tm.setCurrentTask(truncate(claimed.Subject, 100))

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
		result := executeTask(ctx, agent, msg, tm, onEvent, team, taskTimeout)

		// Mark task completed.
		completed := task.StatusCompleted
		tmMgr.Update(claimed.ID, task.UpdateOptions{Status: &completed})

		tm.setStatus(TeammateIdle)
		tm.setCurrentTask("")

		if onEvent != nil {
			onEvent(Event{
				Type:         "teammate_idle",
				TeamID:       team.ID,
				TeammateID:   tm.ID,
				TeammateName: tm.Name,
				Result:       truncate(result, 500),
				Timestamp:    time.Now(),
			})
		}

		// Claimed and completed one task — break to let the next poll pick up more.
		return
	}
}

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
	sb.WriteString("\nComplete this task now. Use swarm_task_complete when done.")
	return sb.String()
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
) string {
	// Create sub-context with configured timeout (default 30 min via Manager)
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := msg.Content
	if msg.Summary != "" {
		prompt = fmt.Sprintf("Summary: %s\n%s", msg.Summary, msg.Content)
	}

	var output strings.Builder
	err := agent.RunStream(subCtx, prompt, func(event provider.StreamEvent) {
		switch event.Type {
		case provider.StreamEventText:
			output.WriteString(event.Text)
		case provider.StreamEventToolCallDone:
			if onEvent != nil {
				onEvent(Event{
					Type:         "teammate_working",
					TeamID:       team.ID,
					TeammateID:   tm.ID,
					TeammateName: tm.Name,
					Timestamp:    time.Now(),
				})
			}
		case provider.StreamEventError:
			output.WriteString(fmt.Sprintf("\n[error: %v]", event.Error))
		}
	})

	if err != nil {
		if subCtx.Err() == context.DeadlineExceeded {
			output.WriteString("\n[timeout: task exceeded time limit]")
		} else if subCtx.Err() == context.Canceled {
			output.WriteString("\n[cancelled]")
		} else {
			output.WriteString(fmt.Sprintf("\n[error: %v]", err))
		}
	}

	return output.String()
}

func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}
