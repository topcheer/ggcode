package swarm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// runTeammateLoop is the core idle loop for a teammate.
// It blocks on the inbox channel (no polling), processing messages one at a time.
// When a "task" message arrives, it uses the agent to execute it.
// On "shutdown" or context cancellation, it exits.
func runTeammateLoop(
	ctx context.Context,
	tm *Teammate,
	team *Team,
	agent AgentRunner,
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
					// Don't return — keep the loop alive in case agent is set later
					continue
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
		}
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
) string {
	// Create sub-context with configured timeout (default 30 min via Manager)
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := msg.Content
	if msg.Summary != "" {
		prompt = fmt.Sprintf("Summary: %s\n\n%s", msg.Summary, msg.Content)
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
