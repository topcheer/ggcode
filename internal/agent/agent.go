package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"

	"github.com/topcheer/ggcode/internal/checkpoint"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

// Agent orchestrates the agentic loop: send messages to LLM, execute tool calls, loop.
// DiffConfirmFunc is called before a file write to request user confirmation.
// It receives the file path and unified diff string, and returns true if approved.
type DiffConfirmFunc func(filePath, diffText string) bool

type interruptionHandler func() string

var errStreamInterruptedForReplan = errors.New("stream interrupted for replan")

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
	onInterrupt    interruptionHandler
	projectMemory  map[string]struct{}
	supportsVision bool
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
// If nil, Ask decisions are treated as Deny.
func (a *Agent) SetApprovalHandler(fn func(toolName string, input string) permission.Decision) {
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

	transientCompactWarned := false
	if err := a.maybeAutoCompact(ctx, onEvent, &transientCompactWarned); err != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
		return err
	}

	toolDefs := a.tools.ToDefinitions()
	reactiveCompactRetries := 0
	idleAutopilotContinuations := 0

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
		for _, tc := range toolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}
			if a.maybeInjectProjectMemoryForTool(tc, toolResults) {
				toolResults = nil
				break
			}
			debug.Log("agent", "executeToolWithPermission: tool=%s", tc.Name)
			result := a.executeToolWithPermission(ctx, tc)
			debug.Log("agent", "tool result: tool=%s is_error=%v output=%s images=%d", tc.Name, result.IsError, truncateStr(result.Content, 200), len(result.Images))
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
			if a.injectToolResultsAndInterruptions(toolResults) {
				toolResults = nil
				break
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
	}

	if a.maxIter > 0 {
		err := fmt.Errorf("max iterations (%d) reached", a.maxIter)
		debug.Log("agent", "RunStreamWithContent END: %v", err)
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
		return err
	}
	return nil
}

func (a *Agent) maybeInjectProjectMemoryForTool(tc provider.ToolCallDelta, pendingToolResults []provider.ContentBlock) bool {
	content, files, target := a.pendingProjectMemoryForTool(tc)
	if len(files) == 0 || strings.TrimSpace(content) == "" {
		return false
	}
	if len(pendingToolResults) > 0 {
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: pendingToolResults,
		})
	}
	a.contextManager.Add(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + content}},
	})
	targetLabel := target
	if targetLabel == "" {
		targetLabel = "the pending path"
	}
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("Additional project memory now applies to %s. Review that guidance first, then continue the task with the updated constraints.", targetLabel),
		}},
	})
	a.SetProjectMemoryFiles(files)
	debug.Log("agent", "injected path-scoped project memory for %s (%d files)", targetLabel, len(files))
	return true
}

func (a *Agent) pendingProjectMemoryForTool(tc provider.ToolCallDelta) (content string, files []string, target string) {
	targets := projectMemoryTargetsForTool(tc.Name, tc.Arguments)
	if len(targets) == 0 {
		return "", nil, ""
	}

	a.mu.RLock()
	workingDir := a.workingDir
	loaded := make(map[string]struct{}, len(a.projectMemory))
	for file := range a.projectMemory {
		loaded[file] = struct{}{}
	}
	a.mu.RUnlock()

	for _, candidate := range targets {
		resolved := normalizeProjectMemoryPath(candidate, workingDir)
		if resolved == "" {
			continue
		}
		projectFiles, err := memory.ProjectMemoryFilesForPath(resolved)
		if err != nil || len(projectFiles) == 0 {
			continue
		}
		var unseen []string
		for _, file := range projectFiles {
			normalized := normalizeProjectMemoryPath(file, workingDir)
			if normalized == "" {
				continue
			}
			if _, ok := loaded[normalized]; ok {
				continue
			}
			unseen = append(unseen, normalized)
		}
		if len(unseen) == 0 {
			continue
		}
		content, files, err := memory.ReadProjectMemoryFiles(unseen)
		if err == nil && strings.TrimSpace(content) != "" {
			return content, files, resolved
		}
	}

	return "", nil, ""
}

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

func (a *Agent) injectToolResultsAndInterruptions(toolResults []provider.ContentBlock) bool {
	if len(toolResults) == 0 {
		return a.injectPendingInterruptions()
	}
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
	debug.Log("agent", "interrupt received after tool result; replanning before remaining tool calls")
	a.contextManager.Add(provider.Message{
		Role:    "user",
		Content: toolResults,
	})
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("New user guidance arrived while you were working. Treat it as higher-priority context, adjust your plan immediately if needed, and then continue.\n\n%s", text),
		}},
	})
	return true
}

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
			if a.injectPendingInterruptions() {
				cancel()
				return nil, assistantTextBuf.String(), nil, errStreamInterruptedForReplan
			}
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

func (a *Agent) shouldAutopilotAskUser(text string) bool {
	if a.currentMode() != permission.AutopilotMode {
		return false
	}
	if !looksLikeExternalBlocker(text) {
		return false
	}
	toolAny, ok := a.tools.Get("ask_user")
	if !ok {
		return false
	}
	if askTool, ok := toolAny.(interface{ HasHandler() bool }); ok {
		return askTool.HasHandler()
	}
	return false
}

func autopilotContinueInstruction(lastAssistantText string) string {
	return "Autopilot is enabled. Do not wait for user confirmation when a safe, reasonable next step is available. Choose the most reasonable assumption, state it briefly if helpful, and continue working until there is nothing meaningful left to do. If you only made partial progress, keep going instead of stopping for a progress update. If progress is blocked on a user action or external step that you cannot do yourself, use `ask_user` instead of repeating a blocked or waiting status.\n\nPrevious assistant message:\n" + lastAssistantText
}

func autopilotAskUserInstruction(lastAssistantText string) string {
	return "Autopilot is enabled. The previous assistant message indicates progress is blocked on a user action or external step. If you can perform that step yourself with the available tools, do it now. Otherwise, call the `ask_user` tool immediately with the specific action or information needed. Do not repeat a blocked or waiting summary.\n\nPrevious assistant message:\n" + lastAssistantText
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
	if looksLikeProgressUpdate(trimmed) {
		return true
	}
	return false
}

const maxReactiveCompactRetries = 3
const autopilotLoopGuardThreshold = 2

func isPromptTooLongError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	keywords := []string{
		"prompt too long",
		"context length",
		"context window",
		"maximum context",
		"too many tokens",
		"input is too long",
		"exceeds the model's context",
		"maximum input tokens",
	}
	for _, keyword := range keywords {
		if strings.Contains(s, keyword) {
			return true
		}
	}
	return false
}

func (a *Agent) tryReactiveCompact(ctx context.Context, onEvent func(provider.StreamEvent), err error, retries *int) bool {
	if !isPromptTooLongError(err) {
		debug.Log("agent", "tryReactiveCompact: not a PTL error: %v", err)
		return false
	}
	if retries != nil && *retries >= maxReactiveCompactRetries {
		debug.Log("agent", "tryReactiveCompact: max retries (%d) reached", *retries)
		return false
	}
	debug.Log("agent", "tryReactiveCompact: PTL detected, tokens=%d attempting compact", a.contextManager.TokenCount())

	onEvent(provider.StreamEvent{
		Type: provider.StreamEventText,
		Text: "[compacting conversation to stay within context window]\n",
	})

	changed, compactErr := a.contextManager.CheckAndSummarize(ctx, a.provider)
	if compactErr != nil {
		return false
	}

	if cm, ok := a.contextManager.(interface{ TruncateOldestGroupForRetry() bool }); ok {
		if cm.TruncateOldestGroupForRetry() {
			changed = true
		}
	}

	if !changed {
		return false
	}
	onEvent(provider.StreamEvent{
		Type: provider.StreamEventText,
		Text: "[conversation compacted]\n",
	})
	if retries != nil {
		*retries = *retries + 1
		debug.Log("agent", "reactive compact retry=%d", *retries)
	}
	return true
}

func (a *Agent) maybeAutoCompact(ctx context.Context, onEvent func(provider.StreamEvent), transientWarned *bool) error {
	threshold := a.contextManager.AutoCompactThreshold()
	tokens := a.contextManager.TokenCount()
	ratio := a.contextManager.UsageRatio()
	debug.Log("agent", "maybeAutoCompact: tokens=%d threshold=%d ratio=%.3f maxTokens=%d",
		tokens, threshold, ratio, a.contextManager.MaxTokens())
	if threshold <= 0 || tokens < threshold {
		return nil
	}

	debug.Log("agent", "maybeAutoCompact: TRIGGERED (tokens=%d >= threshold=%d)", tokens, threshold)
	changed, err := a.contextManager.CheckAndSummarize(ctx, a.provider)
	if err != nil {
		if shouldIgnoreAutoCompactError(err) {
			debug.Log("agent", "ignoring transient auto-compact failure: %v", err)
			if transientWarned == nil || !*transientWarned {
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventText,
					Text: "[conversation compaction skipped due to transient provider error: " + compactErrorReason(err) + "]\n",
				})
				if transientWarned != nil {
					*transientWarned = true
				}
			}
			return nil
		}
		return fmt.Errorf("auto-summarize failed: %w", err)
	}
	if transientWarned != nil {
		*transientWarned = false
	}
	if !changed {
		return nil
	}

	onEvent(provider.StreamEvent{
		Type: provider.StreamEventText,
		Text: "[compacting conversation to stay within context window]\n",
	})
	onEvent(provider.StreamEvent{
		Type: provider.StreamEventText,
		Text: "[conversation compacted]\n",
	})
	return nil
}

func shouldIgnoreAutoCompactError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isPromptTooLongError(err) {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, keyword := range []string{
		"unexpected eof",
		"connection reset by peer",
		"broken pipe",
		"timeout awaiting response headers",
		"tls handshake timeout",
		"server closed idle connection",
		"temporary failure in name resolution",
	} {
		if strings.Contains(s, keyword) {
			return true
		}
	}
	return false
}

func compactErrorReason(err error) string {
	if err == nil {
		return "unknown error"
	}
	text := strings.TrimSpace(err.Error())
	text = strings.TrimPrefix(text, "summarization call failed: ")
	text = strings.TrimPrefix(text, "auto-summarize failed: ")
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 120 {
		text = text[:117] + "..."
	}
	return text
}

func shouldTriggerAutopilotLoopGuard(text string, streak int) bool {
	if streak < autopilotLoopGuardThreshold {
		return false
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	return looksLikeCompletionOrHandoff(trimmed) || looksLikeUserDecisionPrompt(trimmed) || looksLikeExternalBlocker(trimmed)
}

func (a *Agent) forceCompactAndPause(ctx context.Context, onEvent func(provider.StreamEvent)) error {
	debug.Log("agent", "autopilot loop guard triggered; compacting and pausing")
	onEvent(provider.StreamEvent{
		Type: provider.StreamEventText,
		Text: "[autopilot loop guard triggered; compacting and pausing]\n",
	})

	compacted, err := a.contextManager.CheckAndSummarize(ctx, a.provider)
	if err != nil {
		return err
	}
	if !compacted {
		if err := a.contextManager.Summarize(ctx, a.provider); err != nil {
			return err
		}
		compacted = true
	}
	if compacted {
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: "[conversation compacted]\n",
		})
	}
	onEvent(provider.StreamEvent{
		Type: provider.StreamEventText,
		Text: "[autopilot paused to prevent an idle loop]\n",
	})
	return nil
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
		"nothing more to do", "no remaining work", "all changes are complete", "all changes complete",
		"all changes are in place", "done. no remaining work", "done. awaiting",
		"waiting for your next request", "ready for next task", "ready for the next task",
		"awaiting instructions", "no tasks pending", "no work to do", "standing by",
		"idle — no tasks pending", "idle - no tasks pending", "idle — no pending tasks",
		"idle - no pending tasks", "waiting for your next instruction",
		"let me know if you'd like", "if you'd like, i can", "if you want, i can",
		"feel free to ask", "feel free to tell me", "happy to help with anything else",
		"全部完成", "已经全部完成", "任务已完成", "这个任务已经完成", "优化已完成", "实现已完成",
		"所有任务已完成", "所有工作已完成", "工作已完成",
		"没有更多可做", "没有进一步需要处理", "如需我继续", "如果你希望我继续", "我还可以继续",
		"随时告诉我", "如果你还有其他", "如果你有其他", "还有其他任务需要我", "其他方面的具体任务需要我帮忙",
		"等待你的下一条指令", "等待你的下一步指令", "等待下一条指令", "等待下一步指令",
		"等待新指令", "等待新的指令", "等待后续指令", "待命中", "没有待处理任务", "没有任务待处理", "没有工作可做",
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
	if strings.Contains(trimmed, "no remaining work") || strings.Contains(trimmed, "nothing more to do") {
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

func looksLikeProgressUpdate(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	markers := []string{
		"i inspected", "i checked", "i traced", "i investigated", "i analyzed", "i found",
		"i fixed", "i updated", "i changed", "i refactored", "i implemented", "i added",
		"identified", "root cause", "inspection shows",
		"我检查了", "我排查了", "我分析了", "我定位到", "我发现了", "我修复了", "我更新了", "我添加了",
	}
	for _, marker := range markers {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

func looksLikeExternalBlocker(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	markers := []string{
		"blocked until", "blocked on user", "waiting for user to", "need user to",
		"awaiting restart", "awaiting gateway restart", "awaiting test results",
		"restart needed to validate", "needs to be restarted", "cannot proceed without",
		"can't proceed without", "need diagnostic logs", "share logs to continue",
		"需要用户", "等待用户", "阻塞于", "卡在", "需要重启", "等待重启", "等待测试结果", "需要日志",
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

func projectMemoryTargetsForTool(toolName string, raw json.RawMessage) []string {
	if !toolCanTriggerProjectMemory(toolName) {
		return nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	var targets []string
	seen := make(map[string]struct{})
	collectProjectMemoryTargets(payload, "", seen, &targets)
	return targets
}

func collectProjectMemoryTargets(value any, key string, seen map[string]struct{}, out *[]string) {
	switch v := value.(type) {
	case map[string]any:
		for childKey, childValue := range v {
			collectProjectMemoryTargets(childValue, childKey, seen, out)
		}
	case []any:
		for _, item := range v {
			collectProjectMemoryTargets(item, key, seen, out)
		}
	case string:
		if !projectMemoryPathKey(key) {
			return
		}
		trimmed := strings.TrimSpace(v)
		if trimmed == "" || strings.Contains(trimmed, "://") {
			return
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		*out = append(*out, trimmed)
	}
}

func toolCanTriggerProjectMemory(toolName string) bool {
	switch toolName {
	case "read_file", "write_file", "edit_file", "list_directory", "glob", "search_files":
		return true
	default:
		return strings.HasPrefix(toolName, "lsp_")
	}
}

func projectMemoryPathKey(key string) bool {
	switch key {
	case "path", "file_path", "file", "filename", "directory":
		return true
	default:
		return false
	}
}

func normalizeProjectMemoryPath(target, workingDir string) string {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" || strings.Contains(trimmed, "://") {
		return ""
	}
	if !filepath.IsAbs(trimmed) {
		base := workingDir
		if strings.TrimSpace(base) == "" {
			base = "."
		}
		trimmed = filepath.Join(base, trimmed)
	}
	return filepath.Clean(trimmed)
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
