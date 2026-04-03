package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// Agent orchestrates the agentic loop: send messages to LLM, execute tool calls, loop.
type Agent struct {
	provider       provider.Provider
	tools          *tool.Registry
	contextManager ctxpkg.ContextManager
	maxIter        int
	policy         permission.PermissionPolicy
	onApproval     func(toolName string, input string) permission.Decision
	onUsage        func(usage provider.TokenUsage)
	hookConfig     hooks.HookConfig
	workingDir     string // called after each API call with usage stats
	mu             sync.Mutex
}

// NewAgent creates a new agent with optional permission policy.
func NewAgent(p provider.Provider, tools *tool.Registry, systemPrompt string, maxIter int) *Agent {
	a := &Agent{
		provider:       p,
		tools:          tools,
		maxIter:        maxIter,
		contextManager: ctxpkg.NewManager(128000),
	}
	if systemPrompt != "" {
		a.contextManager.Add(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: systemPrompt}},
		})
	}
	return a
}

// SetPermissionPolicy sets the permission policy for tool checks.
func (a *Agent) SetPermissionPolicy(policy permission.PermissionPolicy) {
	a.policy = policy
}

// SetUsageHandler sets a callback invoked after each API call with token usage.
func (a *Agent) SetUsageHandler(fn func(usage provider.TokenUsage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onUsage = fn
}

// SetApprovalHandler sets a callback for interactive approval (Ask → Deny by default).
// If nil, Ask decisions are treated as Deny.
func (a *Agent) SetApprovalHandler(fn func(toolName string, input string) permission.Decision) {
	a.onApproval = fn
}

// PermissionPolicy returns the current policy.
func (a *Agent) PermissionPolicy() permission.PermissionPolicy {
	return a.policy
}

// SetContextManager replaces the default context manager.
func (a *Agent) SetContextManager(cm ctxpkg.ContextManager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.contextManager = cm
}

// AddMessage appends a message to the conversation context.
func (a *Agent) AddMessage(msg provider.Message) {
	a.contextManager.Add(msg)
}

// Messages returns the current conversation messages.
func (a *Agent) Messages() []provider.Message {
	return a.contextManager.Messages()
}

// ContextManager returns the context manager for external inspection.
func (a *Agent) SetProvider(p provider.Provider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.provider = p
}

func (a *Agent) Provider() provider.Provider {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.provider
}

func (a *Agent) ContextManager() ctxpkg.ContextManager {
	return a.contextManager
}

// RunStream runs the agent loop with streaming, sending events to the callback.
func (a *Agent) RunStream(ctx context.Context, userMsg string, onEvent func(provider.StreamEvent)) error {
	a.contextManager.Add(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: userMsg}},
	})

	// Auto-summarize if usage ratio >= 80%.
	if a.contextManager.UsageRatio() >= 0.8 {
		if err := a.contextManager.Summarize(ctx, a.provider); err != nil {
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: fmt.Errorf("auto-summarize failed: %w", err)})
		} else {
			onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: "[context auto-summarized to fit within window]\n",
		})
		}
	}

	toolDefs := a.tools.ToDefinitions()

	for i := 0; i < a.maxIter; i++ {
		msgs := a.contextManager.Messages()

		streamCh, err := a.provider.ChatStream(ctx, msgs, toolDefs)
		if err != nil {
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: fmt.Errorf("stream error: %w", err)})
			return err
		}

		// Consume stream events
		var assistantContent []provider.ContentBlock
		var toolCalls []provider.ToolCallDelta

		for event := range streamCh {
			switch event.Type {
			case provider.StreamEventText:
				assistantContent = append(assistantContent, provider.ContentBlock{Type: "text", Text: event.Text})
				onEvent(event)
			case provider.StreamEventToolCallDone:
				toolCalls = append(toolCalls, event.Tool)
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventText,
					Text: fmt.Sprintf("\n[tool call: %s]\n", event.Tool.Name),
				})
			case provider.StreamEventError:
				onEvent(event)
				return event.Error
			case provider.StreamEventDone:
				onEvent(event)
				// Record token usage if handler is set
				if event.Usage != nil {
					a.mu.Lock()
					fn := a.onUsage
					a.mu.Unlock()
					if fn != nil {
						fn(*event.Usage)
					}
				}
			}
		}

		// No tool calls → done
		if len(toolCalls) == 0 {
			a.contextManager.Add(provider.Message{
				Role:    "assistant",
				Content: assistantContent,
			})
			return nil
		}

		// Add assistant message with tool_use blocks
		for _, tc := range toolCalls {
			assistantContent = append(assistantContent, provider.ToolUseBlock(tc.ID, tc.Name, tc.Arguments))
		}

		a.contextManager.Add(provider.Message{
			Role:    "assistant",
			Content: assistantContent,
		})

		// Execute tool calls and build tool_result message
		var toolResults []provider.ContentBlock
		for _, tc := range toolCalls {
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventText,
				Text: fmt.Sprintf("[executing: %s]\n", tc.Name),
			})

			result := a.executeToolWithPermission(ctx, tc)
			toolResults = append(toolResults, provider.ToolResultBlock(tc.ID, result.Content, result.IsError))

			onEvent(provider.StreamEvent{
				Type: provider.StreamEventText,
				Text: result.Content + "\n",
			})
		}

		a.contextManager.Add(provider.Message{
			Role:    "user", // Anthropic uses user role for tool results
			Content: toolResults,
		})
	}

	return fmt.Errorf("max iterations (%d) reached", a.maxIter)
}

// executeToolWithPermission checks permission before executing a tool.
func (a *Agent) executeToolWithPermission(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	if a.policy != nil {
		decision, err := a.policy.Check(tc.Name, tc.Arguments)
		if err != nil {
			return tool.Result{Content: fmt.Sprintf("permission check error: %v", err), IsError: true}
		}

		switch decision {
		case permission.Deny:
			return tool.Result{
				Content: fmt.Sprintf("Permission denied for tool %q. The operation was blocked by the permission policy.", tc.Name),
				IsError: true,
			}
		case permission.Ask:
			if a.onApproval != nil {
				resp := a.onApproval(tc.Name, string(tc.Arguments))
				if resp == permission.Deny {
					return tool.Result{
						Content: fmt.Sprintf("Permission denied for tool %q. User rejected the request.", tc.Name),
						IsError: true,
					}
				}
				// Allow or user approved
			} else {
				// No approval handler → deny by default
				return tool.Result{
					Content: fmt.Sprintf("Permission denied for tool %q. No approval handler available (running in non-interactive mode).", tc.Name),
					IsError: true,
				}
			}
		}
		// Allow: proceed
	}

	return a.executeTool(ctx, tc)
}

// SetHookConfig sets the hooks configuration.
func (a *Agent) SetHookConfig(cfg hooks.HookConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hookConfig = cfg
}

// SetWorkingDir sets the working directory for hooks.
func (a *Agent) SetWorkingDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workingDir = dir
}

func (a *Agent) executeTool(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	t, ok := a.tools.Get(tc.Name)
	if !ok {
		return tool.Result{Content: fmt.Sprintf("unknown tool: %s", tc.Name), IsError: true}
	}

	env := hooks.HookEnv{
		ToolName:   tc.Name,
		WorkingDir: a.workingDir,
		FilePath:   hooks.ExtractFilePath(tc.Name, string(tc.Arguments)),
		RawInput:   string(tc.Arguments),
	}

	// Pre-tool-use hooks
	preResult := hooks.RunPreHooks(a.hookConfig.PreToolUse, env)
	if !preResult.Allowed {
		return tool.Result{Content: preResult.Output, IsError: true}
	}

	// Execute the actual tool
	result, err := t.Execute(ctx, tc.Arguments)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("tool error: %v", err), IsError: true}
	}

	// Post-tool-use hooks
	postResult := hooks.RunPostHooks(a.hookConfig.PostToolUse, env)
	if postResult.Output != "" {
		result.Content += "\n" + postResult.Output
	}

	return result
}

// Clear resets the conversation (keeps system prompt).
func (a *Agent) Clear() {
	a.contextManager.Clear()
}

// isJSON checks if raw message is valid JSON (for tool calls).
func isJSON(data json.RawMessage) bool {
	var v interface{}
	return json.Unmarshal(data, &v) == nil
}
