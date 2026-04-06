package subagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// ToolInfo is the minimal interface needed from a tool for sub-agent registration.
type ToolInfo interface {
	Name() string
}

// AgentFactory creates an agent with the given provider, tool registry, system prompt, and max turns.
// The tools parameter is an opaque value passed through from the caller.
type AgentFactory func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner

// AgentRunner is the minimal interface needed from an agent.
type AgentRunner interface {
	RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error
}

// RunnerConfig holds everything needed to run a sub-agent.
type RunnerConfig struct {
	Provider     provider.Provider
	AllTools     []ToolInfo
	Task         string
	AllowedTools []string
	Manager      *Manager
	SubAgentID   string
	AgentFactory AgentFactory
	BuildToolSet func(allowedTools []string, allTools []ToolInfo) interface{} // returns opaque tool set for agent
}

// Run starts the sub-agent in a goroutine, running a complete agentic loop.
// The sub-agent gets its own context manager, tool subset, and provider instance.
func Run(ctx context.Context, cfg RunnerConfig) {
	// Acquire concurrency slot
	if err := cfg.Manager.AcquireSemaphore(ctx); err != nil {
		cfg.Manager.Complete(cfg.SubAgentID, "", fmt.Errorf("failed to acquire slot: %w", err))
		return
	}
	defer cfg.Manager.ReleaseSemaphore()

	// Create sub-context with timeout
	timeout := cfg.Manager.Timeout()
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cfg.Manager.SetCancel(cfg.SubAgentID, cancel)

	sa, _ := cfg.Manager.Get(cfg.SubAgentID)
	if sa != nil {
		sa.setStatus(StatusRunning)
		sa.setActivity("thinking", "", "")
		sa.StartedAt = time.Now()
	}

	// Build tool subset for this sub-agent
	var toolSet interface{}
	if cfg.BuildToolSet != nil {
		toolSet = cfg.BuildToolSet(cfg.AllowedTools, cfg.AllTools)
	}

	// Create independent agent with its own context manager
	systemPrompt := fmt.Sprintf(
		"You are a sub-agent. Complete the following task independently:\n%s\n\nProvide a concise result. Do not spawn further agents.",
		cfg.Task,
	)
	if cfg.AgentFactory == nil {
		cfg.Manager.Complete(cfg.SubAgentID, "", fmt.Errorf("AgentFactory not configured"))
		return
	}
	subAgent := cfg.AgentFactory(cfg.Provider, toolSet, systemPrompt, 0)

	// Run the agentic loop, capturing output
	var output strings.Builder
	lastToolName := ""
	err := subAgent.RunStream(subCtx, cfg.Task, func(event provider.StreamEvent) {
		switch event.Type {
		case provider.StreamEventText:
			output.WriteString(event.Text)
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.setActivity("writing", "", "")
			}
			cfg.Manager.Notify(cfg.SubAgentID)
		case provider.StreamEventToolCallDone:
			// Increment tool call count for this subagent
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.IncrementToolCalls()
				sa.setActivity("tool", event.Tool.Name, string(event.Tool.Arguments))
			}
			lastToolName = event.Tool.Name
			cfg.Manager.Notify(cfg.SubAgentID)
		case provider.StreamEventToolResult:
			if summary := subagentToolProgressSummary(lastToolName, event.Result); summary != "" {
				cfg.Manager.UpdateProgress(cfg.SubAgentID, summary)
			} else {
				cfg.Manager.Notify(cfg.SubAgentID)
			}
		case provider.StreamEventError:
			output.WriteString(fmt.Sprintf("[error: %v]\n", event.Error))
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.setActivity("failed", "", "")
			}
			cfg.Manager.Notify(cfg.SubAgentID)
		}
	})

	result := output.String()
	if err != nil {
		if subCtx.Err() == context.DeadlineExceeded {
			cfg.Manager.Complete(cfg.SubAgentID, result, fmt.Errorf("sub-agent timed out after %v", timeout))
		} else if subCtx.Err() == context.Canceled {
			cfg.Manager.Complete(cfg.SubAgentID, result, fmt.Errorf("sub-agent cancelled"))
		} else {
			cfg.Manager.Complete(cfg.SubAgentID, result, err)
		}
	} else {
		cfg.Manager.Complete(cfg.SubAgentID, result, nil)
	}
}

// Wait blocks until the sub-agent with the given ID finishes, returning its result.
func Wait(ctx context.Context, mgr *Manager, id string) (string, error) {
	sa, ok := mgr.Get(id)
	if !ok {
		return "", fmt.Errorf("sub-agent %s not found", id)
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		sa.mu.Lock()
		status := sa.Status
		result := sa.Result
		err := sa.Error
		sa.mu.Unlock()

		switch status {
		case StatusCompleted:
			return result, nil
		case StatusFailed, StatusCancelled:
			return result, err
		}

		select {
		case <-ticker.C:
			continue
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func subagentToolProgressSummary(toolName, result string) string {
	switch toolName {
	case "start_command", "read_command_output", "wait_command", "stop_command", "list_commands":
		return compactProgressSummary(result)
	default:
		return ""
	}
}

func compactProgressSummary(result string) string {
	lines := strings.Split(result, "\n")
	parts := make([]string, 0, 3)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Job ID: "):
			parts = append(parts, line)
		case strings.HasPrefix(line, "Status: "):
			parts = append(parts, line)
		case strings.HasPrefix(line, "Total lines: "):
			parts = append(parts, line)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " • ")
}
