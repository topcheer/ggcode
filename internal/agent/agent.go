package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"

	"github.com/topcheer/ggcode/internal/checkpoint"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/metrics"
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
type runResultHandler func([]provider.ContentBlock, error)

var errStreamInterruptedForReplan = errors.New("stream interrupted for replan")

// Agent orchestrates the agentic loop: send messages to LLM, execute tool calls, loop.
type Agent struct {
	provider                     provider.Provider
	tools                        *tool.Registry
	contextManager               ctxpkg.ContextManager
	maxIter                      int
	policy                       permission.PermissionPolicy
	onApproval                   ApprovalFunc
	onUsage                      func(usage provider.TokenUsage)
	onMetric                     func(metrics.MetricEvent)
	onCheckpoint                 func(messages []provider.Message, tokenCount int)
	onRunResult                  runResultHandler
	hookConfig                   hooks.HookConfig
	workingDir                   string
	sessionID                    string // current session ID; determines todo file path
	checkpoints                  *checkpoint.Manager
	diffConfirm                  DiffConfirmFunc
	onInterrupt                  interruptionHandler
	projectMemory                map[string]struct{}
	supportsVision               bool
	precompact                   *precompactState
	precompactCooldownUntil      time.Time // earliest next precompact; guarded by mu
	shutdownCtx                  context.Context
	shutdownCancel               context.CancelFunc // cancels on Close()
	probeKey                     string             // "vendor|baseURL|model" for context window auto-detection
	autopilotGoal                string             // current autopilot goal text; empty when no goal is active
	autopilotGoalAsked           bool               // true after the goal-collection instruction has been injected
	autopilotGoalSet             bool               // true after the user has confirmed a goal (goal text is non-empty)
	autopilotGoalCheckedThisTurn bool               // prevents infinite goal-check loops within a single idle turn
	reflectionFunc               ReflectionFunc     // called after each run with accumulated stats
	loopDetector                 loopDetector       // tracks consecutive identical tool calls to detect stuck loops
	systemPromptInjector         func() string      // returns extra system prompt text to inject (e.g. lanchat peer warnings)
	baseSystemPrompt             string             // the fully built static system prompt; used as reset base for dynamic injection
	mu                           sync.RWMutex
}

type providerAwareContextManager interface {
	SetProvider(provider.Provider)
}

type usageAwareContextManager interface {
	RecordUsage(provider.TokenUsage)
}

type usageEmitterContextManager interface {
	SetUsageHandler(func(provider.TokenUsage))
}

type todoPathAwareContextManager interface {
	SetTodoFilePath(path string)
}

type modeAwarePolicy interface {
	Mode() permission.PermissionMode
}

// NewAgent creates a new agent with optional permission policy.
func NewAgent(p provider.Provider, tools *tool.Registry, systemPrompt string, maxIter int) *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	a := &Agent{
		provider:         p,
		tools:            tools,
		maxIter:          maxIter,
		contextManager:   ctxpkg.NewManager(128000),
		projectMemory:    make(map[string]struct{}),
		baseSystemPrompt: systemPrompt,
		shutdownCtx:      ctx,
		shutdownCancel:   cancel,
	}
	a.syncContextManagerProviderLocked()
	a.syncContextManagerUsageHandlerLocked()
	a.syncContextManagerTodoPathLocked()
	if systemPrompt != "" {
		a.contextManager.Add(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: systemPrompt}},
		})
	}
	return a
}

// SetProbeKey sets the probe cache key ("vendor|baseURL|model") used for
// context window auto-detection from overflow errors.
func (a *Agent) SetProbeKey(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.probeKey = key
}

// SetPermissionPolicy sets the permission policy for tool checks.
// When switching to or from autopilot mode, the autopilot Goal state is
// reset accordingly.
func (a *Agent) SetPermissionPolicy(policy permission.PermissionPolicy) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Detect mode transitions involving autopilot.
	oldMode := permission.SupervisedMode
	if mp, ok := a.policy.(modeAwarePolicy); ok {
		oldMode = mp.Mode()
	}
	newMode := permission.SupervisedMode
	if mp, ok := policy.(modeAwarePolicy); ok {
		newMode = mp.Mode()
	}

	// Entering autopilot: reset goal collection state.
	if newMode == permission.AutopilotMode && oldMode != permission.AutopilotMode {
		a.autopilotGoal = ""
		a.autopilotGoalAsked = false
		a.autopilotGoalSet = false
	}
	// Leaving autopilot: clear everything.
	if oldMode == permission.AutopilotMode && newMode != permission.AutopilotMode {
		a.autopilotGoal = ""
		a.autopilotGoalAsked = false
		a.autopilotGoalSet = false
	}

	a.policy = policy
}

// SetUsageHandler sets a callback invoked after each API call with token usage.
func (a *Agent) SetUsageHandler(fn func(usage provider.TokenUsage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onUsage = fn
	a.syncContextManagerUsageHandlerLocked()
}

// SetMetricHandler sets a callback invoked after each LLM call or tool execution
// with performance metrics (TTFT, think time, tool duration, etc.).
// The callback must be non-blocking — it should send to a channel or drop if busy.
func (a *Agent) SetMetricHandler(fn func(metrics.MetricEvent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMetric = fn
}

// SetRunResultHandler sets a callback invoked after each RunStreamWithContent
// call completes. The callback receives the final error, if any.
func (a *Agent) SetRunResultHandler(fn func(error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if fn == nil {
		a.onRunResult = nil
		return
	}
	a.onRunResult = func(_ []provider.ContentBlock, err error) {
		fn(err)
	}
}

// SetRunResultWithContentHandler sets a callback invoked after each
// RunStreamWithContent call completes. The callback receives the original user
// content and the final error, if any.
func (a *Agent) SetRunResultWithContentHandler(fn func([]provider.ContentBlock, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onRunResult = fn
}

// SetSystemPromptInjector sets a callback that returns extra text to inject
// into the system prompt at the start of each RunStreamWithContent. This is
// used for dynamic warnings (e.g. lanchat peers editing the same workspace).
// If the callback returns empty string, no injection occurs.
func (a *Agent) SetSystemPromptInjector(fn func() string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.systemPromptInjector = fn
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

// Close releases resources held by the agent, including cancelling any
// in-flight pre-compact operations. Should be called on shutdown.
func (a *Agent) Close() {
	a.CancelPreCompact()
	if a.shutdownCancel != nil {
		a.shutdownCancel()
	}
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
	a.syncContextManagerUsageHandlerLocked()
	a.syncContextManagerTodoPathLocked()
}

// AddMessage appends a message to the conversation context.
func (a *Agent) AddMessage(msg provider.Message) {
	a.contextManager.Add(msg)
}

// ReconcileToolCalls checks the conversation history for unpaired tool_use
// blocks (tool_calls without matching tool_result blocks across ALL assistant
// messages) and adds cancelled tool_result entries to keep the conversation
// valid for LLM APIs.
// See context.Manager.ReconcileToolCalls() for details.
func (a *Agent) ReconcileToolCalls() bool {
	if a.contextManager == nil {
		return false
	}
	return a.contextManager.ReconcileToolCalls()
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

// AddedSinceRunStart returns messages added by the agent via Add() during the
// most recent RunStreamWithContent call. Used by session persistence to
// determine which messages need to be appended to the JSONL file.
func (a *Agent) AddedSinceRunStart() []provider.Message {
	if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
		return cm.AddedSinceRunStart()
	}
	return nil
}

// StartRunTracking clears the run-added message tracking. This is normally
// called inside RunStreamWithContent, but callers can invoke it earlier
// (e.g. before ExpandMentions) to ensure AddedSinceRunStart returns empty
// instead of stale data from a previous run if the agent never starts.
func (a *Agent) StartRunTracking() {
	if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
		cm.StartRunTracking()
	}
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

func (a *Agent) SetReasoningEffort(effort string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.provider.(provider.ReasoningEffortProvider)
	if !ok {
		return false
	}
	p.SetReasoningEffort(effort)
	return true
}

func (a *Agent) ReasoningEffort() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.provider.(provider.ReasoningEffortProvider)
	if !ok {
		return ""
	}
	return p.ReasoningEffort()
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
// Also updates baseSystemPrompt so dynamic injection resets to this base.
func (a *Agent) UpdateSystemPrompt(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.baseSystemPrompt = text
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

func (a *Agent) syncContextManagerUsageHandlerLocked() {
	if cm, ok := a.contextManager.(usageEmitterContextManager); ok {
		cm.SetUsageHandler(a.onUsage)
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

// GetHookConfig returns the current hook configuration (thread-safe).
func (a *Agent) GetHookConfig() hooks.HookConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hookConfig
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
}

func (a *Agent) WorkingDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workingDir
}

// SetSessionID sets the current session ID and propagates it to the todo tool
// and context manager so both read/write from the same session-scoped path.
func (a *Agent) SetSessionID(id string) {
	// Update sessionID + context manager path atomically under one lock
	// to avoid a TOCTOU window where sessionID is new but todoPath is stale.
	a.mu.Lock()
	a.sessionID = id
	a.syncContextManagerTodoPathLocked()
	a.mu.Unlock()
	// Update the TodoWrite tool's session binding outside agent.mu.
	// tools.Get acquires registry.mu and tw.SetSessionID acquires TodoWrite.mu;
	// holding agent.mu during those calls risks deadlock if a registry or
	// tool callback tries to call back into the agent.
	if t, ok := a.tools.Get("todo_write"); ok {
		if tw, ok := t.(*tool.TodoWrite); ok {
			tw.SetSessionID(id)
		}
	}
}

func (a *Agent) syncContextManagerTodoPathLocked() {
	if cm, ok := a.contextManager.(todoPathAwareContextManager); ok {
		cm.SetTodoFilePath(tool.TodoFilePath(a.sessionID))
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
func (a *Agent) RunStreamWithContent(ctx context.Context, content []provider.ContentBlock, onEvent func(provider.StreamEvent)) (err error) {
	debug.Log("agent", "RunStreamWithContent START content_blocks=%d", len(content))

	// Start tracking messages added during this run for session persistence.
	// persistFullSessionMessages() will use this to know which messages
	// were added by the agent and need to be appended to the JSONL file.
	if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
		cm.StartRunTracking()
	}

	// Extract user prompt text for stats tracking
	userPromptForStats := ""
	for _, b := range content {
		if b.Type == "text" {
			userPromptForStats += b.Text
		}
	}
	runStats := newRunStats(userPromptForStats)

	// Reset loop detector for each new user turn.
	a.resetLoopDetector()

	defer func() {
		runStats.finalize(err)
		a.maybeReflect(runStats)
		a.mu.RLock()
		fn := a.onRunResult
		a.mu.RUnlock()
		if fn != nil {
			fn(content, err)
		}
		// Clean up session todos on agent stop. This prevents permanent todo
		// residue when the LLM creates todos but forgets to clear them.
		// Covers normal completion, cancellation, and error cases.
		if t, ok := a.tools.Get("todo_write"); ok {
			if tw, ok := t.(*tool.TodoWrite); ok {
				tw.ClearTodos()
			}
		}
	}()

	a.contextManager.Add(provider.Message{
		Role:    "user",
		Content: content,
	})

	// on_user_message hook (synchronous, can block).
	userText := ""
	for _, b := range content {
		if b.Type == "text" {
			userText += b.Text
		}
	}
	a.mu.RLock()
	hookCfg := a.hookConfig
	workDir := a.workingDir
	a.mu.RUnlock()
	userMsgResult := hooks.RunUserMessageHooks(hookCfg.OnUserMessage, hooks.HookEnv{
		Event:       hooks.EventOnUserMessage,
		Workspace:   workDir,
		WorkingDir:  workDir,
		UserMessage: userText,
	})
	if !userMsgResult.Allowed {
		onEvent(provider.StreamEvent{
			Type:  provider.StreamEventError,
			Error: fmt.Errorf("%s", userMsgResult.Output),
		})
		return fmt.Errorf("user message blocked by hook: %s", userMsgResult.Output)
	}

	// on_agent_stop hook (async, fire-and-forget on return).
	defer func() {
		stopReason := "completed"
		stopError := ""
		if err != nil {
			if errors.Is(err, context.Canceled) {
				stopReason = "cancelled"
			} else {
				stopReason = "error"
				stopError = err.Error()
			}
		}
		hooks.RunAgentStopHooks(hookCfg, hooks.HookEnv{
			Event:      hooks.EventOnAgentStop,
			Workspace:  workDir,
			WorkingDir: workDir,
			StopReason: stopReason,
			StopError:  stopError,
		})
	}()

	// Reconcile tool_calls: if the last assistant message has unpaired tool_use
	// blocks (no matching tool_result blocks in subsequent messages), add a user
	// message with cancelled tool_result entries. This handles both session
	// restoration from file and runtime interruption where the agent loop was
	// cancelled before tool results could be added.
	if a.ReconcileToolCalls() {
		debug.Log("agent", "RunStreamWithContent: reconciled unpaired tool_calls")
	}

	// Autopilot Goal collection: on the first RunStream after entering
	// autopilot mode, inject a meta-instruction asking the LLM to propose
	// a goal and confirm it with the user via ask_user. This works across
	// all surfaces (TUI questionnaire, Desktop dialog, daemon IM/mobile).
	//
	// Also: if mode changed away from autopilot since last run, clear any
	// stale goal. This handles TUI's cp.SetMode() which mutates the policy
	// in-place without calling agent.SetPermissionPolicy().
	a.clearGoalIfNotAutopilot()
	a.maybeInjectAutopilotGoalCollection()
	a.maybeInjectDynamicSystemPrompt()

	transientCompactWarned := false
	toolDefs := a.tools.ToDefinitions()
	reactiveCompactRetries := 0
	idleAutopilotContinuations := 0
	consecutiveEmptyResponses := 0
	verifyRetries := 0

	for i := 0; a.maxIter <= 0 || i < a.maxIter; i++ {
		runStats.Iterations = i + 1
		if err := ctx.Err(); err != nil {
			return err
		}
		// Adopt a completed background pre-compact only at an LLM turn
		// boundary. If it is still running, do not wait; this ChatStream uses
		// the current context and a later LLM turn can consume the result.
		if a.consumeReadyPreCompact(onEvent) {
			runStats.recordCompaction()
		}
		if a.injectPendingInterruptions() {
			continue
		}
		if err := a.maybeAutoCompact(ctx, onEvent, &transientCompactWarned); err != nil {
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
			return err
		}
		a.ensurePromptSendable()
		msgs := a.contextManager.Messages()
		runStats.recordContextUsage(a.contextManager.TokenCount())
		if runStats.ContextWindow == 0 {
			runStats.ContextWindow = a.contextManager.ContextWindow()
		}
		debug.Log("agent", "Iteration %d/%d: contextManager messages=%d tokens=%d threshold=%d usage_ratio=%.3f maxTokens=%d",
			i+1, a.maxIter, len(msgs), a.contextManager.TokenCount(), a.contextManager.AutoCompactThreshold(), a.contextManager.UsageRatio(), a.contextManager.ContextWindow())

		resp, textBuf, toolCalls, err := a.streamChatResponse(ctx, msgs, toolDefs, onEvent)
		if err != nil {
			if errors.Is(err, errStreamInterruptedForReplan) {
				idleAutopilotContinuations = 0
				reactiveCompactRetries = 0
				continue
			}
			if a.tryReactiveCompact(ctx, onEvent, err, &reactiveCompactRetries) {
				runStats.recordCompaction()
				idleAutopilotContinuations = 0
				continue
			}
			// User cancellation: return the original error (which wraps
			// context.Canceled) so callers can detect it with errors.Is.
			// Converting to a friendly string would break the error chain.
			if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {
				onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ctx.Err()})
				return ctx.Err()
			}
			friendlyErr := fmt.Errorf("%s", provider.FriendlyError(err))
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: friendlyErr})
			return friendlyErr
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

			// Autopilot Goal achievement check: if the LLM declares the goal
			// complete, clear the goal and stop. This runs before other
			// autopilot heuristics so that GOAL_COMPLETE always wins.
			if a.isAutopilotGoalComplete(textBuf) {
				debug.Log("agent", "Iteration %d: autopilot goal declared complete", i+1)
				a.clearAutopilotGoal()
				return nil
			}

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
						Text: autopilotAskUserInstruction(textBuf, a.getAutopilotGoal()),
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
						Text: autopilotContinueInstruction(textBuf, a.getAutopilotGoal()),
					}},
				})
				continue
			}
			// If we are in autopilot mode and have a goal but the LLM stopped
			// without tool calls and without explicit completion language,
			// inject a goal check prompt to nudge it to either continue or
			// declare done.
			if a.currentMode() == permission.AutopilotMode && a.hasAutopilotGoal() && !a.autopilotGoalCheckedThisTurn {
				a.autopilotGoalCheckedThisTurn = true
				debug.Log("agent", "Iteration %d: autopilot injecting goal check", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: autopilotGoalCheckInstruction(a.getAutopilotGoal(), textBuf),
					}},
				})
				continue
			}
			idleAutopilotContinuations = 0
			// Auto-verify: before returning, run build/test verification.
			// If it fails, inject errors back and continue the loop.
			// Only in non-plan modes, max 3 retries.
			if verifyRetries < autoVerifyMaxRetries && runStats.Iterations > 1 {
				if a.maybeAutoVerify(ctx, onEvent, textBuf) {
					verifyRetries++
					debug.Log("agent", "Iteration %d: auto-verify failed, continuing (retry %d/%d)", i+1, verifyRetries, autoVerifyMaxRetries)
					continue
				}
			}
			debug.Log("agent", "Iteration %d: no tool calls, returning", i+1)
			return nil
		}
		idleAutopilotContinuations = 0
		// Reset the per-turn goal check flag when tool calls are present
		// (the LLM is actively working, no need for a goal check nudge).
		a.autopilotGoalCheckedThisTurn = false

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

		for idx, tc := range toolCalls {
			if err := ctx.Err(); err != nil {
				// Context cancelled mid-tool-execution. The assistant message
				// (with tool_use blocks) was already added to contextManager above.
				// Without matching tool_results, the next LLM API call will fail
				// because tool_use has no corresponding tool_result (protocol violation).
				// Fill in "cancelled" results for all tool_calls that have not run yet.
				a.fillCancelledToolResults(toolCalls[idx:], &toolResults)
				return err
			}
			// Track tool call for reflection stats
			runStats.recordToolCall(tc.Name)
			extractPathsFromToolCall(tc.Name, tc.Arguments, runStats)
			// Check for consecutive duplicate tool calls (loop detection).
			// If detected, inject a guidance message into the tool result.
			var loopGuidance string
			if guidance := a.loopDetectionInjection(tc); guidance != "" {
				loopGuidance = guidance
			}
			// Check for project memory but defer injection
			if mc, mf, mt := a.pendingProjectMemoryForTool(tc); len(mf) > 0 && strings.TrimSpace(mc) != "" {
				if deferredMemoryContent == "" {
					deferredMemoryContent = mc
					deferredMemoryFiles = mf
					deferredMemoryTarget = mt
				}
			}
			// Don't log executeToolWithPermission start — the permission check log already covers this
			result := a.executeToolWithPermission(ctx, tc)
			// Inject matching harness rules into the result
			result.Content = a.injectRulesIntoResult(tc.Name, tc.Arguments, result.Content)
			if result.IsError {
				debug.Log("agent", "tool result ERROR: tool=%s output=%s", tc.Name, util.Truncate(result.Content, 200))
			}

			// Record tool errors for reflection/ratchet rule extraction.
			if result.IsError {
				runStats.recordToolError(tc.Name, result.Content)
			}

			// Error-streak detection: if consecutive tool calls are failing,
			// inject strategic guidance to break the cycle.
			if errorGuidance := a.errorStreakCheck(result.IsError, tc.Name); errorGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + errorGuidance
				} else {
					result.Content = errorGuidance
				}
			}

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
				if loopGuidance != "" {
					result.Content = result.Content + "\n\n" + loopGuidance
				}
				toolResults = append(toolResults, provider.ToolResultWithImages(tc.ID, tc.Name, result.Content, imgs, result.IsError))
			} else {
				if loopGuidance != "" {
					result.Content = result.Content + "\n\n" + loopGuidance
				}
				toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, result.Content, result.IsError))
			}

			onEvent(provider.StreamEvent{
				Type:    provider.StreamEventToolResult,
				Tool:    tc,
				Result:  result.Content,
				IsError: result.IsError,
			})

			if err := ctx.Err(); err != nil {
				// Context cancelled after completing some tools. Fill "cancelled"
				// results for remaining tool_calls that have not run yet.
				a.fillCancelledToolResults(toolCalls[idx+1:], &toolResults)
				return err
			}
		}

		if err := ctx.Err(); err != nil {
			// Context cancelled after all tools executed. toolResults has been
			// populated but not yet added to contextManager. We MUST add them
			// before returning to keep tool_use/tool_result pairs balanced.
			if len(toolResults) > 0 {
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: toolResults,
				})
			}
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

	var reasoningBuf strings.Builder
	var thinkingSignature string

	// Metric tracking — records timestamps during streaming, fires onMetric on Done.
	llmStartTime := time.Now()
	var firstTokenTime time.Time
	var thinkStartTime time.Time
	var thinkDuration time.Duration
	hasFirstToken := false

	for event := range stream {
		switch event.Type {
		case provider.StreamEventText:
			if !hasFirstToken && event.Text != "" {
				firstTokenTime = time.Now()
				hasFirstToken = true
			}
			onEvent(event)
			textBuf.WriteString(event.Text)
			assistantTextBuf.WriteString(event.Text)
		case provider.StreamEventReasoning:
			if !hasFirstToken && event.Text != "" {
				firstTokenTime = time.Now()
				hasFirstToken = true
			}
			// Track thinking duration
			if event.Text != "" && thinkStartTime.IsZero() {
				thinkStartTime = time.Now()
			}
			// Forward to UI for streaming display (GUI uses it for collapsible reasoning panel).
			onEvent(event)
			if event.Text != "" {
				reasoningBuf.WriteString(event.Text)
			}
			// Anthropic sends signature at block_start, before any text deltas.
			if event.ThinkingSignature != "" {
				thinkingSignature = event.ThinkingSignature
			}
		case provider.StreamEventToolCallChunk:
			onEvent(event)
		case provider.StreamEventToolCallDone:
			// Close thinking window if open
			if !thinkStartTime.IsZero() {
				thinkDuration += time.Since(thinkStartTime)
				thinkStartTime = time.Time{}
			}
			flushText()
			onEvent(event)
			toolCalls = append(toolCalls, event.Tool)
			content = append(content, provider.ToolUseBlock(event.Tool.ID, event.Tool.Name, event.Tool.Arguments))
		case provider.StreamEventDone:
			if event.Usage != nil {
				usage = *event.Usage
			}
			// Close thinking window if open
			if !thinkStartTime.IsZero() {
				thinkDuration += time.Since(thinkStartTime)
				thinkStartTime = time.Time{}
			}
			// Fire LLM metric
			now := time.Now()
			ttft := time.Duration(0)
			if !firstTokenTime.IsZero() {
				ttft = firstTokenTime.Sub(llmStartTime)
			}
			a.emitMetric(metrics.MetricEvent{
				Timestamp: now,
				Type:      "llm",
				TTFT:      ttft,
				ThinkTime: thinkDuration,
				Duration:  now.Sub(llmStartTime),
			})
			onEvent(event)

			// on_stream_stop hook (async fire-and-forget).
			a.mu.RLock()
			streamHookCfg := a.hookConfig
			streamWorkDir := a.workingDir
			a.mu.RUnlock()
			hooks.RunStreamStopHooks(streamHookCfg, hooks.HookEnv{
				Event:      hooks.EventOnStreamStop,
				Workspace:  streamWorkDir,
				WorkingDir: streamWorkDir,
				StopReason: "completed",
			})
		case provider.StreamEventError:
			debug.Log("agent", "ChatStream event error: %v", event.Error)
			return nil, assistantTextBuf.String(), nil, fmt.Errorf("chat error: %w", event.Error)
		}
	}

	flushText()

	// Build response message with optional reasoning content for echo-back.
	respMsg := provider.Message{
		Role:    "assistant",
		Content: content,
	}
	// Store reasoning/thinking content for echo-back to reasoning models.
	// - DeepSeek: reasoning_content (plain text)
	// - Anthropic: thinking block with signature
	if reasoningBuf.Len() > 0 || thinkingSignature != "" {
		rc := reasoningBuf.String()
		block := provider.ContentBlock{
			ReasoningContent:  rc,
			ThinkingSignature: thinkingSignature,
		}
		if thinkingSignature != "" {
			// Anthropic extended thinking
			block.Type = "thinking"
		} else {
			// DeepSeek reasoning
			block.Type = "text"
		}
		// Prepend thinking block so it appears before tool_use blocks
		respMsg.Content = append([]provider.ContentBlock{block}, respMsg.Content...)
	}

	return &provider.ChatResponse{
		Message: respMsg,
		Usage:   usage,
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

func (a *Agent) emitMetric(m metrics.MetricEvent) {
	a.mu.Lock()
	fn := a.onMetric
	a.mu.Unlock()
	if fn != nil {
		fn(m)
	}
}

// fillCancelledToolResults appends "cancelled" tool_result entries for tool_calls
// that were not executed due to context cancellation.
//
// Background: The LLM API protocol (OpenAI/Anthropic) requires that every tool_use
// block in an assistant message has a matching tool_result in the subsequent user
// message. If the agent loop is cancelled (e.g. user pressed Ctrl+C) while tools
// are being executed, some tool_calls will have results and some won't. Without
// this function, the contextManager would contain:
//
//	assistant: [tool_use(id=1), tool_use(id=2), tool_use(id=3)]
//	user:      [tool_result(id=1), tool_result(id=2)]       ← missing id=3!
//
// The next LLM API call would fail with a 400 error because tool_use(id=3) has no
// matching tool_result. This function fills in the gaps:
//
//	user: [tool_result(id=1), tool_result(id=2), tool_result(id=3, "cancelled")]
//
// This keeps the session valid for both in-memory continuation and JSONL resume.
//
// Parameters:
//   - pending: tool_calls that have NOT yet produced a result
//   - results: existing results slice to append to (modified in-place via pointer)
func (a *Agent) fillCancelledToolResults(pending []provider.ToolCallDelta, results *[]provider.ContentBlock) {
	for _, tc := range pending {
		debug.Log("agent", "Filling cancelled tool_result for tool=%s id=%s", tc.Name, tc.ID)
		*results = append(*results, provider.ToolResultNamedBlock(
			tc.ID, tc.Name,
			"operation cancelled by user",
			true, // mark as error so LLM knows it did not succeed
		))
	}
	if len(pending) > 0 {
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: *results,
		})
	}
}

// isJSON checks if raw message is valid JSON (for tool calls).
func isJSON(data json.RawMessage) bool {
	var v interface{}
	return json.Unmarshal(data, &v) == nil
}
