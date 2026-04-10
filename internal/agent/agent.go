package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"

	"github.com/topcheer/ggcode/internal/checkpoint"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

// Agent orchestrates the agentic loop: send messages to LLM, execute tool calls, loop.
// DiffConfirmFunc is called before a file write to request user confirmation.
// It receives the file path and unified diff string, and returns true if approved.
type DiffConfirmFunc func(filePath, diffText string) bool

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
	workingDir     string
	checkpoints    *checkpoint.Manager
	diffConfirm    DiffConfirmFunc
	mu             sync.RWMutex
}

type providerAwareContextManager interface {
	SetProvider(provider.Provider)
}

type todoPathAwareContextManager interface {
	SetTodoFilePath(path string)
}

type modeAwarePolicy interface {
	Mode() permission.PermissionMode
}

// NewAgent creates a new agent with optional permission policy.
func NewAgent(p provider.Provider, tools *tool.Registry, systemPrompt string, maxIter int) *Agent {
	a := &Agent{
		provider:       p,
		tools:          tools,
		maxIter:        maxIter,
		contextManager: ctxpkg.NewManager(128000),
	}
	a.syncContextManagerProviderLocked()
	a.syncContextManagerTodoPathLocked()
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
	a.mu.Lock()
	defer a.mu.Unlock()
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
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onApproval = fn
}

// PermissionPolicy returns the current policy.
func (a *Agent) PermissionPolicy() permission.PermissionPolicy {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.policy
}

// SetContextManager replaces the default context manager.
func (a *Agent) SetContextManager(cm ctxpkg.ContextManager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.contextManager = cm
	a.syncContextManagerProviderLocked()
	a.syncContextManagerTodoPathLocked()
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
	a.syncContextManagerProviderLocked()
}

func (a *Agent) Provider() provider.Provider {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.provider
}

func (a *Agent) ContextManager() ctxpkg.ContextManager {
	return a.contextManager
}

func (a *Agent) syncContextManagerProviderLocked() {
	if cm, ok := a.contextManager.(providerAwareContextManager); ok {
		cm.SetProvider(a.provider)
	}
}

// RunStream runs the agent loop with streaming, sending events to the callback.
func (a *Agent) RunStream(ctx context.Context, userMsg string, onEvent func(provider.StreamEvent)) error {
	return a.RunStreamWithContent(ctx, []provider.ContentBlock{{Type: "text", Text: userMsg}}, onEvent)
}

// RunStreamWithContent runs the agent loop and emits UI events for complete model turns.
func (a *Agent) RunStreamWithContent(ctx context.Context, content []provider.ContentBlock, onEvent func(provider.StreamEvent)) error {
	debug.Log("agent", "RunStreamWithContent START content_blocks=%d", len(content))

	a.contextManager.Add(provider.Message{
		Role:    "user",
		Content: content,
	})

	if threshold := ctxpkg.AutoCompactThresholdTokens(a.contextManager.MaxTokens()); threshold > 0 && a.contextManager.TokenCount() >= threshold {
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: "[compacting conversation to stay within context window]\n",
		})
	}

	if summarized, err := a.contextManager.CheckAndSummarize(ctx, a.provider); err != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: fmt.Errorf("auto-summarize failed: %w", err)})
	} else if summarized {
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: "[conversation compacted]\n",
		})
	}

	toolDefs := a.tools.ToDefinitions()

	for i := 0; a.maxIter <= 0 || i < a.maxIter; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		msgs := a.contextManager.Messages()
		debug.Log("agent", "Iteration %d/%d: contextManager messages=%d usage_ratio=%.2f", i+1, a.maxIter, len(msgs), a.contextManager.UsageRatio())

		resp, textBuf, toolCalls, err := a.streamChatResponse(ctx, msgs, toolDefs, onEvent)
		if err != nil {
			return err
		}

		a.emitUsage(resp.Usage)

		// No tool calls → done unless autopilot should continue with best-effort assumptions.
		if len(toolCalls) == 0 {
			if a.shouldAutopilotContinue(textBuf) {
				debug.Log("agent", "Iteration %d: autopilot continuing after assistant asked for input", i+1)
				a.contextManager.Add(resp.Message)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: autopilotContinueInstruction(textBuf),
					}},
				})
				continue
			}
			debug.Log("agent", "Iteration %d: no tool calls, returning", i+1)
			a.contextManager.Add(resp.Message)
			return nil
		}

		debug.Log("agent", "Iteration %d: tool_calls=%d", i+1, len(toolCalls))

		a.contextManager.Add(resp.Message)

		// Execute tool calls and build tool_result message
		var toolResults []provider.ContentBlock
		for _, tc := range toolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}
			debug.Log("agent", "executeToolWithPermission: tool=%s", tc.Name)
			result := a.executeToolWithPermission(ctx, tc)
			debug.Log("agent", "tool result: tool=%s is_error=%v output=%s", tc.Name, result.IsError, truncateStr(result.Content, 200))
			toolResults = append(toolResults, provider.ToolResultBlock(tc.ID, result.Content, result.IsError))

			onEvent(provider.StreamEvent{
				Type:    provider.StreamEventToolResult,
				Tool:    tc,
				Result:  result.Content,
				IsError: result.IsError,
			})

			if err := ctx.Err(); err != nil {
				return err
			}
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		debug.Log("agent", "Adding tool results to contextManager: blocks=%d", len(toolResults))
		a.contextManager.Add(provider.Message{
			Role:    "user", // Anthropic uses user role for tool results
			Content: toolResults,
		})
	}

	if a.maxIter > 0 {
		err := fmt.Errorf("max iterations (%d) reached", a.maxIter)
		debug.Log("agent", "RunStreamWithContent END: %v", err)
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
		return err
	}
	return nil
}

func (a *Agent) streamChatResponse(ctx context.Context, msgs []provider.Message, toolDefs []provider.ToolDefinition, onEvent func(provider.StreamEvent)) (*provider.ChatResponse, string, []provider.ToolCallDelta, error) {
	stream, err := a.provider.ChatStream(ctx, msgs, toolDefs)
	if err != nil {
		debug.Log("agent", "ChatStream error: %v", err)
		wrapped := fmt.Errorf("chat error: %w", err)
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: wrapped})
		return nil, "", nil, wrapped
	}

	var (
		textBuf          strings.Builder
		assistantTextBuf strings.Builder
		content          []provider.ContentBlock
		toolCalls        []provider.ToolCallDelta
		usage            provider.TokenUsage
	)

	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		content = append(content, provider.TextBlock(textBuf.String()))
		textBuf.Reset()
	}

	for event := range stream {
		switch event.Type {
		case provider.StreamEventText:
			onEvent(event)
			textBuf.WriteString(event.Text)
			assistantTextBuf.WriteString(event.Text)
		case provider.StreamEventToolCallChunk:
			onEvent(event)
		case provider.StreamEventToolCallDone:
			flushText()
			onEvent(event)
			toolCalls = append(toolCalls, event.Tool)
			content = append(content, provider.ToolUseBlock(event.Tool.ID, event.Tool.Name, event.Tool.Arguments))
		case provider.StreamEventDone:
			if event.Usage != nil {
				usage = *event.Usage
			}
			onEvent(event)
		case provider.StreamEventError:
			debug.Log("agent", "ChatStream event error: %v", event.Error)
			wrapped := fmt.Errorf("chat error: %w", event.Error)
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: wrapped})
			return nil, assistantTextBuf.String(), nil, wrapped
		}
	}

	flushText()

	return &provider.ChatResponse{
		Message: provider.Message{
			Role:    "assistant",
			Content: content,
		},
		Usage: usage,
	}, assistantTextBuf.String(), toolCalls, nil
}

func (a *Agent) emitUsage(usage provider.TokenUsage) {
	a.mu.Lock()
	fn := a.onUsage
	a.mu.Unlock()
	if fn != nil {
		fn(usage)
	}
}

func (a *Agent) currentMode() permission.PermissionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if policy, ok := a.policy.(modeAwarePolicy); ok {
		return policy.Mode()
	}
	return permission.SupervisedMode
}

func (a *Agent) shouldAutopilotContinue(text string) bool {
	if a.currentMode() != permission.AutopilotMode {
		return false
	}
	return shouldAutopilotKeepGoing(text)
}

func autopilotContinueInstruction(lastAssistantText string) string {
	return "Autopilot is enabled. Do not wait for user confirmation. Choose the most reasonable assumption, state it briefly if helpful, and continue working until there is nothing meaningful left to do. If you only made partial progress, keep going instead of stopping for a progress update.\n\nPrevious assistant message:\n" + lastAssistantText
}

func shouldAutopilotKeepGoing(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	if looksLikeCompletionOrHandoff(trimmed) {
		return false
	}
	if looksLikeUserDecisionPrompt(trimmed) {
		return true
	}
	if looksLikeMoreWorkRemaining(trimmed) {
		return true
	}
	return true
}

func looksLikeUserDecisionPrompt(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "?") || strings.Contains(trimmed, "？") {
		return true
	}
	markers := []string{
		"would you like", "should i", "which option", "which direction", "please provide",
		"please confirm", "can you confirm", "let me know", "tell me which", "what would you like",
		"what do you want", "how would you like", "do you want", "choose", "pick one",
		"请确认", "请提供", "请选择", "你希望", "是否", "要不要", "告诉我", "需要你", "先确认",
	}
	for _, marker := range markers {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

func looksLikeCompletionOrHandoff(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	markers := []string{
		"all set", "wrapped up", "nothing else", "nothing meaningful left",
		"no further action", "that's it", "here's what i changed", "summary of changes",
		"completed the requested", "finished the requested", "completed the task", "finished the task",
		"completed the implementation", "finished the implementation", "completed the optimization pass",
		"finished the optimization pass",
		"waiting for your next request", "ready for next task", "ready for the next task",
		"awaiting instructions", "no tasks pending", "no work to do", "standing by",
		"idle — no tasks pending", "idle - no tasks pending", "idle — no pending tasks",
		"idle - no pending tasks", "waiting for your next instruction",
		"let me know if you'd like", "if you'd like, i can", "if you want, i can",
		"feel free to ask", "feel free to tell me", "happy to help with anything else",
		"全部完成", "已经全部完成", "任务已完成", "这个任务已经完成", "优化已完成", "实现已完成",
		"没有更多可做", "没有进一步需要处理", "如需我继续", "如果你希望我继续", "我还可以继续",
		"随时告诉我", "如果你还有其他", "如果你有其他", "还有其他任务需要我", "其他方面的具体任务需要我帮忙",
		"等待你的下一条指令", "等待你的下一步指令", "等待下一条指令", "等待下一步指令",
		"待命中", "没有待处理任务", "没有任务待处理", "没有工作可做",
	}
	for _, marker := range markers {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

func looksLikeMoreWorkRemaining(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	markers := []string{
		"next step", "next i", "next i'll", "still need", "still needs", "need to", "needs more",
		"follow up", "follow-up", "continue with", "continue by", "identified", "more to do",
		"another step", "hotspot", "todo", "then i can", "then i'll", "remaining work",
		"接下来", "下一步", "还需要", "仍需", "还有", "后续", "继续", "再处理", "剩余",
	}
	for _, marker := range markers {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

// executeToolWithPermission checks permission before executing a tool.
func (a *Agent) executeToolWithPermission(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}
	debug.Log("agent", "permission check: tool=%s", tc.Name)
	a.mu.RLock()
	policy := a.policy
	onApproval := a.onApproval
	a.mu.RUnlock()
	if policy != nil {
		decision, err := policy.Check(tc.Name, tc.Arguments)
		debug.Log("agent", "permission decision: tool=%s decision=%s err=%v", tc.Name, decision, err)
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
			if onApproval != nil {
				resp := onApproval(tc.Name, string(tc.Arguments))
				if resp == permission.Deny {
					return tool.Result{
						Content: fmt.Sprintf("Permission denied for tool %q. User rejected the request.", tc.Name),
						IsError: true,
					}
				}
			} else {
				// No approval handler → deny by default
				return tool.Result{
					Content: fmt.Sprintf("Permission denied for tool %q. No approval handler available (running in non-interactive mode).", tc.Name),
					IsError: true,
				}
			}
		}
	}

	return a.executeTool(ctx, tc)
}

// SetCheckpointManager sets the checkpoint manager for undo support.
func (a *Agent) SetCheckpointManager(m *checkpoint.Manager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkpoints = m
}

// CheckpointManager returns the checkpoint manager.
func (a *Agent) CheckpointManager() *checkpoint.Manager {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.checkpoints
}

// SetDiffConfirm sets the diff confirmation callback.
func (a *Agent) SetDiffConfirm(fn DiffConfirmFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.diffConfirm = fn
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
	a.syncContextManagerTodoPathLocked()
}

func (a *Agent) syncContextManagerTodoPathLocked() {
	if cm, ok := a.contextManager.(todoPathAwareContextManager); ok {
		cm.SetTodoFilePath(tool.TodoFilePath(a.workingDir))
	}
}

func (a *Agent) executeTool(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}
	t, ok := a.tools.Get(tc.Name)
	if !ok {
		return tool.Result{Content: fmt.Sprintf("unknown tool: %s", tc.Name), IsError: true}
	}

	a.mu.RLock()
	hookCfg := a.hookConfig
	workDir := a.workingDir
	a.mu.RUnlock()
	env := hooks.HookEnv{
		ToolName:   tc.Name,
		WorkingDir: workDir,
		FilePath:   hooks.ExtractFilePath(tc.Name, string(tc.Arguments)),
		RawInput:   string(tc.Arguments),
	}

	// Pre-tool-use hooks
	preResult := hooks.RunPreHooks(hookCfg.PreToolUse, env)
	if !preResult.Allowed {
		return tool.Result{Content: preResult.Output, IsError: true}
	}

	// For file-editing tools: read old content, compute new, show diff, save checkpoint
	if tc.Name == "edit_file" || tc.Name == "write_file" {
		return a.executeFileTool(ctx, t, tc, env)
	}

	// Execute the actual tool
	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}
	result, err := t.Execute(ctx, tc.Arguments)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("tool error: %v", err), IsError: true}
	}

	// Post-tool-use hooks
	postResult := hooks.RunPostHooks(hookCfg.PostToolUse, env)
	if postResult.Output != "" {
		result.Content += "\n" + postResult.Output
	}

	return result
}

// executeFileTool handles edit_file and write_file with diff preview and checkpointing.
func (a *Agent) executeFileTool(ctx context.Context, t tool.Tool, tc provider.ToolCallDelta, env hooks.HookEnv) tool.Result {
	a.mu.Lock()
	cpMgr := a.checkpoints
	diffFn := a.diffConfirm
	a.mu.Unlock()

	// Determine file path and compute old/new content
	filePath, oldContent, newContent, err := a.computeFileChange(tc)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("file change error: %v", err), IsError: true}
	}

	// Show diff and ask for confirmation if diffConfirm is set
	if diffFn != nil && diff.HasChanges(oldContent, newContent) {
		diffText := diff.UnifiedDiff(oldContent, newContent, 3)
		if !diffFn(filePath, diffText) {
			return tool.Result{Content: fmt.Sprintf("File write to %s cancelled by user.", filePath), IsError: true}
		}
	}

	// Pre-tool-use hooks
	a.mu.RLock()
	hookCfg2 := a.hookConfig
	a.mu.RUnlock()
	preResult := hooks.RunPreHooks(hookCfg2.PreToolUse, env)
	if !preResult.Allowed {
		return tool.Result{Content: preResult.Output, IsError: true}
	}

	// Execute the actual tool
	result, err := t.Execute(ctx, tc.Arguments)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("tool error: %v", err), IsError: true}
	}

	// Save checkpoint
	if cpMgr != nil && !result.IsError {
		cpMgr.Save(filePath, oldContent, newContent, tc.Name)
	}

	// Post-tool-use hooks
	postResult := hooks.RunPostHooks(hookCfg2.PostToolUse, env)
	if postResult.Output != "" {
		result.Content += "\n" + postResult.Output
	}

	return result
}

// computeFileChange reads the old content and computes the new content for a file tool call.
func (a *Agent) computeFileChange(tc provider.ToolCallDelta) (filePath, oldContent, newContent string, err error) {
	switch tc.Name {
	case "edit_file":
		var args struct {
			FilePath string `json:"file_path"`
			OldText  string `json:"old_text"`
			NewText  string `json:"new_text"`
		}
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			return "", "", "", fmt.Errorf("invalid arguments: %w", err)
		}
		filePath = args.FilePath
		data, err := os.ReadFile(filePath)
		if err != nil {
			// File may not exist yet — that's OK for write_file, but edit_file needs it
			return "", "", "", fmt.Errorf("cannot read file: %w", err)
		}
		oldContent = string(data)
		newContent = replaceFirst(oldContent, args.OldText, args.NewText)
		return filePath, oldContent, newContent, nil

	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			return "", "", "", fmt.Errorf("invalid arguments: %w", err)
		}
		filePath = args.Path
		data, err := os.ReadFile(filePath)
		if err != nil {
			oldContent = ""
		} else {
			oldContent = string(data)
		}
		newContent = args.Content
		return filePath, oldContent, newContent, nil

	default:
		return "", "", "", fmt.Errorf("not a file tool: %s", tc.Name)
	}
}

// replaceFirst replaces the first occurrence of old in s with new.
func replaceFirst(s, old, new string) string {
	idx := indexOf(s, old)
	if idx < 0 {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
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

func truncateStr(s string, maxLen int) string {
	return util.Truncate(s, maxLen)
}
