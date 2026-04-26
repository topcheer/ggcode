package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"

	"github.com/topcheer/ggcode/internal/checkpoint"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

// DiffConfirmFunc is called before a file write to request user confirmation.
// It receives a context, the file path and unified diff string, and returns
// true if approved. Implementations MUST honor ctx.Done() so the agent
// goroutine doesn't leak when the TUI shuts down while a confirmation is in
// flight.
type DiffConfirmFunc func(ctx context.Context, filePath, diffText string) bool

// ApprovalFunc is called when a tool requires interactive approval. It MUST
// honor ctx.Done() to avoid a goroutine leak if the TUI exits while a
// permission prompt is awaiting user input.
type ApprovalFunc func(ctx context.Context, toolName string, input string) permission.Decision

type interruptionHandler func() string

var errStreamInterruptedForReplan = errors.New("stream interrupted for replan")

// Agent orchestrates the agentic loop: send messages to LLM, execute tool calls, loop.
type Agent struct {
	provider       provider.Provider
	tools          *tool.Registry
	contextManager ctxpkg.ContextManager
	maxIter        int
	policy         permission.PermissionPolicy
	onApproval     ApprovalFunc
	onUsage        func(usage provider.TokenUsage)
	onCheckpoint   func(messages []provider.Message, tokenCount int)
	hookConfig     hooks.HookConfig
	workingDir     string
	checkpoints    *checkpoint.Manager
	diffConfirm    DiffConfirmFunc
	onInterrupt    interruptionHandler
	projectMemory  map[string]struct{}
	supportsVision bool
	precompact     *precompactState
	mu             sync.RWMutex
}

type providerAwareContextManager interface {
	SetProvider(provider.Provider)
}

type usageAwareContextManager interface {
	RecordUsage(provider.TokenUsage)
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
		projectMemory:  make(map[string]struct{}),
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
// If nil, Ask decisions are treated as Deny. The callback receives the per-run
// context so it can abort cleanly if the agent is cancelled while waiting.
func (a *Agent) SetApprovalHandler(fn ApprovalFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onApproval = fn
}

// SetInterruptionHandler sets a callback that drains user guidance arriving mid-run.
func (a *Agent) SetInterruptionHandler(fn func() string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onInterrupt = fn
}

// PermissionPolicy returns the current policy.
func (a *Agent) PermissionPolicy() permission.PermissionPolicy {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.policy
}

// SetContextManager replaces the default context manager.
func (a *Agent) SetContextManager(cm ctxpkg.ContextManager) {
	// Cancel any in-flight pre-compact that targets the OLD context manager
	// before we swap. Otherwise the goroutine keeps mutating a manager that
	// is no longer attached to this agent.
	a.CancelPreCompact()
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

// SetProjectMemoryFiles seeds the set of already-loaded project memory files so
// path-triggered dynamic loading can avoid reinjecting startup guidance.
func (a *Agent) SetProjectMemoryFiles(files []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.projectMemory == nil {
		a.projectMemory = make(map[string]struct{}, len(files))
	}
	for _, file := range files {
		if normalized := normalizeProjectMemoryPath(file, a.workingDir); normalized != "" {
			a.projectMemory[normalized] = struct{}{}
		}
	}
}

func (a *Agent) ProjectMemoryFiles() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	files := make([]string, 0, len(a.projectMemory))
	for file := range a.projectMemory {
		files = append(files, file)
	}
	slices.Sort(files)
	return files
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

// ToolRegistry returns the tool registry used by this agent.
func (a *Agent) ToolRegistry() *tool.Registry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tools
}

// SystemPrompt returns the current system prompt (from the first system message).
func (a *Agent) SystemPrompt() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	msgs := a.contextManager.Messages()
	for _, m := range msgs {
		if m.Role == "system" {
			var parts []string
			for _, c := range m.Content {
				if c.Type == "text" {
					parts = append(parts, c.Text)
				}
			}
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

// SetSupportsVision controls whether tool_result images are included in
// messages sent to the provider. When false, image data is stripped from
// tool results and only the text placeholder is sent.
func (a *Agent) SetSupportsVision(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.supportsVision = v
}

func (a *Agent) SupportsVision() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.supportsVision
}

func (a *Agent) ContextManager() ctxpkg.ContextManager {
	return a.contextManager
}

// UpdateSystemPrompt replaces the first system message in the context.
// If no system message exists, it adds one.
func (a *Agent) UpdateSystemPrompt(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cm, ok := a.contextManager.(*ctxpkg.Manager)
	if !ok {
		return
	}
	cm.UpdateFirstSystemMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: text}},
	})
}

func (a *Agent) syncContextManagerProviderLocked() {
	if cm, ok := a.contextManager.(providerAwareContextManager); ok {
		cm.SetProvider(a.provider)
	}
}

func (a *Agent) syncContextManagerUsage(usage provider.TokenUsage) {
	if cm, ok := a.contextManager.(usageAwareContextManager); ok {
		debug.Log("agent", "syncUsage: input=%d output=%d", usage.InputTokens, usage.OutputTokens)
		cm.RecordUsage(usage)
	}
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

// SetCheckpointHandler sets a callback invoked after summarize compaction
// to persist the compacted message state.
func (a *Agent) SetCheckpointHandler(fn func(messages []provider.Message, tokenCount int)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onCheckpoint = fn
}

// SetWorkingDir sets the working directory for hooks.
func (a *Agent) SetWorkingDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workingDir = dir
	a.syncContextManagerTodoPathLocked()
}

func (a *Agent) WorkingDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workingDir
}

func (a *Agent) syncContextManagerTodoPathLocked() {
	if cm, ok := a.contextManager.(todoPathAwareContextManager); ok {
		cm.SetTodoFilePath(tool.TodoFilePath(a.workingDir))
	}
}

// Clear resets the conversation (keeps system prompt).
func (a *Agent) Clear() {
	a.CancelPreCompact()
	a.contextManager.Clear()
}

// --- Core agent loop ---

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

	// If a background pre-compact is in flight, drain it (or fail-fast on
	// cancellation). When it succeeded, maybeAutoCompact below will see
	// tokens<threshold and become a no-op, eliminating the inline 2-30s
	// summarize stall on the user's critical path.
	a.waitForPreCompact(ctx)

	transientCompactWarned := false
	if err := a.maybeAutoCompact(ctx, onEvent, &transientCompactWarned); err != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
		return err
	}

	toolDefs := a.tools.ToDefinitions()
	reactiveCompactRetries := 0
	idleAutopilotContinuations := 0
	consecutiveEmptyResponses := 0

	for i := 0; a.maxIter <= 0 || i < a.maxIter; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.injectPendingInterruptions() {
			continue
		}
		if err := a.maybeAutoCompact(ctx, onEvent, &transientCompactWarned); err != nil {
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
			return err
		}
		msgs := a.contextManager.Messages()
		debug.Log("agent", "Iteration %d/%d: contextManager messages=%d tokens=%d threshold=%d usage_ratio=%.3f maxTokens=%d",
			i+1, a.maxIter, len(msgs), a.contextManager.TokenCount(), a.contextManager.AutoCompactThreshold(), a.contextManager.UsageRatio(), a.contextManager.MaxTokens())

		resp, textBuf, toolCalls, err := a.streamChatResponse(ctx, msgs, toolDefs, onEvent)
		if err != nil {
			if errors.Is(err, errStreamInterruptedForReplan) {
				idleAutopilotContinuations = 0
				reactiveCompactRetries = 0
				continue
			}
			if a.tryReactiveCompact(ctx, onEvent, err, &reactiveCompactRetries) {
				idleAutopilotContinuations = 0
				continue
			}
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
			return err
		}
		reactiveCompactRetries = 0

		a.syncContextManagerUsage(resp.Usage)
		a.emitUsage(resp.Usage)

		// Detect empty LLM response: API accepted input but produced no output.
		// Only trigger when InputTokens > 0 (real API call) to avoid false positives
		// in tests or scenarios where usage stats are unavailable.
		if resp.Usage.OutputTokens == 0 && resp.Usage.InputTokens > 0 && len(toolCalls) == 0 {
			consecutiveEmptyResponses++
			debug.Log("agent", "Iteration %d: empty response detected (consecutive=%d, input_tokens=%d)",
				i+1, consecutiveEmptyResponses, resp.Usage.InputTokens)
			if consecutiveEmptyResponses >= 3 {
				debug.Log("agent", "too many consecutive empty responses (%d), aborting", consecutiveEmptyResponses)
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventText,
					Text: "[context overflow — conversation reset for recovery]\n",
				})
				return nil
			}
			// Retry: inject a nudge and continue
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: "The previous response was empty. Please try again.",
				}},
			})
			continue
		}
		consecutiveEmptyResponses = 0

		// No tool calls → done unless autopilot should continue with best-effort assumptions.
		if len(toolCalls) == 0 {
			a.contextManager.Add(resp.Message)
			if a.injectPendingInterruptions() {
				idleAutopilotContinuations = 0
				continue
			}
			if a.shouldAutopilotAskUser(textBuf) {
				idleAutopilotContinuations++
				if shouldTriggerAutopilotLoopGuard(textBuf, idleAutopilotContinuations) {
					if err := a.forceCompactAndPause(ctx, onEvent); err != nil {
						onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: fmt.Errorf("autopilot loop guard failed: %w", err)})
						return err
					}
					return nil
				}
				debug.Log("agent", "Iteration %d: autopilot escalating external blocker to ask_user", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: autopilotAskUserInstruction(textBuf),
					}},
				})
				continue
			}
			if a.shouldAutopilotContinue(textBuf) {
				idleAutopilotContinuations++
				if shouldTriggerAutopilotLoopGuard(textBuf, idleAutopilotContinuations) {
					if err := a.forceCompactAndPause(ctx, onEvent); err != nil {
						onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: fmt.Errorf("autopilot loop guard failed: %w", err)})
						return err
					}
					return nil
				}
				debug.Log("agent", "Iteration %d: autopilot continuing after assistant asked for input", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: autopilotContinueInstruction(textBuf),
					}},
				})
				continue
			}
			idleAutopilotContinuations = 0
			debug.Log("agent", "Iteration %d: no tool calls, returning", i+1)
			return nil
		}
		idleAutopilotContinuations = 0

		debug.Log("agent", "Iteration %d: tool_calls=%d", i+1, len(toolCalls))

		a.contextManager.Add(resp.Message)

		// Execute tool calls and build tool_result message
		var toolResults []provider.ContentBlock
		// Collect follow-up messages from tools (e.g., inline skills)
		var followUpMessages []provider.Message
		// Defer project memory injection until after all tools execute,
		// so every tool_call gets a matching tool_result.
		var deferredMemoryContent string
		var deferredMemoryFiles []string
		var deferredMemoryTarget string

		for _, tc := range toolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}
			// Check for project memory but defer injection
			if mc, mf, mt := a.pendingProjectMemoryForTool(tc); len(mf) > 0 && strings.TrimSpace(mc) != "" {
				if deferredMemoryContent == "" {
					deferredMemoryContent = mc
					deferredMemoryFiles = mf
					deferredMemoryTarget = mt
				}
			}
			debug.Log("agent", "executeToolWithPermission: tool=%s", tc.Name)
			result := a.executeToolWithPermission(ctx, tc)
			debug.Log("agent", "tool result: tool=%s is_error=%v output=%s images=%d", tc.Name, result.IsError, truncateStr(result.Content, 200), len(result.Images))

			// Collect follow-up messages from tools (e.g., inline skills).
			if len(result.FollowUpMessages) > 0 {
				followUpMessages = append(followUpMessages, result.FollowUpMessages...)
			}

			// If the tool suggests a working directory change, apply it.
			if result.SuggestedWorkingDir != "" && !result.IsError {
				a.mu.Lock()
				oldDir := a.workingDir
				a.workingDir = result.SuggestedWorkingDir
				a.mu.Unlock()
				debug.Log("agent", "working dir changed: %s -> %s (suggested by %s)", oldDir, result.SuggestedWorkingDir, tc.Name)
			}
			if len(result.Images) > 0 && a.SupportsVision() {
				imgs := make([]provider.ContentImage, len(result.Images))
				for i, ri := range result.Images {
					imgs[i] = provider.ContentImage{MIME: ri.MIME, Base64: ri.Base64}
				}
				toolResults = append(toolResults, provider.ToolResultWithImages(tc.ID, tc.Name, result.Content, imgs, result.IsError))
			} else {
				toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, result.Content, result.IsError))
			}

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
		if len(toolResults) == 0 {
			continue
		}
		debug.Log("agent", "Adding tool results to contextManager: blocks=%d", len(toolResults))
		a.contextManager.Add(provider.Message{
			Role:    "user", // Anthropic uses user role for tool results
			Content: toolResults,
		})

		// Inject follow-up messages from tools (e.g., inline skill instructions).
		for _, msg := range followUpMessages {
			debug.Log("agent", "Injecting follow-up message from tool: role=%s", msg.Role)
			a.contextManager.Add(msg)
		}

		// Inject deferred project memory after all tool results are submitted.
		if deferredMemoryContent != "" {
			targetLabel := deferredMemoryTarget
			if targetLabel == "" {
				targetLabel = "the pending path"
			}
			a.contextManager.Add(provider.Message{
				Role:    "system",
				Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + deferredMemoryContent}},
			})
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf("Additional project memory now applies to %s. Review that guidance first, then continue the task with the updated constraints.", targetLabel),
				}},
			})
			a.SetProjectMemoryFiles(deferredMemoryFiles)
			debug.Log("agent", "injected deferred path-scoped project memory for %s (%d files)", targetLabel, len(deferredMemoryFiles))
		}
	}

	if a.maxIter > 0 {
		err := fmt.Errorf("max iterations (%d) reached", a.maxIter)
		debug.Log("agent", "RunStreamWithContent END: %v", err)
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
		return err
	}
	return nil
}

// --- Interruption injection ---

// injectPendingInterruptions checks for mid-run user guidance and injects it
// as a high-priority user message. Returns true if an interruption was injected.
func (a *Agent) injectPendingInterruptions() bool {
	a.mu.RLock()
	fn := a.onInterrupt
	a.mu.RUnlock()
	if fn == nil {
		return false
	}
	text := strings.TrimSpace(fn())
	if text == "" {
		return false
	}
	debug.Log("agent", "injecting mid-run user guidance")
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("New user guidance arrived while you were working. Treat it as higher-priority context, adjust your plan immediately if needed, and then continue.\n\n%s", text),
		}},
	})
	return true
}

// --- Stream response parsing ---

// streamChatResponse opens a streaming chat, collects text/tool-call events,
// and returns the assembled response, the raw assistant text buffer, and any
// completed tool calls.
func (a *Agent) streamChatResponse(ctx context.Context, msgs []provider.Message, toolDefs []provider.ToolDefinition, onEvent func(provider.StreamEvent)) (*provider.ChatResponse, string, []provider.ToolCallDelta, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := a.provider.ChatStream(streamCtx, msgs, toolDefs)
	if err != nil {
		debug.Log("agent", "ChatStream error: %v", err)
		return nil, "", nil, fmt.Errorf("chat error: %w", err)
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
			return nil, assistantTextBuf.String(), nil, fmt.Errorf("chat error: %w", event.Error)
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

// --- Internal helpers ---

func (a *Agent) emitUsage(usage provider.TokenUsage) {
	a.mu.Lock()
	fn := a.onUsage
	a.mu.Unlock()
	if fn != nil {
		fn(usage)
	}
}

// isJSON checks if raw message is valid JSON (for tool calls).
func isJSON(data json.RawMessage) bool {
	var v interface{}
	return json.Unmarshal(data, &v) == nil
}

func truncateStr(s string, maxLen int) string {
	return util.Truncate(s, maxLen)
}
