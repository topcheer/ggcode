package subagent

import (
	"context"
	"fmt"
	runtimedebug "runtime/debug"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const subagentRedactedReasoningPlaceholder = "Reasoning hidden by model."

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

type usageHandlerSetter interface {
	SetUsageHandler(func(provider.TokenUsage))
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
	Model        string                                                       // optional model override (e.g., "sonnet", "opus", "haiku")
	AgentType    string                                                       // optional agent type hint (e.g., "Explore", "Plan")
	WorkingDir   string                                                       // working directory for the sub-agent
	OnStreamText func(agentID, text string)                                   // called on each text chunk for tunnel relay
	OnUsage      func(provider.TokenUsage)                                    // optional exact-usage callback for session accounting
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

	// Panic recovery: ensures Complete() is always called so Wait() never blocks forever.
	defer func() {
		if r := recover(); r != nil {
			debug.Log("subagent", "panic recovered subagent=%s error=%v stack=%s", cfg.SubAgentID, r, string(runtimedebug.Stack()))
			cfg.Manager.Complete(cfg.SubAgentID, "", fmt.Errorf("sub-agent panic: %v", r))
		}
	}()

	// Create sub-context with timeout
	timeout := cfg.Manager.Timeout()
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if !cfg.Manager.SetCancel(cfg.SubAgentID, cancel) {
		return
	}

	sa, _ := cfg.Manager.Get(cfg.SubAgentID)
	if sa != nil {
		sa.setStatus(StatusRunning)
		sa.setActivity("thinking", "", "")
		sa.setStartedAt(time.Now())
	}

	// Build tool subset for this sub-agent
	var toolSet interface{}
	if cfg.BuildToolSet != nil {
		toolSet = cfg.BuildToolSet(cfg.AllowedTools, cfg.AllTools)
	}

	// Create independent agent with its own context manager
	rolePrefix := "You are a sub-agent."
	if cfg.AgentType != "" {
		rolePrefix = fmt.Sprintf("You are a %s sub-agent.", cfg.AgentType)
	}
	systemPrompt := fmt.Sprintf(
		"%s Complete the following task independently:\n%s\n\nProvide a concise result. Do not spawn further agents. Do not use emoji with Variation Selector-16 (U+FE0F, e.g. ⚠️ ✨️ ⚙️) — use plain text instead to avoid terminal rendering issues.",
		rolePrefix,
		cfg.Task,
	)
	if cfg.WorkingDir != "" {
		systemPrompt += fmt.Sprintf("\n\nWorking directory: %s", cfg.WorkingDir)
	}
	if cfg.AgentFactory == nil {
		cfg.Manager.Complete(cfg.SubAgentID, "", fmt.Errorf("AgentFactory not configured"))
		return
	}
	subAgent := cfg.AgentFactory(cfg.Provider, toolSet, systemPrompt, 0)

	// Propagate working directory so sub-agent tools operate in the right directory
	if cfg.WorkingDir != "" {
		if wd, ok := subAgent.(interface{ SetWorkingDir(string) }); ok {
			wd.SetWorkingDir(cfg.WorkingDir)
		}
	}
	if cfg.OnUsage != nil {
		if usageAware, ok := subAgent.(usageHandlerSetter); ok {
			usageAware.SetUsageHandler(cfg.OnUsage)
		}
	}

	// Run the agentic loop, capturing output
	var output strings.Builder
	type pendingToolMeta struct {
		Name        string
		RawArgs     string
		DisplayName string
		Detail      string
	}
	pendingTools := make(map[string]pendingToolMeta)
	var unnamedTool pendingToolMeta
	var hasUnnamedTool bool
	var textBuf strings.Builder // accumulate text chunks into turn-level events
	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		text := textBuf.String()
		textBuf.Reset()
		if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
			sa.appendEvent(AgentEvent{Type: AgentEventText, Text: text})
		}
	}
	err := subAgent.RunStream(subCtx, cfg.Task, func(event provider.StreamEvent) {
		switch event.Type {
		case provider.StreamEventText:
			output.WriteString(event.Text)
			textBuf.WriteString(event.Text)
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.setActivity("writing", "", "")
			}
			cfg.Manager.NotifyStreamText(cfg.SubAgentID, event.Text)
			// Flush and notify every ~200 tokens to keep follow panel responsive
			// during long pure-text output (e.g. summary rounds).
			if textBuf.Len() >= 800 {
				flushText()
				cfg.Manager.Notify(cfg.SubAgentID)
			}
		case provider.StreamEventToolCallDone:
			// Flush accumulated text before recording tool call
			flushText()
			rawArgs := string(event.Tool.Arguments)
			meta := pendingToolMeta{
				Name:    event.Tool.Name,
				RawArgs: rawArgs,
			}
			// Increment tool call count for this subagent
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.IncrementToolCalls()
				sa.setActivity("tool", meta.Name, meta.RawArgs)
				sa.appendEvent(AgentEvent{
					Type:     AgentEventToolCall,
					ToolName: meta.Name,
					ToolID:   event.Tool.ID,
					ToolArgs: meta.RawArgs,
				})
			}
			cfg.Manager.Notify(cfg.SubAgentID)
			cfg.Manager.NotifyToolCall(cfg.SubAgentID, event.Tool.ID, meta.Name, "", meta.RawArgs, "")
			if event.Tool.ID != "" {
				pendingTools[event.Tool.ID] = meta
			} else {
				unnamedTool = meta
				hasUnnamedTool = true
			}
		case provider.StreamEventToolResult:
			flushText()
			meta, ok := pendingTools[event.Tool.ID]
			if ok {
				delete(pendingTools, event.Tool.ID)
			} else if event.Tool.ID == "" && hasUnnamedTool {
				meta = unnamedTool
				hasUnnamedTool = false
			}
			if summary := subagentToolProgressSummary(meta.Name, event.Result); summary != "" {
				cfg.Manager.UpdateProgress(cfg.SubAgentID, summary)
			} else {
				cfg.Manager.Notify(cfg.SubAgentID)
			}
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.appendEvent(AgentEvent{
					Type:     AgentEventToolResult,
					ToolName: meta.Name,
					ToolID:   event.Tool.ID,
					ToolArgs: meta.RawArgs,
					Result:   event.Result,
					IsError:  event.IsError,
				})
			}
			cfg.Manager.NotifyToolResult(cfg.SubAgentID, event.Tool.ID, meta.Name, "", "", event.Result, event.IsError)
		case provider.StreamEventError:
			flushText()
			output.WriteString(fmt.Sprintf("[error: %v]\n", event.Error))
			if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
				sa.setActivity("failed", "", "")
				sa.appendEvent(AgentEvent{
					Type:    AgentEventError,
					Text:    fmt.Sprintf("%v", event.Error),
					IsError: true,
				})
			}
			cfg.Manager.Notify(cfg.SubAgentID)
		case provider.StreamEventReasoning:
			text := strings.TrimSpace(event.Text)
			switch text {
			case "":
			case "__redacted_thinking__":
				text = subagentRedactedReasoningPlaceholder
			}
			if text != "" {
				if sa, ok := cfg.Manager.Get(cfg.SubAgentID); ok {
					sa.appendEvent(AgentEvent{
						Type: AgentEventReasoning,
						Text: text,
					})
				}
				cfg.Manager.NotifyReasoning(cfg.SubAgentID, text)
			}
		}
	})
	// Flush any remaining text at the end of the stream
	flushText()

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

func WaitForSnapshot(ctx context.Context, mgr *Manager, id string, wait time.Duration) (Snapshot, error) {
	sa, ok := mgr.Get(id)
	if !ok {
		return Snapshot{}, fmt.Errorf("sub-agent %s not found", id)
	}

	if wait <= 0 {
		return sa.snapshot(), nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		snap := sa.snapshot()
		switch snap.Status {
		case StatusCompleted, StatusFailed, StatusCancelled:
			return snap, nil
		}

		select {
		case <-ticker.C:
			continue
		case <-timer.C:
			return sa.snapshot(), nil
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
}

func subagentToolProgressSummary(toolName, result string) string {
	switch toolName {
	case "start_command", "read_command_output", "wait_command", "stop_command", "write_command_input", "list_commands":
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
