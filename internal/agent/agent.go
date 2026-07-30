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

// maxAgentLLMRetries is the number of agent-level retries for transient LLM
// errors that slip past the provider's own retry loop (providerRetryAttempts=20).
// These are typically mid-stream disconnects or DNS hiccups after partial output.
const maxAgentLLMRetries = 3

// isAgentRetryableLLMError returns true for transient errors that warrant an
// agent-level retry. Excludes: context overflow (handled by reactive compact),
// user cancellation (should not retry), auth errors (retrying won't help),
// and permanent quota exhaustion (429 with billing/quota keywords — provider
// layer already detected and chose not to retry, agent should respect that).
func isAgentRetryableLLMError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	s := strings.ToLower(err.Error())

	// Quota/billing exhaustion is permanent — never retry, even if the error
	// contains "rate limit" or "429". Coding plan providers (ZAI/GLM, Kimi,
	// OpenAI) use 429 for both transient rate limits AND permanent quota
	// exhaustion. The provider layer's shared classifier already filters
	// these out; we must do the same at the agent level.
	if provider.ClassifyLLMError(err) == provider.FailureQuota {
		return false
	}

	for _, keyword := range []string{
		"connection reset by peer",
		"unexpected eof",
		"broken pipe",
		"tls handshake timeout",
		"server closed idle connection",
		"no such host",
		"connection refused",
		"i/o timeout",
		"eof",
		"retry attempts exhausted", // provider gave up after 20 tries
		"rate limit",               // 429 Too Many Requests after provider retries
		"rate_limit",               // snake_case variant from API error codes
		"too many requests",        // standard HTTP 429 message
		"overloaded",               // Anthropic overload response
		"service unavailable",      // 503 temporary server overload
		"bad gateway",              // 502 transient proxy error
	} {
		if strings.Contains(s, keyword) {
			return true
		}
	}
	return false
}

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
	onRunHealth                func(error) // run-level health signal (success/failure) for node health reporting
	hookConfig                 hooks.HookConfig
	workingDir                 string
	sessionID                  string // current session ID; determines todo file path
	checkpoints                *checkpoint.Manager
	codeIndex                  *tool.CodeIndexManager // optional: background BM25 index for code_search
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
	strategistBudgetAnnounced  bool                 // true once the budget-exhausted message has been injected
	reflectionFunc             ReflectionFunc       // called after each run with accumulated stats
	loopDetector               loopDetector         // tracks consecutive identical tool calls to detect stuck loops
	errorClassifier            *ErrorClassifier     // immediate type-specific guidance on tool errors (AgentDebug-inspired)
	overseer                   *overseerState       // deterministic async-overseer: trajectory analysis for stuck/drift/spam
	repetition                 *repetitionTracker   // semantic-level repetition detection for failed edit clusters
	speculator                 *speculator          // pattern-aware speculative tool execution (PASTE-inspired)
	toolMemo                   *toolMemo            // read-only tool result memoization (ToolCaching-inspired)
	confidence                 *confidenceState     // holistic trajectory confidence scoring (HTC-inspired)
	budgetGuard                *budgetGuardState    // per-step token cost trend monitoring (BAGEN-inspired)
	costBudget                 *sessionCostBudget   // absolute session-level token budget enforcement
	cacheKeepalive             *cacheKeepaliveState // prompt cache warming pings during idle (Anthropic)
	commandCache               *commandCache        // deterministic build/test command result caching
	emptySearch                *emptySearchState    // empty search spiral detection (futile search guidance)
	postEditVerify             postEditVerifyState  // tracks source-code edits to inject periodic verification hints
	planner                    *planState           // agent-side auto task decomposition (Devin/Claude Code-inspired)
	todoStaleness              *todoStalenessState  // mid-run stale todo detection (plan abandonment awareness)
	recurringError             *recurringErrorState // recurring build/test error fingerprint detection across edit cycles
	unreadEdit                 *unreadEditState     // read-before-edit guard: warns when editing unread files
	editFailRecovery           *editFailState       // consecutive edit failure recovery guidance
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
		costBudget:       newSessionCostBudget(),
		cacheKeepalive:   newCacheKeepaliveState(),
		commandCache:     newCommandCache(),
		emptySearch:      newEmptySearchState(),
		errorClassifier:  NewErrorClassifier(),
		planner:          newPlanState(),
		todoStaleness:    newTodoStalenessState(),
		recurringError:   newRecurringErrorState(),
		unreadEdit:       newUnreadEditState(),
		editFailRecovery: newEditFailState(),
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

// SetRunHealthHandler sets a callback invoked after each RunStreamWithContent
// completes, receiving the final error (nil on success, including success
// after internal retries). Unlike onRunResult it is a dedicated slot for
// node health reporting (e.g. lanchat presence) and does not conflict with
// the session-persistence handler.
func (a *Agent) SetRunHealthHandler(fn func(error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onRunHealth = fn
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
		if debug.IsVerbose("agent") {
			debug.Log("agent", "syncUsage: input=%d output=%d", usage.InputTokens, usage.OutputTokens)
		}
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

// InvalidateToolCaches clears the speculator and memoize caches. Called after
// external file changes (e.g., /undo, /revert) that bypass the normal tool
// execution path, to prevent serving stale cached results.
func (a *Agent) InvalidateToolCaches() {
	a.speculator.invalidateCache()
	a.toolMemo.invalidateTTLBased()
	a.commandCache.invalidate()
}

// extractEditedPaths parses a tool call's arguments to extract file paths
// from file-editing tools. Used to mark files dirty in the code index.
func extractEditedPaths(tc provider.ToolCallDelta) []string {
	if len(tc.Arguments) == 0 {
		return nil
	}
	var args map[string]any
	if json.Unmarshal(tc.Arguments, &args) != nil {
		return nil
	}
	var paths []string
	switch tc.Name {
	case "write_file", "edit_file", "read_file":
		if p, ok := args["path"].(string); ok {
			paths = append(paths, p)
		}
		if p, ok := args["file_path"].(string); ok {
			paths = append(paths, p)
		}
	case "multi_file_edit", "multi_file_write":
		if files, ok := args["files"].([]any); ok {
			for _, f := range files {
				if fm, ok := f.(map[string]any); ok {
					if p, ok := fm["path"].(string); ok {
						paths = append(paths, p)
					}
				}
			}
		}
	case "notebook_edit":
		if p, ok := args["notebook_path"].(string); ok {
			paths = append(paths, p)
		}
	}
	return paths
}

// SetCodeIndexManager sets the persistent code index for semantic search.
// When set, file edits are tracked so the index stays fresh via MarkDirty.
func (a *Agent) SetCodeIndexManager(m *tool.CodeIndexManager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.codeIndex = m
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

// SetSessionTokenBudget sets the maximum total tokens (input + output)
// allowed for a single agent run. 0 disables budget enforcement.
func (a *Agent) SetSessionTokenBudget(budget int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.costBudget.SetBudget(budget)
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
	// syncVerifyRetries tracks how many auto-repair cycles have been consumed
	// by the synchronous verification gate. Bounded by maxSyncVerifyRetries.
	syncVerifyRetries := 0

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
		// Node health reporting: separate slot from onRunResult so both
		// can be registered without conflict.
		a.mu.RLock()
		healthFn := a.onRunHealth
		a.mu.RUnlock()
		if healthFn != nil {
			healthFn(err)
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

	// Agent-side planning: analyze the user's first message for complexity.
	// If complex (multi-file, multi-goal, multi-step), suggest a structured
	// plan early in the conversation (Devin/Claude Code auto-planning pattern).
	a.plannerAnalyze(userText)

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
	agentLLMRetries := 0
	inlineToolCallNudges := 0
	consecutiveEmptyResponses := 0
	truncationContinues := 0
	progressCheckInjected := false
	convergence85Injected := false    // 85% iteration budget: shift to convergence
	convergence95Injected := false    // 95% iteration budget: must finalize now
	contextWarningLevel := 0          // 0=none, 1=95%, 2=99%, 3=100%
	budgetHintLevel := budgetHintNone // proactive context conservation (70%, 85%)
	todoCheckCount := 0

	a.autopilotStrategistCount = 0
	a.strategistBudgetAnnounced = false

	// Reset monitoring systems once at run start, NOT inside the iteration
	// loop. These systems accumulate state across iterations within a run.
	a.resetOverseer()
	a.resetPlanner()
	a.resetTodoStaleness()
	a.recurringError.reset()
	a.speculator.resetSequence()
	a.toolMemo.reset()
	a.commandCache.reset()
	a.confidence.reset()
	a.budgetGuard.reset()
	a.costBudget.reset()
	a.emptySearch.reset()
	// Reset the unread-file edit tracker so each run starts fresh.
	a.unreadEdit.reset()
	// Reset the edit failure recovery tracker.
	a.editFailRecovery.reset()

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
		if debug.IsVerbose("agent") {
			debug.Log("agent", "Iteration %d/%d: contextManager messages=%d tokens=%d threshold=%d usage_ratio=%.3f maxTokens=%d",
				i+1, a.maxIter, len(msgs), a.contextManager.TokenCount(), a.contextManager.AutoCompactThreshold(), a.contextManager.UsageRatio(), a.contextManager.ContextWindow())
		}

		// Agent-side planning: inject a plan suggestion or reminder early in
		// the conversation when the request was detected as complex. This is
		// a deterministic, zero-LLM-cost approach inspired by Devin's Planner
		// and Claude Code's auto-todo behavior.
		if planHint := a.maybeSuggestPlan(i + 1); planHint != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: planHint}},
			})
			msgs = a.contextManager.Messages()
		}

		// Mid-run stale todo detection: if the agent created a todo list but
		// hasn't updated it for several iterations while there are still
		// incomplete items, inject a one-time reminder to sync the plan.
		if staleReminder := a.maybeRemindStaleTodo(i + 1); staleReminder != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: staleReminder}},
			})
			msgs = a.contextManager.Messages()
		}

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
						"Progress checkpoint: iteration %d/%d. Assess — on track? If not, switch strategy.",
						i+1, a.maxIter,
					),
				}},
			})
			msgs = a.contextManager.Messages() // refresh after adding checkpoint
		}

		// Convergence pressure: at 85% and 95% of max iterations, inject
		// one-time wrap-up guidance. The mid-point checkpoint (60%) asks the
		// agent to assess strategy; these tell it to shift into finalization
		// mode. This prevents the agent from being abruptly cut off mid-task
		// when the iteration budget runs out — a common UX problem in agents
		// without convergence awareness (Claude Code and Cursor both have
		// iteration-end convergence behavior).
		// Only fires when maxIter >= 20 to avoid interfering with short runs.
		if a.maxIter >= 20 && !convergence85Injected && i+1 >= a.maxIter*85/100 {
			convergence85Injected = true
			remaining := a.maxIter - (i + 1)
			debug.Log("agent", "Injecting convergence pressure (85%%) at iteration %d/%d (%d remaining)", i+1, a.maxIter, remaining)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf(
						"Iteration budget low: %d/%d used, ~%d remaining. Shift to convergence — finalize current changes, run build/test verification, and prepare a concise summary of what was accomplished. Do not start new exploration or unrelated tasks.",
						i+1, a.maxIter, remaining,
					),
				}},
			})
			msgs = a.contextManager.Messages()
		}
		if a.maxIter >= 20 && !convergence95Injected && i+1 >= a.maxIter*95/100 {
			convergence95Injected = true
			remaining := a.maxIter - (i + 1)
			debug.Log("agent", "Injecting final convergence (95%%) at iteration %d/%d (%d remaining)", i+1, a.maxIter, remaining)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf(
						"Final iterations: only ~%d remaining. You MUST produce your final response now — verify your changes compile, then summarize what was done and any remaining issues.",
						remaining,
					),
				}},
			})
			msgs = a.contextManager.Messages()
		}

		// Proactive context budget efficiency hints at 70% and 85% fill.
		// These fire BEFORE the crisis-level warnings below, giving the agent
		// a chance to self-regulate and avoid or delay compaction entirely.
		if a.maybeInjectBudgetHint(&budgetHintLevel) {
			msgs = a.contextManager.Messages()
		}

		// Context budget warnings at 95%, 99%, and 100% utilization.
		// Each level fires once, with escalating urgency. The goal is to help
		// the agent prepare for imminent compaction — NOT to make it stop.
		if a.contextManager.ContextWindow() > 0 {
			usage := a.contextManager.UsageRatio()
			var newLevel int
			var msgText string
			switch {
			case usage >= 1.0 && contextWarningLevel < 3:
				newLevel = 3
				msgText = "Context full — compaction now. Finish current step, do not stop."
			case usage >= 0.99 && contextWarningLevel < 2:
				newLevel = 2
				msgText = "Context at 99%. Compaction imminent — keep working, do NOT wrap up; avoid full file reads."
			case usage >= 0.95 && contextWarningLevel < 1:
				newLevel = 1
				msgText = "Context at 95%. Compaction soon — keep working, do NOT wrap up; prefer grep over full file reads."
			}
			if newLevel > contextWarningLevel {
				contextWarningLevel = newLevel
				debug.Log("agent", "Injecting context budget warning level %d at %.0f%% utilization", newLevel, usage*100)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: msgText,
					}},
				})
				msgs = a.contextManager.Messages()
			}
		}

		resp, textBuf, toolCalls, truncated, err := a.streamChatResponse(ctx, a.ensureMessagesSendable(msgs), toolDefs, onEvent)
		if err != nil {
			if errors.Is(err, errStreamInterruptedForReplan) {
				reactiveCompactRetries = 0
				agentLLMRetries = 0
				continue
			}
			if a.tryReactiveCompact(ctx, onEvent, err, &reactiveCompactRetries) {
				runStats.recordCompaction()
				continue
			}
			// Agent-level retry for transient LLM errors that slip past the
			// provider's own retry loop (e.g. mid-stream disconnect after
			// partial output, DNS hiccup between provider retries).
			if isAgentRetryableLLMError(err) && agentLLMRetries < maxAgentLLMRetries {
				agentLLMRetries++
				// Use longer backoff for rate limiting errors (429/overloaded)
				// vs. transient network errors. Rate limits need more time
				// to reset before retrying.
				multiplier := 2 // seconds per retry step
				errStr := strings.ToLower(err.Error())
				if strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "rate_limit") || strings.Contains(errStr, "too many") || strings.Contains(errStr, "overloaded") {
					multiplier = 5 // 5s, 10s, 15s for rate-limited requests
				}
				delay := time.Duration(agentLLMRetries*multiplier) * time.Second
				debug.Log("agent", "transient LLM error (attempt %d/%d), retrying in %v: %v",
					agentLLMRetries, maxAgentLLMRetries, delay, err)
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem,
					Text: fmt.Sprintf("[Retrying LLM call (%d/%d) after %v...] ",
						agentLLMRetries, maxAgentLLMRetries, delay)})
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ctx.Err()})
					return ctx.Err()
				}
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
		agentLLMRetries = 0

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

		// Cost budget: track absolute session-level token consumption.
		// Enforces a configurable per-session token budget with progressive
		// warnings (75%, 90%) and a hard stop at 100%.
		a.costBudget.recordStep(resp.Usage.InputTokens, resp.Usage.OutputTokens)
		if costMsg, stop := a.costBudget.check(); costMsg != "" {
			debug.Log("cost-budget", "budget threshold crossed: consumed=%d budget=%d stop=%v",
				a.costBudget.totalTokens, a.costBudget.budget, stop)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: costMsg,
				}},
			})
			msgs = a.contextManager.Messages()
			if stop {
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventSystem,
					Text: costMsg,
				})
				return nil
			}
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
			// Truncated response recovery: the LLM hit the output token limit
			// mid-response. Save the partial output and inject a continuation
			// prompt so the model picks up where it left off. This prevents
			// silent loss of partial content (the old behavior sent a hard error
			// and discarded everything already streamed).
			if truncated && truncationContinues < 3 {
				truncationContinues++
				debug.Log("agent", "Iteration %d: response truncated by output limit, auto-continuing (attempt %d/3)", i+1, truncationContinues)
				a.contextManager.Add(resp.Message)
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventSystem,
					Text: "[Response was truncated by output length limit — continuing...] ",
				})
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: "Your previous response was cut off by the output token limit. Continue from where you left off — do not repeat what you already wrote.",
					}},
				})
				continue
			}

			// Detect inline tool calls in text/reasoning (common with lower-reasoning
			// models that write tool calls in prose instead of structured tool_use blocks).
			// Nudge the model to use proper tool call format and retry.
			assistantText := textBuf
			if hasInlineToolCall(assistantText) && inlineToolCallNudges < 2 {
				inlineToolCallNudges++
				debug.Log("agent", "Iteration %d: inline tool call detected in text, nudging model (attempt %d/2)", i+1, inlineToolCallNudges)
				a.contextManager.Add(resp.Message)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: "Use structured tool_use format, not inline text syntax for tool calls.",
					}},
				})
				continue
			}

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
			} else if a.currentMode() == permission.AutopilotMode && a.hasAutopilotGoal() && !a.strategistBudgetAnnounced {
				// Strategist call budget exhausted. Inject a one-time guidance
				// message so the agent can wrap up or continue with its own
				// judgment. Without the flag this would re-inject on every
				// subsequent no-tool-call iteration, creating an infinite loop.
				a.strategistBudgetAnnounced = true
				debug.Log("agent", "Iteration %d: strategist budget exhausted (%d/%d), injecting one-time continuation guidance", i+1, a.autopilotStrategistCount, maxAutopilotStrategistCalls)
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist budget at limit (%d/%d) — continuing autonomously]", a.autopilotStrategistCount, maxAutopilotStrategistCalls)})
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: "Strategist budget exhausted. Continue remaining tasks autonomously — build, test, verify, then summarize.",
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
			// Synchronous verification with auto-repair.
			// Before returning, verify the build if code was changed. If it
			// fails and retry budget remains, inject errors and continue the
			// loop — this is the "fix-on-fail" pattern used by Claude Code,
			// Aider, and Cursor. It eliminates the manual round-trip where
			// the user must say "fix the build" after every failed change.
			syncPassed := false
			if codeChangedInRun(runStats) && a.currentMode() != permission.PlanMode && ctx.Err() == nil {
				if a.syncVerifyAndGate(ctx, runStats, syncVerifyRetries) {
					syncVerifyRetries++
					debug.Log("agent", "Iteration %d: sync verify failed, auto-repairing (retry %d/%d)", i+1, syncVerifyRetries, maxSyncVerifyRetries)
					continue
				}
				// Sync verify ran (passed or budget exhausted).
				// If it passed on the first attempt, skip redundant async verify.
				syncPassed = syncVerifyRetries == 0
			}
			if syncPassed {
				debug.Log("agent", "sync verify passed, skipping async verify")
			} else {
				// Capture stats for async verification before returning.
				asyncVerifyStats = runStats
			}
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

		// Batch edit conflict detection: when the LLM emits multiple file-editing
		// calls targeting the same file in one batch, warn upfront so the model
		// knows subsequent edits may fail (file content changes after each edit).
		batchConflictWarnings := detectBatchEditConflicts(toolCalls)

		// Deduplicate identical read-only tool calls within the same LLM response.
		// LLMs occasionally emit duplicate calls (e.g., two read_file for the
		// same path). Skip the second execution and reuse the first result.
		type dedupKey struct {
			tool string
			args string
		}
		seenReadOnly := make(map[dedupKey]int) // key → index of first result in toolResults

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
			// In-turn deduplication: if the LLM sent the same read-only tool call
			// twice in this response, reuse the first result instead of re-executing.
			dedupK := dedupKey{tool: tc.Name, args: string(tc.Arguments)}
			if speculativeSafeTools[tc.Name] {
				if firstIdx, ok := seenReadOnly[dedupK]; ok && firstIdx < len(toolResults) {
					dedupContent := toolResults[firstIdx].Text
					debug.Log("agent", "in-turn dedup: %s already executed in this response, reusing result", tc.Name)
					toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, dedupContent, false))
					onEvent(provider.StreamEvent{
						Type:    provider.StreamEventToolResult,
						Tool:    tc,
						Result:  dedupContent,
						IsError: false,
					})
					continue
				}
			}
			// Check memoization cache: if a read-only tool was called with identical args
			// earlier in this run (and the underlying resource hasn't changed), return the
			// cached result. This prevents redundant re-execution after tool-result clearing.
			var result tool.Result
			if memoResult, hit := a.toolMemo.get(tc.Name, tc.Arguments); hit {
				result = memoResult
				// Annotate cache hits so the model knows this is cached content, not a
				// fresh execution. After tool-result clearing replaces old results with
				// placeholders, the model re-calls the tool and gets identical content back.
				// Without this annotation, the model treats it as new information and
				// re-analyzes identical content (wasting attention budget). The annotation
				// lets the model skip redundant analysis and proceed efficiently.
				// Context-efficient: only added for non-empty, non-error results, and the
				// prefix is capped at 80 chars.
				if result.Content != "" && !result.IsError {
					result.Content = fmt.Sprintf("[cached — %s returned identical content since your last call]\n%s", tc.Name, result.Content)
				}
				debug.Log("memoize", "memo hit for %s (saved tool execution)", tc.Name)
			} else if cachedResult, hit := a.speculator.getCached(tc.Name, tc.Arguments); hit {
				result = cachedResult
				debug.Log("speculate", "speculative cache hit for %s (saved tool execution)", tc.Name)
			} else if pre, ok := preExecuted[idx]; ok {
				// Parallel pre-execution result (LLMCompiler/W&D-inspired).
				// Runs permission check; if denied, the read-only result is discarded.
				result = a.usePreExecutedWithPermission(ctx, tc, pre)
			} else if cmdCached, hit := a.checkCommandCache(tc.Name, tc.Arguments); hit {
				// Deterministic command cache: skip re-running build/test commands
				// when no source files have changed since the last execution.
				result = cmdCached
			} else {
				result = a.executeToolWithPermission(ctx, tc)
				// Cache deterministic command results (build, test, lint, etc.)
				// for reuse when the same command is called again without file changes.
				a.storeCommandResult(tc.Name, tc.Arguments, result)
			}
			// Record the tool call for speculative pattern learning.
			a.speculator.recordObservation(tc.Name)
			// Track todo_write usage for the agent-side planner: once the
			// agent creates a todo list, plan suggestions and reminders stop.
			if tc.Name == "todo_write" && !result.IsError {
				a.plannerMarkTodoCreated()
				// Track for stale todo detection: record the iteration so we
				// can detect plan abandonment if the agent stops updating.
				todoCount := parseTodoCount(tc.Arguments)
				a.recordTodoStalenessUpdate(i+1, todoCount)
			}
			// File-editing tools invalidate the speculative cache: any
			// pre-executed read_file/grep results for edited files are now
			// stale. Clear the cache to prevent serving outdated content.
			if (fileEditingTools[tc.Name] || gitFileModifyingTools[tc.Name] || tc.Name == "notebook_edit") && !result.IsError {
				a.speculator.invalidateCache()
				// Also invalidate TTL-based memoize entries (grep, LSP, git)
				// whose results may be stale after a file edit. mtime-based
				// entries (read_file, list_directory) are kept — their
				// validity is tied to the file's modification time.
				a.toolMemo.invalidateTTLBased()
				// Invalidate the deterministic command cache: any build/test
				// results are now stale because source files changed.
				a.commandCache.invalidate()
				// Record created files so the unread-edit guard exempts them.
				for _, p := range extractCreateFilePaths(tc.Name, tc.Arguments) {
					a.unreadEdit.recordCreated(p)
				}
				// Track edit for recurring-error detection: increments the
				// "edits since last build error" counter so that a recurring
				// error with edits in between is flagged as a root-cause gap.
				a.recurringErrorRecordEdit()
				// Mark edited files as dirty in the code index so the
				// background indexer can update them incrementally.
				if a.codeIndex != nil {
					a.codeIndex.MarkDirty(extractEditedPaths(tc))
				}
			}
			// Store result in memoization cache for read-only tools.
			if speculativeSafeTools[tc.Name] && !result.IsError {
				a.toolMemo.put(tc.Name, tc.Arguments, result)
			}
			// Track files read during this run so the unread-edit guard
			// knows which files the agent has seen.
			if (tc.Name == "read_file" || tc.Name == "multi_file_read") && !result.IsError {
				for _, p := range extractReadFilePaths(tc.Name, tc.Arguments) {
					a.unreadEdit.recordRead(p)
					a.editFailRecovery.recordRead(p)
				}
			}
			// Unread-file edit guard: warn when editing a file not read in
			// this run. Fires before the tool executes so the hint is in the
			// result alongside any error from the edit attempt.
			if !result.IsError && (tc.Name == "edit_file" || tc.Name == "multi_edit_file" || tc.Name == "multi_file_edit") {
				for _, p := range extractEditFilePaths(tc.Name, tc.Arguments) {
					a.editFailRecovery.recordEditSuccess(p)
					if hint := a.unreadEdit.checkUnreadEdit(p); hint != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + hint
						} else {
							result.Content = hint
						}
					}
					// Stale-read detection: warn when the file was modified on
					// disk since the last read (external edit, git pull, etc.).
					if hint := a.unreadEdit.checkStaleRead(p); hint != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + hint
						} else {
							result.Content = hint
						}
					}
				}
			}
			// Consecutive edit failure recovery: when an edit fails on a file
			// 2+ times in a row, inject targeted guidance to re-read the file
			// before retrying. This catches the common "edit fail loop" pattern
			// faster than the overseer (which runs every 12 iterations).
			if result.IsError && (tc.Name == "edit_file" || tc.Name == "multi_edit_file" || tc.Name == "multi_file_edit") {
				for _, p := range extractEditFilePaths(tc.Name, tc.Arguments) {
					if hint := a.editFailRecovery.recordEditFailure(p); hint != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + hint
						} else {
							result.Content = hint
						}
					}
				}
			}
			// Inject matching harness rules into the result
			result.Content = a.injectRulesIntoResult(tc.Name, tc.Arguments, result.Content)
			// Batch edit conflict warning: if this tool call targets a file that
			// is also edited by another call in the same batch, inject a warning
			// so the model understands why edits may fail and how to consolidate.
			if warn, ok := batchConflictWarnings[idx]; ok {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + warn
				} else {
					result.Content = warn
				}
			}
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

			// Empty search spiral detection: tracks consecutive search tools
			// returning no results and injects alternative strategy guidance.
			// Fires before other guards so the guidance is visible early.
			if emptyGuidance := a.emptySearch.recordResult(tc.Name, result.Content, result.IsError, extractFileHint(tc.Name, tc.Arguments)); emptyGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + emptyGuidance
				} else {
					result.Content = emptyGuidance
				}
			}

			// Smart verify hint reset: if the agent ran a build/test/verify command,
			// reset the edit counter and track the result.
			a.maybeResetVerifyOnCommand(tc.Name, tc.Arguments, result.IsError)

			// Recurring error detection: when a build/test command returns the
			// SAME error after file edits, inject guidance that the edits aren't
			// addressing the root cause. This catches the #1 agent failure mode
			// (incremental edits that don't fix the underlying problem).
			if recurringGuidance := a.recurringErrorCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); recurringGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + recurringGuidance
				} else {
					result.Content = recurringGuidance
				}
			}

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
			// Prompt injection guard: scan external-content tool results for
			// adversarial injection patterns and wrap them with a security
			// notice so the model treats them as untrusted data.
			result.Content = guardPromptInjection(tc.Name, result.Content)

			// Secret redaction: mask API keys, tokens, private keys, and other
			// credentials in tool outputs before they enter context. Prevents
			// accidental leakage of secrets to external LLM providers and
			// session history persistence.
			result.Content = redactSecrets(tc.Name, result.Content)

			// Repetitive-line compression: collapse consecutive identical or
			// template-similar lines (common in build/test/install output) before
			// the size-based guard. This may prevent truncation entirely for
			// outputs that are large only due to repetition.
			if compressed := compressRepetitiveLines(result.Content); len(compressed) < len(result.Content) {
				debug.Log("compress", "repetitive-line compression: tool=%s %d→%d bytes", tc.Name, len(result.Content), len(compressed))
				result.Content = compressed
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

			// Register read-only tool results for in-turn deduplication.
			if speculativeSafeTools[tc.Name] && !result.IsError {
				seenReadOnly[dedupK] = len(toolResults) - 1
			}

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
func (a *Agent) streamChatResponse(ctx context.Context, msgs []provider.Message, toolDefs []provider.ToolDefinition, onEvent func(provider.StreamEvent)) (*provider.ChatResponse, string, []provider.ToolCallDelta, bool, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := a.provider.ChatStream(streamCtx, msgs, toolDefs)
	if err != nil {
		debug.Log("agent", "ChatStream error: %v", err)
		return nil, "", nil, false, fmt.Errorf("chat error: %w", err)
	}

	var (
		textBuf          strings.Builder
		assistantTextBuf strings.Builder
		content          []provider.ContentBlock
		toolCalls        []provider.ToolCallDelta
		usage            provider.TokenUsage
		truncated        bool
	)

	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		s := textBuf.String()
		// Skip whitespace-only text blocks — these occur when models emit
		// newlines/spaces between tool_use blocks with no meaningful content.
		// Keeping them wastes tokens and can cause API errors on strict providers.
		if strings.TrimSpace(s) != "" {
			content = append(content, provider.TextBlock(s))
		}
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
			truncated = event.Truncated
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
			return nil, assistantTextBuf.String(), nil, false, fmt.Errorf("chat error: %w", event.Error)
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
	}, assistantTextBuf.String(), toolCalls, truncated, nil
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
