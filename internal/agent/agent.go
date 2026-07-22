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
	"github.com/topcheer/ggcode/internal/safego"
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
	provider                   provider.Provider
	tools                      *tool.Registry
	contextManager             ctxpkg.ContextManager
	maxIter                    int
	policy                     permission.PermissionPolicy
	onApproval                 ApprovalFunc
	onUsage                    func(usage provider.TokenUsage)
	usageSource                string // tracks the source of the current LLM call for usage persistence
	onMetric                   func(metrics.MetricEvent)
	onCheckpoint               func(summaryMsgID, lastMsgID string, tokenCount int)
	lastCheckpointMessageCount int // tracks last fallback checkpoint to avoid spamming
	onRunResult                runResultHandler
	hookConfig                 hooks.HookConfig
	workingDir                 string
	sessionID                  string // current session ID; determines todo file path
	checkpoints                *checkpoint.Manager
	diffConfirm                DiffConfirmFunc
	onInterrupt                interruptionHandler
	projectMemory              map[string]struct{}
	supportsVision             bool
	precompact                 *precompactState
	precompactCooldownUntil    time.Time // earliest next precompact; guarded by mu
	shutdownCtx                context.Context
	shutdownCancel             context.CancelFunc   // cancels on Close()
	probeKey                   string               // "vendor|baseURL|model" for context window auto-detection
	autopilotGoal              string               // current autopilot goal text; empty when no goal is active
	autopilotGoalAsked         bool                 // true after the goal-collection instruction has been injected
	autopilotGoalSet           bool                 // true after the user has confirmed a goal (goal text is non-empty)
	autopilotStrategistCount   int                  // number of strategist calls this run (safety valve)
	reflectionFunc             ReflectionFunc       // called after each run with accumulated stats
	loopDetector               loopDetector         // tracks consecutive identical tool calls to detect stuck loops
	errorClassifier            *ErrorClassifier     // immediate type-specific guidance on tool errors (AgentDebug-inspired)
	overseer                   *overseerState       // deterministic async-overseer: trajectory analysis for stuck/drift/spam
	repetition                 *repetitionTracker   // semantic-level repetition detection for failed edit clusters
	speculator                 *speculator          // pattern-aware speculative tool execution (PASTE-inspired)
	toolMemo                   *toolMemo            // read-only tool result memoization (ToolCaching-inspired)
	confidence                 *confidenceState     // holistic trajectory confidence scoring (HTC-inspired)
	budgetGuard                *budgetGuardState    // per-step token cost trend monitoring (BAGEN-inspired)
	cacheKeepalive             *cacheKeepaliveState // prompt cache warming pings during idle (Anthropic)
	postEditVerify             postEditVerifyState  // tracks source-code edits to inject periodic verification hints
	systemPromptInjector       func() string        // returns extra system prompt text to inject (e.g. lanchat peer warnings)
	baseSystemPrompt           string               // the fully built static system prompt; used as reset base for dynamic injection
	lastInjectedSystemPrompt   string               // cache of last injected prompt to skip redundant updates
	onVerifyProgress           func(text string)    // called during async verification (status updates)
	onVerifyResult             func(VerifyResult)   // called when async verification completes
	mu                         sync.RWMutex
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
		overseer:         newOverseerState(),
		repetition:       newRepetitionTracker(),
		speculator:       newSpeculator(),
		toolMemo:         newToolMemo(),
		confidence:       newConfidenceState(),
		budgetGuard:      newBudgetGuardState(),
		cacheKeepalive:   newCacheKeepaliveState(),
		errorClassifier:  NewErrorClassifier(),
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

// SetVerifyCallbacks sets callbacks for async post-run verification.
// progress is called with status text during verification.
// result is called when verification completes (passed or failed).
// Either callback may be nil.
func (a *Agent) SetVerifyCallbacks(progress func(string), result func(VerifyResult)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onVerifyProgress = progress
	a.onVerifyResult = result
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
	a.cacheKeepalive.stopIdle()
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
	a.lastInjectedSystemPrompt = "" // force re-injection on next iteration
	cm, ok := a.contextManager.(*ctxpkg.Manager)
	if !ok {
		return
	}
	cm.UpdateFirstSystemMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: text, Cache: true}},
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
// summaryMsgID is the ID of the summary message (already in JSONL via runAdded).
// lastMsgID is the ID of the last message in the snapshot before compaction.
func (a *Agent) SetCheckpointHandler(fn func(summaryMsgID, lastMsgID string, tokenCount int)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onCheckpoint = fn
}

// SetPersistHandler sets a per-message persistence callback. When set,
// every Add() call triggers this callback so messages are written to
// JSONL immediately, rather than batched at run end.
func (a *Agent) SetPersistHandler(fn func(msg provider.Message)) {
	if m, ok := a.contextManager.(*ctxpkg.Manager); ok {
		m.SetPersistHandler(fn)
	}
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

// SessionID returns the current session ID.
func (a *Agent) SessionID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessionID
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

// estimateToolDefinitionOverhead approximates the tokens consumed by the tool
// definitions (names + descriptions + JSON schemas) that are passed to the
// provider on every request. This is added to the context manager's dynamic
// prompt overhead so compaction decisions account for the real prompt size.
func estimateToolDefinitionOverhead(defs []provider.ToolDefinition) int {
	total := 0
	for _, d := range defs {
		total += len(d.Name)
		total += len(d.Description)
		total += len(d.Parameters)
	}
	return total / 4
}

// RunStreamWithContent runs the agent loop and emits UI events for complete model turns.
func (a *Agent) RunStreamWithContent(ctx context.Context, content []provider.ContentBlock, onEvent func(provider.StreamEvent)) (err error) {
	debug.Log("agent", "RunStreamWithContent START content_blocks=%d", len(content))

	// Stop any background cache-keepalive pings — the user is sending a new
	// message, so the cache will be refreshed naturally by this request.
	a.cacheKeepalive.stopIdle()

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
	// asyncVerifyStats captures run stats for the background verification goroutine.
	asyncVerifyStats := (*RunStats)(nil)

	// Reset loop detector for each new user turn.
	a.resetLoopDetector()
	a.errorClassifier.reset()
	a.resetPostEditVerify()
	a.resetRepetitionTracker()

	defer func() {
		runStats.finalize(err)
		// Skip reflection, ratchet LLM calls, and playbook recording on
		// cancellation. These post-run actions can trigger expensive,
		// un-cancellable LLM calls (ratchet uses context.Background() with
		// a 30s timeout) and produce noisy insights for aborted work.
		// The onRunResult callback and todo cleanup still run to ensure
		// session persistence and state cleanup.
		isCancelled := errors.Is(err, context.Canceled) ||
			(err == nil && ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled))
		if !isCancelled {
			a.maybeReflect(runStats)
		} else {
			debug.Log("agent", "skipping reflection/ratchet on cancellation")
		}
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
		// Launch async verification — does not block the return.
		// Runs build/test in background, reports result via callbacks.
		// Also skipped on cancellation (err != nil).
		if asyncVerifyStats != nil && err == nil && !isCancelled {
			statsCopy := *asyncVerifyStats
			safego.Go("asyncVerify", func() {
				a.asyncVerify(a.shutdownCtx, &statsCopy)
			})
		}
		// Fallback checkpoint: if the session has accumulated a large number
		// of messages without compaction succeeding, force-save a checkpoint.
		// This prevents unbounded context growth in autopilot sessions where
		// the summarization LLM call keeps failing.
		a.maybeFallbackCheckpoint()

		// Start background prompt-cache keepalive pings for Anthropic.
		// Sends a minimal request every 270s to keep the prompt cache warm
		// during idle, saving ~83K tokens when the user resumes.
		// Skipped on cancellation or when provider doesn't support caching.
		if !isCancelled && err == nil {
			if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
				msgs := cm.Messages()
				a.cacheKeepalive.startIdle(a.provider, msgs, a.tools.ToDefinitions())
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
	a.maybeInjectRatchetRules()

	transientCompactWarned := false
	toolDefs := a.tools.ToDefinitions()
	if cm, ok := a.contextManager.(interface{ SetToolDefinitionOverhead(int) }); ok {
		cm.SetToolDefinitionOverhead(estimateToolDefinitionOverhead(toolDefs))
	}
	reactiveCompactRetries := 0
	consecutiveEmptyResponses := 0
	progressCheckInjected := false
	contextWarningInjected := false
	todoCheckCount := 0

	a.autopilotStrategistCount = 0

	// Reset monitoring systems once at run start, NOT inside the iteration
	// loop. These systems accumulate state across iterations within a run.
	a.resetOverseer()
	a.speculator.resetSequence()
	a.toolMemo.reset()
	a.confidence.reset()
	a.budgetGuard.reset()

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

		// Mid-point progress checkpoint: at 60% of max iterations, inject a
		// one-time progress assessment. This is the lightweight "overseer"
		// pattern from SICA — giving the agent a chance to course-correct
		// before running out of iteration budget.
		// Only fires when maxIter >= 20 to avoid interfering with short runs.
		if a.maxIter >= 20 && !progressCheckInjected && i+1 >= a.maxIter*3/5 {
			progressCheckInjected = true
			debug.Log("agent", "Injecting mid-point progress checkpoint at iteration %d/%d", i+1, a.maxIter)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf(
						"Progress checkpoint: you are at iteration %d of %d. "+
							"Briefly assess: Are you on track to complete the task? "+
							"If your current approach isn't working efficiently, consider switching to a different strategy. "+
							"Prioritize completing the core task over perfectionism.",
						i+1, a.maxIter,
					),
				}},
			})
			msgs = a.contextManager.Messages() // refresh after adding checkpoint
		}

		// Context budget warning: at 80% context window utilization, inject a
		// one-time hint to use context-efficient strategies. This implements
		// the "context density awareness" pattern — the agent adjusts tool
		// usage when context space is scarce.
		if !contextWarningInjected && a.contextManager.ContextWindow() > 0 {
			usage := a.contextManager.UsageRatio()
			if usage >= 0.80 {
				contextWarningInjected = true
				debug.Log("agent", "Injecting context budget warning at %.0f%% utilization", usage*100)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: fmt.Sprintf(
							"Context note: %.0f%% of the context window is now in use. "+
								"To conserve context space: prefer targeted searches (grep) over full file reads, "+
								"avoid re-reading files you've already seen, and keep responses concise. "+
								"If possible, complete the task with fewer, more focused tool calls.",
							usage*100,
						),
					}},
				})
				msgs = a.contextManager.Messages()
			}
		}

		resp, textBuf, toolCalls, err := a.streamChatResponse(ctx, a.ensureMessagesSendable(msgs), toolDefs, onEvent)
		if err != nil {
			if errors.Is(err, errStreamInterruptedForReplan) {
				reactiveCompactRetries = 0
				continue
			}
			if a.tryReactiveCompact(ctx, onEvent, err, &reactiveCompactRetries) {
				runStats.recordCompaction()
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

		// Autopilot: extract GOAL: declaration from LLM output as early as
		// possible, so the strategist detection is active
		// for subsequent iterations.
		a.maybeSetAutopilotGoalFromLLMOutput(textBuf)

		a.syncContextManagerUsage(resp.Usage)
		a.emitUsage(resp.Usage)

		// Budget guard: track per-step output token cost trend (BAGEN-inspired).
		// Detects cost-escalation patterns that indicate a doomed trajectory.
		a.budgetGuard.recordStep(resp.Usage.OutputTokens, resp.Usage.InputTokens)
		if budgetWarning := a.budgetGuard.maybeWarn(a.contextManager.ContextWindow(), a.contextManager.TokenCount()); budgetWarning != "" {
			debug.Log("budget-guard", "cost escalation detected: steps=%d consumed=%d", len(a.budgetGuard.stepCosts), a.budgetGuard.totalConsumed)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: budgetWarning,
				}},
			})
			msgs = a.contextManager.Messages()
		}

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
				continue
			}

			// Autopilot strategist: when in autopilot mode with a confirmed
			// goal, call an independent LLM to analyze the full conversation
			// context and decide what the agent should do next. This replaces
			// the old deterministic text-pattern-matching autopilot logic.
			//
			// The strategist is ONLY called when the LLM stops calling tools
			// (len(toolCalls)==0), i.e., at natural decision points. Between
			// strategist calls there can be many tool-execution iterations
			// (3-10 typically), so the effective work per budget unit is much
			// higher than the raw count suggests.
			//
			// Budget: 30 calls per Run. With ~5 tool iterations between each
			// strategist call, this covers ~150 tool operations — enough for
			// medium-to-large tasks. For very large projects, the user simply
			// sends another message ("continue") to reset the budget.
			if a.currentMode() == permission.AutopilotMode && a.hasAutopilotGoal() && a.autopilotStrategistCount < maxAutopilotStrategistCalls {
				a.autopilotStrategistCount++
				debug.Log("agent", "Iteration %d: autopilot calling strategist (call #%d/%d)", i+1, a.autopilotStrategistCount, maxAutopilotStrategistCalls)
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist #%d/%d: analyzing conversation and deciding next steps...] ", a.autopilotStrategistCount, maxAutopilotStrategistCalls)})

				result, sErr := a.runAutopilotStrategist(ctx, textBuf)
				if sErr != nil {
					debug.Log("agent", "autopilot strategist failed: %v", sErr)
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist unavailable (%v) — autopilot stopping]", sErr)})
					// Fall through to normal return — can't drive autonomously.
				} else if result.Complete {
					debug.Log("agent", "Iteration %d: strategist declared goal achieved", i+1)
					// Strip the "GOAL_ACHIEVED" sentinel; the rest is the
					// strategist's summary of what was accomplished.
					summary := result.Guidance
					if len(summary) >= 13 && strings.EqualFold(summary[:13], "GOAL_ACHIEVED") {
						summary = strings.TrimSpace(summary[13:])
					}
					msg := "[Strategist: goal achieved — autopilot complete.]"
					if summary != "" {
						msg = fmt.Sprintf("[Strategist: goal achieved — autopilot complete. %s]", summary)
					}
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: msg})
					a.clearAutopilotGoal()
					return nil
				} else if result.Guidance != "" {
					debug.Log("agent", "Iteration %d: strategist injecting guidance (%d chars)", i+1, len(result.Guidance))
					a.contextManager.Add(provider.Message{
						Role: "user",
						Content: []provider.ContentBlock{{
							Type: "text",
							Text: result.Guidance,
						}},
					})
					continue
				} else {
					// Strategist returned empty guidance (not complete, not error).
					// This can happen with content-filtered or malformed API responses.
					debug.Log("agent", "Iteration %d: strategist returned empty guidance", i+1)
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Strategist returned no guidance — autopilot stopping]"})
					a.clearAutopilotGoal()
					return nil
				}
			} else if a.currentMode() == permission.AutopilotMode && a.hasAutopilotGoal() {
				// Strategist call budget exhausted. Instead of stopping the run,
				// inject a guidance message that pushes the agent to verify its
				// work and continue any remaining tasks. This keeps autopilot
				// running through the natural maxIter limit rather than cutting
				// short at an arbitrary strategist count.
				debug.Log("agent", "Iteration %d: strategist budget exhausted (%d/%d), injecting continuation guidance", i+1, a.autopilotStrategistCount, maxAutopilotStrategistCalls)
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist budget at limit (%d/%d) — continuing autonomously]", a.autopilotStrategistCount, maxAutopilotStrategistCalls)})
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: "Strategist guidance budget is exhausted. Review the original goal and all work done so far. If there are any remaining tasks from the plan, continue implementing them. If all planned tasks are done, verify: run build, run tests, check for TODOs or incomplete items. If everything passes, provide a final summary of what was accomplished.",
					}},
				})
				continue
			}

			// Check for incomplete todos before finishing. If the agent
			// created todos but didn't complete them, inject a reminder
			// instead of silently finishing. Max 2 reminders to avoid loops.
			if todoCheckCount < 2 {
				if reminder := a.checkIncompleteTodos(); reminder != "" {
					todoCheckCount++
					debug.Log("agent", "Iteration %d: incomplete todos detected, injecting reminder (%d/2)", i+1, todoCheckCount)
					a.contextManager.Add(provider.Message{
						Role: "user",
						Content: []provider.ContentBlock{{
							Type: "text",
							Text: reminder,
						}},
					})
					continue
				}
			}
			// Capture stats for async verification before returning.
			asyncVerifyStats = runStats
			debug.Log("agent", "Iteration %d: no tool calls, returning", i+1)
			return nil
		}

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

		// Parallel pre-execution of read-only tools (LLMCompiler/W&D-inspired).
		// When the LLM returns multiple tool calls, independent read-only tools
		// are executed concurrently before the sequential loop. Results are
		// consumed in-order; side-effect tools still run sequentially.
		preExecuted := a.preExecuteReadOnlyTools(ctx, toolCalls)

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
			// Check memoization cache: if a read-only tool was called with identical args
			// earlier in this run (and the underlying resource hasn't changed), return the
			// cached result. This prevents redundant re-execution after tool-result clearing.
			var result tool.Result
			if memoResult, hit := a.toolMemo.get(tc.Name, tc.Arguments); hit {
				result = memoResult
				debug.Log("memoize", "memo hit for %s (saved tool execution)", tc.Name)
			} else if cachedResult, hit := a.speculator.getCached(tc.Name, tc.Arguments); hit {
				result = cachedResult
				debug.Log("speculate", "speculative cache hit for %s (saved tool execution)", tc.Name)
			} else if pre, ok := preExecuted[idx]; ok {
				// Parallel pre-execution result (LLMCompiler/W&D-inspired).
				// Runs permission check; if denied, the read-only result is discarded.
				result = a.usePreExecutedWithPermission(ctx, tc, pre)
			} else {
				result = a.executeToolWithPermission(ctx, tc)
			}
			// Record the tool call for speculative pattern learning.
			a.speculator.recordObservation(tc.Name)
			// File-editing tools invalidate the speculative cache: any
			// pre-executed read_file/grep results for edited files are now
			// stale. Clear the cache to prevent serving outdated content.
			if fileEditingTools[tc.Name] && !result.IsError {
				a.speculator.invalidateCache()
			}
			// Store result in memoization cache for read-only tools.
			if speculativeSafeTools[tc.Name] && !result.IsError {
				a.toolMemo.put(tc.Name, tc.Arguments, result)
			}
			// Inject matching harness rules into the result
			result.Content = a.injectRulesIntoResult(tc.Name, tc.Arguments, result.Content)
			if result.IsError {
				debug.Log("agent", "tool result ERROR: tool=%s output=%s", tc.Name, util.Truncate(result.Content, 200))
			}

			// Record tool errors for reflection/ratchet rule extraction.
			if result.IsError {
				runStats.recordToolError(tc.Name, result.Content)
			}

			// Error classifier: immediate type-specific guidance on the first
			// occurrence of each error category (AgentDebug-inspired).
			// Fires before error-streak so the agent gets targeted feedback
			// immediately, not after 4 consecutive failures.
			if result.IsError {
				if catGuidance := a.errorClassifier.classifyToolError(tc.Name, result.Content); catGuidance.Name != "" {
					g := fmt.Sprintf("[Error guidance: %s] %s", catGuidance.Name, catGuidance.Guidance)
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + g
					} else {
						result.Content = g
					}
				}
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

			// Overseer: deterministic trajectory analysis (SICA-inspired).
			// Detects tool spam, read-only stall, stuck-on-file, error escalation, and drift.
			if overseerGuidance := a.overseerCheck(tc.Name, result.IsError, extractFileHint(tc.Name, tc.Arguments), runStats.Iterations); overseerGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + overseerGuidance
				} else {
					result.Content = overseerGuidance
				}
			}

			// Repetition tracker: semantic-level detection of failed edit clusters.
			// Catches near-miss loops that exact-match loop detection misses.
			if repetitionGuidance := a.repetitionCheckEdit(tc.Name, tc.Arguments, result.IsError); repetitionGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + repetitionGuidance
				} else {
					result.Content = repetitionGuidance
				}
			}
			// Also check read-edit-fail cycles for read_file calls.
			if tc.Name == "read_file" || tc.Name == "multi_file_read" {
				if readGuidance := a.repetitionCheckRead(extractFileHint(tc.Name, tc.Arguments)); readGuidance != "" {
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + readGuidance
					} else {
						result.Content = readGuidance
					}
				}
			}

			// Trajectory confidence: record result and check for early warning.
			// HTC-inspired: detect "overconfidence in failure" before errors compound.
			a.confidence.recordResult(tc.Name, result.IsError, extractFileHint(tc.Name, tc.Arguments))
			if confidenceGuidance := a.confidence.maybeIntervene(); confidenceGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + confidenceGuidance
				} else {
					result.Content = confidenceGuidance
				}
			}

			// Smart verify hint reset: if the agent ran a build/test/verify command,
			// reset the edit counter and track the result.
			a.maybeResetVerifyOnCommand(tc.Name, tc.Arguments, result.IsError)

			// Post-edit verification hint: after successful source-code edits,
			// periodically suggest running the build command to verify changes.
			if !result.IsError {
				if verifyHint := a.postEditVerifyHint(tc.Name, tc.Arguments); verifyHint != "" {
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + verifyHint
					} else {
						result.Content = verifyHint
					}
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
			// Context-fill-aware output guard: proactively truncate large
			// non-error results when context is getting full. This prevents
			// a single 50KB build log from consuming 12K+ tokens when the
			// context window is already under pressure. Head-tail preservation
			// ensures the agent sees both context (head) and errors/results (tail).
			if !result.IsError {
				threshold := a.contextManager.AutoCompactThreshold()
				if threshold > 0 {
					fillRatio := float64(a.contextManager.TokenCount()) / float64(threshold)
					if truncated := guardToolOutput(result.Content, fillRatio); len(truncated) < len(result.Content) {
						debug.Log("agent", "tool output guarded: tool=%s tokens=%d threshold=%d fill=%.0f%% %d→%d bytes", tc.Name, a.contextManager.TokenCount(), threshold, fillRatio*100, len(result.Content), len(truncated))
						result.Content = truncated
					}
				}
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
				// fillCancelledToolResults adds to contextManager only when
				// pending > 0. If this was the last tool call, we still need to
				// add the completed results to keep tool_use/tool_result pairs
				// balanced for the next LLM call.
				if len(toolCalls[idx+1:]) == 0 && len(toolResults) > 0 {
					a.contextManager.Add(provider.Message{
						Role:    "user",
						Content: toolResults,
					})
				}
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

		// Speculative tool execution (PASTE-inspired): now that tool results
		// are sent to the LLM, the LLM will spend 2-5 seconds generating its
		// next response. Use that idle window to speculatively pre-execute
		// likely next read-only tool calls based on learned patterns.
		if len(toolCalls) > 0 {
			// Context-fill-aware: skip speculation when context is critically
			// full (>75%). Speculative results arriving into a nearly-full
			// context window can trigger unnecessary compaction. Speculation
			// is optional — skipping it is always safe.
			speculateOK := true
			if a.contextManager != nil {
				if threshold := a.contextManager.AutoCompactThreshold(); threshold > 0 {
					fillRatio := float64(a.contextManager.TokenCount()) / float64(threshold)
					if fillRatio >= contextFillCritical {
						speculateOK = false
						debug.Log("speculate", "skipping speculation: context fill %.0f%%", fillRatio*100)
					}
				}
			}
			if speculateOK {
				lastTC := toolCalls[len(toolCalls)-1]
				a.speculator.speculate(ctx, a.tools, lastTC.Name, lastTC.Arguments)
			}
		}

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
		// Emit a summary of what was accomplished before the error, so the
		// user has actionable context instead of a bare "max iterations" message.
		runStats.finalize(nil) // compute Duration for the summary
		summary := runStats.Summary()
		debug.Log("agent", "RunStreamWithContent END: max iterations reached (%s)", summary)
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\nReached maximum iterations (%d). Summary: %s.\nYour task may be partially complete — review the changes above. You can continue by sending a follow-up message.", a.maxIter, summary),
		})
		err := fmt.Errorf("max iterations (%d) reached", a.maxIter)
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
				Timestamp:    now,
				Type:         "llm",
				TTFT:         ttft,
				ThinkTime:    thinkDuration,
				Duration:     now.Sub(llmStartTime),
				InputTokens:  usage.InputTokens,
				OutputTokens: usage.OutputTokens,
				CacheRead:    usage.CacheRead,
				CacheWrite:   usage.CacheWrite,
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
		case provider.StreamEventSystem:
			// Forward provider-level system messages (retry notifications, etc.)
			onEvent(event)
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

// emitUsage invokes the usage callback with the given source tag.
// source values: "agent", "strategist", "verify", "ratchet".
func (a *Agent) emitUsage(usage provider.TokenUsage) {
	a.emitUsageWithSource(usage, "agent")
}

func (a *Agent) emitUsageWithSource(usage provider.TokenUsage, source string) {
	a.mu.Lock()
	fn := a.onUsage
	a.usageSource = source
	a.mu.Unlock()
	if fn != nil {
		fn(usage)
	}
}

// UsageSource returns the source tag of the most recent LLM call.
// Used by the usage callback to categorize usage entries in the session JSONL.
func (a *Agent) UsageSource() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.usageSource
}

func (a *Agent) emitMetric(m metrics.MetricEvent) {
	a.mu.RLock()
	fn := a.onMetric
	a.mu.RUnlock()
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
