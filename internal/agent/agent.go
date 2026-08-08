package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
		"resource_exhausted",       // Gemini 429 rate limit error type
		"engine_overloaded",        // Kimi engine_overloaded_error
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
	shutdownCancel             context.CancelFunc                    // cancels on Close()
	probeKey                   string                                // "vendor|baseURL|model" for context window auto-detection
	autopilotGoal              string                                // current autopilot goal text; empty when no goal is active
	autopilotGoalAsked         bool                                  // true after the goal-collection instruction has been injected
	autopilotGoalSet           bool                                  // true after the user has confirmed a goal (goal text is non-empty)
	autopilotStrategistCount   int                                   // number of strategist calls this run (safety valve)
	strategistBudgetAnnounced  bool                                  // true once the budget-exhausted message has been injected
	strategistNoProgressCount  int                                   // consecutive strategist calls where agent made no tool calls
	reflectionFunc             ReflectionFunc                        // called after each run with accumulated stats
	loopDetector               loopDetector                          // tracks consecutive identical tool calls to detect stuck loops
	errorClassifier            *ErrorClassifier                      // immediate type-specific guidance on tool errors (AgentDebug-inspired)
	overseer                   *overseerState                        // deterministic async-overseer: trajectory analysis for stuck/drift/spam
	repetition                 *repetitionTracker                    // semantic-level repetition detection for failed edit clusters
	speculator                 *speculator                           // pattern-aware speculative tool execution (PASTE-inspired)
	toolMemo                   *toolMemo                             // read-only tool result memoization (ToolCaching-inspired)
	confidence                 *confidenceState                      // holistic trajectory confidence scoring (HTC-inspired)
	verifDebt                  *verificationDebtState                // verification debt tracker (SAUP-inspired uncertainty propagation)
	editAbandon                *editAbandonState                     // edit abandonment detection (PASTE/LLMCompiler-inspired attention-shift tracking)
	costBudget                 *sessionCostBudget                    // absolute session-level token budget enforcement
	toolCallBudget             *toolCallBudget                       // per-session tool invocation limit (action-level guardrail)
	cacheKeepalive             *cacheKeepaliveState                  // prompt cache warming pings during idle (Anthropic)
	commandCache               *commandCache                         // deterministic build/test command result caching
	emptySearch                *emptySearchState                     // empty search spiral detection (futile search guidance)
	postEditVerify             postEditVerifyState                   // tracks source-code edits to inject periodic verification hints
	planner                    *planState                            // agent-side auto task decomposition (Devin/Claude Code-inspired)
	todoStaleness              *todoStalenessState                   // mid-run stale todo detection (plan abandonment awareness)
	recurringError             *recurringErrorState                  // recurring build/test error fingerprint detection across edit cycles
	errStrategyLoop            *errStrategyState                     // error strategy loop detection (procedural memory failure)
	solutionFixation           *solutionFixationState                // solution fixation: diagnosis anchoring on failed edit clusters
	fixCascade                 *fixCascadeState                      // failed fix cascade (wrong-hypothesis lock-in) detection
	errRegression              *errRegressionState                   // error count regression (negative progress) detection
	stalledConvergence         *stalledConvergenceState              // stalled convergence detection (diminishing returns pattern)
	unreadEdit                 *unreadEditState                      // read-before-edit guard: warns when editing unread files
	expiredRead                *expiredReadState                     // expired-read detection: self-invalidated context awareness (AgentDiet)
	editFailRecovery           *editFailState                        // consecutive edit failure recovery guidance
	scopeDrift                 *scopeDriftState                      // semantic scope creep detection (file-diversity tracking)
	driftRecurrence            *driftRecurrenceState                 // drift recurrence detection (post-warning behavioral persistence)
	constraintAmnesia          *constraintAmnesiaState               // constraint amnesia detection (early constraint forgetting)
	constraintViolation        *constraintViolationState             // self-declared constraint violation detection (AgentRx step-level tracking)
	exportGuard                *exportGuardState                     // breaking change detection for exported Go symbols (regression guard)
	hubPackageGuard            *hubPackageState                      // per-edit blast-radius awareness for high fan-in packages
	artifactGuard              *generatedArtifactState               // generated artifact / lock file edit warning
	toolFilter                 *tool.RelevanceFilter                 // dynamic MCP tool pruning based on conversation relevance
	fulfillmentGate            *fulfillmentGateState                 // pre-completion coverage verification (request-vs-work match)
	ambiguityPoint             *ambiguityPointState                  // pre-run intent disambiguation (ambiguity detection in user request)
	companionGuard             *companionGuardState                  // companion test file coverage check (unedited paired tests)
	specGaming                 *specGamingState                      // specification gaming detection (reward hacking / verification tampering)
	scopeNarrow                *scopeNarrowState                     // verification scope narrowing detection (command-level spec gaming)
	complexityGate             *complexityGateState                  // post-completion code complexity quality gate
	changeReconcile            *changeReconcileState                 // pre-completion git diff reconciliation (unexpected side-effect detection)
	claimVerify                *claimVerifyState                     // tool output misinterpretation detection (AgentRx-inspired)
	diffSummary                *diffSummaryState                     // pre-completion holistic change summary for self-review
	commitHint                 *commitHintState                      // post-completion commit reminder for uncommitted changes
	verifyRegression           *verifyRegressionState                // cross-run error diff: detects correction-induced regressions
	selfCorrectionGate         *selfCorrectionGateState              // EIR/ECR stability gate: detects net-negative self-correction loops
	lastGoodCheckpoint         *lastGoodCheckpoint                   // last-known-good file snapshot: actionable revert targets for failed self-correction
	sessionTimeout             *sessionTimeoutState                  // wall-clock timeout for agent runs (autopilot guardrail)
	diskSpace                  *diskSpaceState                       // low disk space detection (resource exhaustion awareness)
	envDrift                   *envDriftState                        // env var drift detection (.env.example vs actual env)
	transientRetryBudget       int                                   // remaining automatic retries for transient tool failures (per run)
	compoundingFailure         *compoundingFailureState              // sliding-window cross-tool failure rate (strategy reset detection)
	failureMode                *failureModeState                     // meta-level failure mode classification (transient/structural/systemic)
	toolFallback               *toolFallbackState                    // tool error fallback chain (actionable recovery suggestions)
	argSizeGuardFires          int                                   // count of argument size guard injections this run
	fileFreshness              *fileFreshnessSentinel                // proactive cross-iteration external file change detection
	readHash                   *readHashTracker                      // content-fingerprint read validity (sub-second mtime race detection, false-positive suppression)
	toolThermal                *thermalState                         // cross-tool usage balance monitor (explore/modify/verify distribution)
	latencyTracker             *LatencyTracker                       // per-tool latency baseline & slow-tool outlier detection
	toolSequence               *toolSequenceValidator                // cross-iteration tool call anti-pattern detection
	planDrift                  *planDriftState                       // plan drift detection (exit_plan_mode item tracking)
	unverifiedClaim            *unverifiedClaimState                 // unverified success claim detection (text claims vs actual verification)
	convergenceLock            *convergenceLockState                 // post-verification unnecessary edit drift detection
	userSentiment              *userSentimentState                   // negative user feedback detection (frustration/rejection course correction)
	taskAnchor                 *taskAnchorState                      // periodic task re-anchoring for context collapse prevention
	adaptiveSampling           *adaptiveSamplingState                // per-turn temperature adaptation (phase-aware sampling control)
	effortAdapter              *adaptiveEffortState                  // per-turn reasoning effort adaptation (Opus 5 effort toggle pattern)
	branchGuard                *branchGuardState                     // protected branch edit warning (main/master/develop awareness)
	destructiveGuard           *gitDestructiveState                  // destructive git operation detection (reset --hard, force push, etc.)
	shellNativeHint            *shellNativeHintState                 // suggests native tools when agent uses shell for equivalent operations
	monorepoScoper             *monorepoScoperState                  // monorepo package scope sprawl detection
	mcpEcosystem               *mcpEcosystemState                    // MCP server health, conflict, and capability intelligence
	mcpRuntime                 tool.MCPRuntime                       // MCP runtime for server snapshots (optional)
	bgOrphan                   *bgOrphanState                        // orphaned background command detection (unchecked start_command jobs)
	actionAnnihil              *actionAnnihilateState                // action annihilation detection (tool calls that cancel prior side effects)
	toolStorm                  *toolStormState                       // tool call storm detection (diverse tools fired without reasoning)
	reasoningRedund            *reasoningRedundancyState             // reasoning redundancy detection (consecutive text-only overthinking)
	queryConverge              *queryConvergeState                   // query convergence failure detection (repeated similar searches without action)
	serialRead                 *serialReadState                      // sequential read serialization detection (cross-turn single-read batching opportunity)
	strategyFixation           *strategyFixationState                // strategy fixation detection (same file edited N times with failed verifications -- approach-level failure)
	errorRush                  *errorRushState                       // error rush / panic coding detection (blind-fixing after consecutive errors without diagnosis)
	targetScatter              *targetScatterState                   // target scatter detection (world model miscalibration - unfocused investigation across many unrelated files)
	attentionFragment          *attentionFragmentState               // attention fragmentation detection (CLT extraneous load from rapid directory context-switching)
	errorCompound              *errorCompoundState                   // error compounding risk detector (systemic trajectory reliability)
	correctionSpiral           *correctionSpiralState                // correction spiral detector (error severity escalation across fixes)
	wastedExplore              *wastedExploreState                   // wasted exploration detection (search results never acted upon)
	toolResultRedundancy       *toolResultRedundancyState            // tool result redundancy detection (overlapping content across calls)
	anchorErosion              *anchorErosionState                   // anchor precision decay detection (edit argument quality degradation over run lifecycle)
	tunnelVision               *tunnelVisionState                    // tunnel vision detection (narrow file scope / under-exploration)
	prematureCommit            *prematureCommitState                 // premature commitment detection (insufficient evidence before first edit)
	selfMod                    *selfModState                         // self-modification safety guard (agent editing its own infrastructure)
	diagnosticDisconnect       *diagnosticDisconnectState            // diagnostic-action disconnect detection (ignored diagnostics from failed tool calls)
	bareEditStreak             *bareEditStreakState                  // unverified mutation streak detection (consecutive edits without verification)
	recklessExec               *recklessExecState                    // reckless execution detection (edits to unexplored files in early iterations)
	irrevGate                  *irrevGateState                       // irreversibility-weighted calibration gate (caution scales with action reversibility)
	verifyDebt                 *verifyDebtState                      // verification debt accumulator (edits since last green build)
	editPropagation            *editPropagationState                 // cross-file edit propagation risk (distinct files since green build)
	errorCascade               *errorCascadeState                    // cascading failure detection (common-root-cause error clustering)
	delegationOrch             *delegationState                      // delegation orchestration intelligence (orphaned delegations, serial anti-pattern, over-delegation)
	crossFileImpact            *crossFileImpactState                 // pre-completion cross-file impact analysis (removed symbol breakage detection)
	contextFootprint           *contextFootprintState                // per-tool context budget attribution (which tools consume the most context)
	promptOps                  *promptOpsState                       // system prompt redundancy and token efficiency intelligence (PromptOps)
	cacheEffMonitor            *cacheEffMonitor                      // prompt cache efficiency monitoring (cache bust storm detection)
	pressureForecaster         *pressureForecaster                   // context window pressure forecasting (predictive compaction warning)
	redundantRead              *redundantReadState                   // redundant re-read detection (context waste prevention)
	searchParamGuard           *searchParamGuardState                // search parameter quality guard (vague/broad pattern detection)
	toolRedundancy             *toolRedundancyState                  // scattered duplicate tool call detection (non-consecutive redundancy)
	ruleStore                  *RuleStore                            // cached rule store for hot-path rule injection (avoids per-tool disk I/O)
	ruleInjectCount            map[string]int                        // per-rule injection counter for dedup (caps repetitive hints)
	approvalMemory             *permission.ApprovalMemory            // session-level learned approval patterns (auto-approve after N repeats)
	toolDiversity              *diversityState                       // tool diversity stagnation detection (strategy imbalance awareness)
	fileChurn                  *churnState                           // file churn detection (invalidated assumption awareness)
	editOscillation            *oscillationState                     // edit oscillation detection (semantic back-and-forth awareness)
	analysisParalysis          *analysisParalysisState               // analysis paralysis detection (exploration-heavy / action-starved loops)
	overReflection             *overReflectionDetector               // over-reflection detection (text-heavy no-action turns, arXiv:2506.12928)
	toolCallEconomy            *toolCallEconomyState                 // tool call economy detection (batchable individual calls)
	silentError                *silentErrorState                     // silent error advancement detection (unaddressed error proceeding)
	verifySuppress             *verifySuppressState                  // verification suppression detection (reward hacking via error masking)
	verbosityDrift             *verbosityDriftState                  // verbosity drift detection (token-to-productivity ratio degradation)
	cusumDrift                 *cusumDriftState                      // CUSUM statistical drift detection (cumulative behavioral deviation)
	toolOveruse                *toolOveruseState                     // tool overuse / self-awareness detection (unnecessary tool calls for known info)
	assumptionTracker          *assumptionTrackerState               // implicit assumption detection (unverified guesses in assistant text)
	sycophancyGuard            *sycophancyState                      // user-premise sycophancy detection (agreeing with user premises without verification)
	unverifiedConfidence       *unverifiedConfidenceState            // unverified confidence detection (overconfident claims without verification)
	phantomVerify              *phantomVerifyState                   // phantom verification detection (category-specific verification claims without matching commands)
	evidenceOverconfidence     *evidenceOverconfidenceState          // evidence-induced overconfidence (tool-type calibration asymmetry)
	verifyDisconnect           *verifyDisconnectState                // verification outcome disconnect (advancing past failures)
	selectiveEvidence          *selectiveEvidenceTrackerState        // confirmation bias detection (cherry-picked evidence + dismissed negatives)
	temporalBlindness          *temporalBlindnessState               // temporal blindness detection (stale verification across mutations)
	selfDiagState              *selfDiagState                        // unverified self-diagnosis detection (correlated failure after errors)
	deferredWork               *deferredWorkState                    // deferred work tracking (forgotten follow-up detection)
	circularReasoning          *circularReasoningState               // circular reasoning detection (tautological justification)
	contradiction              *contradictionState                   // cross-turn contradiction detection (root-cause reversals)
	ungroundedReflect          *ungroundedReflectionState            // ungrounded reflection detector (text-only thinking loops)
	actionHedging              *actionHedgingState                   // action hedging detection (verbalized uncertainty during mutations)
	scopeCreep                 *scopeCreepState                      // scope creep detection (unsolicited changes beyond request)
	prematureAbstr             *prematureAbstrState                  // premature abstraction detection (over-engineering within task scope)
	capBoundary                *capabilityBoundaryState              // capability boundary detection (stubborn persistence beyond solvability)
	planAbandon                *planAbandonState                     // plan abandonment detection (declare plan, claim done without executing)
	toolTargetMismatch         *toolTargetState                      // tool-target mismatch detection (stated intent vs actual tool target)
	outcomeMisattrib           *outcomeMisattribState                // outcome misattribution detection (success claim despite failure result)
	trajectoryHealth           *trajectoryHealthState                // metacognitive trajectory health synthesis (multi-signal composite)
	reversibility              *reversibilityState                   // pre-action reversibility assessment (irreversible action safety check)
	mindlessAction             *mindlessActionState                  // mindless action detection (rapid-fire tool calls without reasoning)
	successDeclare             *successDeclareState                  // premature success declaration detection (calibration gap: done claim + continued work)
	criteriaDrift              *criteriaDriftState                   // success criteria drift detection (proxy gaming via evaluator weakening)
	reasonAction               *reasonActionState                    // reasoning-action alignment verification (cognitive category mismatch)
	symbolGrounding            *symbolGroundingState                 // symbol grounding verification (ungrounded code symbol reference detection)
	inputUnderspec             *inputUnderspecState                  // input underspecification detection (vague/underspecified user request)
	futileCycle                *futileCycleState                     // futile cycle detection (circular exploration without writes)
	compoundedUncert           *compoundedUncertaintyState           // compounded trajectory uncertainty (multiplicative epistemic risk accumulation)
	spiralState                *spiralHallucinationState             // cross-turn epistemic error propagation (Spiral of Hallucination)
	trajIntel                  *trajIntelState                       // post-run trajectory intelligence extraction
	strategyStagnation         *strategyStagnationState              // strategy stagnation detection (same-tool+target retries after failure)
	iterPressure               *iterPressureState                    // iteration pressure degradation detection (verify/edit ratio drop near budget limit)
	momentumLoss               *momentumLossState                    // late-phase productivity collapse detection (last-mile stall)
	diminishingEdit            *diminishingEditState                 // polish-spiral detection (diminishing edit substance)
	overcorrection             *overcorrectionState                  // overcorrection cascade detection (disproportionate fix size)
	prematureSurrender         *surrenderState                       // premature task abandonment detection (metacognitive surrender awareness)
	prematureRefactor          *prematureRefactorState               // premature refactoring detection (unverified code restructuring awareness)
	subgoalTrack               *subgoalState                         // subgoal completion integrity (missing-step planning failure awareness)
	infoScent                  *infoScentState                       // information scent decay detection (diminishing novelty across explorations)
	foresightCalib             *foresightCalibrateState              // foresight calibration (prediction-observation mismatch tracking, WorldEvolver arXiv:2606.30639)
	causalAttribution          *causalAttributionState               // causal failure attribution (CausalFlow-inspired root-cause step identification)
	attemptBrief               *attemptBriefState                    // compact attempt summary for knowledge reuse across failed approaches
	behaviorPattern            *behaviorPatternState                 // cross-run behavioral anti-pattern detection (systemic issue awareness)
	crossDetectorConsensus     *consensusState                       // cross-detector consensus (systemic failure from simultaneous detector firings)
	taintInfluence             *taintInfluenceState                  // tainted data influence detection (IFC: tracks untrusted content flowing into privileged tool calls)
	falsePremise               *falsePremiseState                    // false premise detection: ungrounded success claims contradicting tool errors (world-model drift)
	perfBaseline               *perfBaselineState                    // cross-session performance regression detection
	guidancePromoter           *GuidancePromoter                     // cross-session guidance tag recurrence → proactive rule promotion (inter-test-time evolution)
	lastRunStats               *RunStats                             // stats from the most recent run (for post-run summary display)
	qualityScorer              *ResponseQualityScorer                // per-run response quality scoring for provider/model A/B comparison
	systemPromptInjector       func() string                         // returns extra system prompt text to inject (e.g. lanchat peer warnings)
	baseSystemPrompt           string                                // the fully built static system prompt; used as reset base for dynamic injection
	lastInjectedSystemPrompt   string                                // cache of last injected prompt to skip redundant updates
	onVerifyProgress           func(text string)                     // called during async verification (status updates)
	onVerifyResult             func(VerifyResult)                    // called when async verification completes
	onToolProgress             func(toolID, toolName, output string) // called for streaming tool output (e.g. wait_command)
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
		provider:               p,
		tools:                  tools,
		maxIter:                maxIter,
		contextManager:         ctxpkg.NewManager(128000),
		projectMemory:          make(map[string]struct{}),
		baseSystemPrompt:       systemPrompt,
		shutdownCtx:            ctx,
		shutdownCancel:         cancel,
		overseer:               newOverseerState(),
		repetition:             newRepetitionTracker(),
		speculator:             newSpeculator(),
		toolMemo:               newToolMemo(),
		confidence:             newConfidenceState(),
		verifDebt:              newVerificationDebtState(),
		editAbandon:            newEditAbandonState(),
		costBudget:             newSessionCostBudget(),
		toolCallBudget:         newToolCallBudget(),
		cacheKeepalive:         newCacheKeepaliveState(),
		commandCache:           newCommandCache(),
		emptySearch:            newEmptySearchState(),
		errorClassifier:        NewErrorClassifier(),
		planner:                newPlanState(),
		todoStaleness:          newTodoStalenessState(),
		recurringError:         newRecurringErrorState(),
		errStrategyLoop:        newErrStrategyState(),
		fixCascade:             newFixCascadeState(),
		errRegression:          newErrRegressionState(),
		stalledConvergence:     newStalledConvergenceState(),
		unreadEdit:             newUnreadEditState(),
		expiredRead:            newExpiredReadState(),
		falsePremise:           newFalsePremiseState(),
		editFailRecovery:       newEditFailState(),
		scopeDrift:             newScopeDriftState(),
		driftRecurrence:        newDriftRecurrenceState(),
		constraintAmnesia:      newConstraintAmnesiaState(),
		constraintViolation:    newConstraintViolationState(),
		exportGuard:            newExportGuardState(),
		hubPackageGuard:        newHubPackageState(),
		artifactGuard:          newGeneratedArtifactState(),
		branchGuard:            newBranchGuardState(),
		destructiveGuard:       newGitDestructiveState(),
		shellNativeHint:        newShellNativeHintState(),
		monorepoScoper:         newMonorepoScoperState(),
		mcpEcosystem:           newMCPEcosystemState(),
		approvalMemory:         permission.NewApprovalMemory(),
		behaviorPattern:        newBehaviorPatternState(),
		crossDetectorConsensus: newConsensusState(),
		taintInfluence:         newTaintInfluenceState(),
		perfBaseline:           newPerfBaselineState(),
		fulfillmentGate:        newFulfillmentGateState(),
		ambiguityPoint:         newAmbiguityPointState(),
		planDrift:              newPlanDriftState(),
		unverifiedClaim:        newUnverifiedClaimState(),
		companionGuard:         newCompanionGuardState(),
		specGaming:             newSpecGamingState(),
		scopeNarrow:            newScopeNarrowState(),
		complexityGate:         newComplexityGateState(),
		changeReconcile:        newChangeReconcileState(),
		claimVerify:            newClaimVerifyState(),
		diffSummary:            newDiffSummaryState(),
		commitHint:             newCommitHintState(),
		verifyRegression:       newVerifyRegressionState(),
		selfCorrectionGate:     newSelfCorrectionGateState(),
		lastGoodCheckpoint:     newLastGoodCheckpoint(),
		toolFilter:             tool.NewRelevanceFilter(),
		latencyTracker:         NewLatencyTracker(),
		toolSequence:           newToolSequenceValidator(),
		taskAnchor:             newTaskAnchorState("", time.Time{}),
		adaptiveSampling:       newAdaptiveSamplingState(),
		effortAdapter:          newAdaptiveEffortState(),
		sessionTimeout:         newSessionTimeoutState(0),
		fileFreshness:          newFileFreshnessSentinel(),
		readHash:               newReadHashTracker(),
		toolThermal:            newThermalState(),
		userSentiment:          newUserSentimentState(),
		transientRetryBudget:   maxTransientRetryBudgetPerRun,
		compoundingFailure:     newCompoundingFailureState(),
		failureMode:            newFailureModeState(),
		toolFallback:           newToolFallbackState(),
		contextFootprint:       newContextFootprintState(),
		promptOps:              newPromptOpsState(),
		cacheEffMonitor:        newCacheEffMonitor(),
		pressureForecaster:     newPressureForecaster(),
		redundantRead:          newRedundantReadState(),
		searchParamGuard:       newSearchParamGuard(),
		toolRedundancy:         newToolRedundancyAnalyzer(),
		bgOrphan:               newBgOrphanState(),
		actionAnnihil:          newActionAnnihilateState(),
		serialRead:             newSerialReadState(),
		toolStorm:              newToolStormState(),
		outcomeMisattrib:       newOutcomeMisattribState(),
		reasoningRedund:        newReasoningRedundancyState(),
		queryConverge:          newQueryConvergeState(),
		causalAttribution:      newCausalAttributionState(),
		errorCompound:          newErrorCompoundState(),
		correctionSpiral:       newCorrectionSpiralState(),
		wastedExplore:          newWastedExploreState(),
		toolResultRedundancy:   newToolResultRedundancyState(),
		anchorErosion:          newAnchorErosionState(),
		selfMod:                newSelfModState(),
		tunnelVision:           newTunnelVisionState(),
		prematureCommit:        newPrematureCommitState(),
		diagnosticDisconnect:   newDiagnosticDisconnectState(),
		bareEditStreak:         newBareEditState(),
		strategyFixation:       newStrategyFixationState(),
		errorRush:              newErrorRushState(),
		targetScatter:          newTargetScatterState(),
		attentionFragment:      newAttentionFragmentState(),
		recklessExec:           newRecklessExecState(),
		irrevGate:              newIrrevGateState(),
		verifyDebt:             newVerifyDebtState(),
		editPropagation:        newEditPropagationState(),
		toolDiversity:          newDiversityState(),
		fileChurn:              newChurnState(),
		editOscillation:        newOscillationState(),
		analysisParalysis:      newAnalysisParalysisState(),
		overReflection:         newOverReflectionDetector(),
		toolCallEconomy:        newToolCallEconomyState(),
		silentError:            newSilentErrorState(),
		verifySuppress:         newVerifySuppressState(),
		verbosityDrift:         newVerbosityDriftState(),
		cusumDrift:             newCusumDriftState(),
		toolOveruse:            newToolOveruseState(),
		assumptionTracker:      newAssumptionTrackerState(),
		sycophancyGuard:        newSycophancyState(),
		unverifiedConfidence:   newUnverifiedConfidenceState(),
		phantomVerify:          newPhantomVerifyState(),
		solutionFixation:       newSolutionFixationState(),
		evidenceOverconfidence: newEvidenceOverconfidenceState(),
		verifyDisconnect:       newVerifyDisconnectState(),
		selectiveEvidence:      newSelectiveEvidenceTrackerState(),
		temporalBlindness:      newTemporalBlindnessState(),
		selfDiagState:          newSelfDiagState(),
		deferredWork:           newDeferredWorkState(),
		circularReasoning:      newCircularReasoningState(),
		contradiction:          newContradictionState(),
		ungroundedReflect:      newUngroundedReflectionState(),
		actionHedging:          newActionHedgingState(),
		scopeCreep:             newScopeCreepState(),
		prematureAbstr:         newPrematureAbstrState(),
		capBoundary:            newCapabilityBoundaryState(),
		planAbandon:            newPlanAbandonState(),
		compoundedUncert:       newCompoundedUncertaintyState(),
		spiralState:            newSpiralHallucinationState(),
		trajectoryHealth:       newTrajectoryHealthState(),
		mindlessAction:         newMindlessActionState(),
		strategyStagnation:     newStrategyStagnationState(),
		iterPressure:           newIterPressureState(maxIter),
		momentumLoss:           newMomentumLossState(),
		diminishingEdit:        newDiminishingEditState(),
		overcorrection:         newOvercorrectionState(),
		prematureSurrender:     newSurrenderState(),
		prematureRefactor:      newPrematureRefactorState(),
		subgoalTrack:           newSubgoalState(),
		infoScent:              newInfoScentState(),
		foresightCalib:         newForesightCalibrateState(),
		reversibility:          newReversibilityState(),
		errorCascade:           newErrorCascadeState(),
		crossFileImpact:        newCrossFileImpactState(),
		diskSpace:              newDiskSpaceState(),
		envDrift:               newEnvDriftState(),
		successDeclare:         newSuccessDeclareState(),
		criteriaDrift:          newCriteriaDriftState(),
		reasonAction:           newReasonActionState(),
		attemptBrief:           newAttemptBriefState(),
		symbolGrounding:        newSymbolGroundingState(),
		inputUnderspec:         newInputUnderspecState(),
		qualityScorer:          NewResponseQualityScorer(100),
		futileCycle:            newFutileCycleState(),
		trajIntel:              newTrajIntelState(),
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

// QualityComparison returns aggregated quality stats per provider/model pair,
// sorted by average score descending. Returns nil if no runs have been scored.
func (a *Agent) QualityComparison() []ProviderComparison {
	if a.qualityScorer == nil {
		return nil
	}
	return a.qualityScorer.Compare()
}

// QualityReport returns a human-readable provider/model comparison summary.
func (a *Agent) QualityReport() string {
	if a.qualityScorer == nil {
		return "Quality scoring not available."
	}
	return a.qualityScorer.FormatComparison()
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

// SetToolProgressCallback sets a callback invoked when a running tool emits
// intermediate output (e.g. wait_command streaming). The callback receives
// toolID, toolName, and output text. May be nil.
func (a *Agent) SetToolProgressCallback(fn func(toolID, toolName, output string)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onToolProgress = fn
}

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
	p, ok := a.provider.(provider.ReasoningEffortProvider)
	if !ok {
		a.mu.Unlock()
		return false
	}
	p.SetReasoningEffort(effort)
	// Mark that the user has explicitly set effort — adaptive effort stays dormant.
	if a.effortAdapter != nil {
		a.effortAdapter.setUserOverride(effort != "")
	}
	a.mu.Unlock()
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

// SetToolChoice controls whether the model uses tools: "auto" (model decides),
// "required" (force tool use), "none" (disable tools). Returns false if the
// provider does not support tool_choice.
func (a *Agent) SetToolChoice(choice string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.provider.(provider.ToolChoiceProvider)
	if !ok {
		return false
	}
	p.SetToolChoice(choice)
	return true
}

func (a *Agent) ToolChoice() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.provider.(provider.ToolChoiceProvider)
	if !ok {
		return ""
	}
	return p.ToolChoice()
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

// SetMCPRuntime wires the MCP runtime for ecosystem intelligence.
// This enables the agent to detect MCP server health issues, tool name
// conflicts, and capability gaps at session start.
func (a *Agent) SetMCPRuntime(rt tool.MCPRuntime) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mcpRuntime = rt
}

// CheckpointManager returns the checkpoint manager.
func (a *Agent) CheckpointManager() *checkpoint.Manager {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.checkpoints
}

// LastRunStats returns stats from the most recent RunStreamWithContent call.
// Returns nil if no run has completed yet. The pointer is safe to read but
// must not be mutated by callers.
func (a *Agent) LastRunStats() *RunStats {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastRunStats
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

// CodeIndexManager returns the code index manager if one is set.
func (a *Agent) CodeIndexManager() *tool.CodeIndexManager {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.codeIndex
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

// SetToolCallBudget sets the maximum total tool calls allowed for a single
// agent run. 0 disables explicit enforcement (auto-derivation from maxIter
// may still apply).
func (a *Agent) SetToolCallBudget(budget int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolCallBudget.SetBudget(budget)
}

// SetSessionTimeout sets the maximum wall-clock duration for a single agent run.
// A value of 0 disables the timeout (interactive default). In autopilot mode,
// a default timeout is applied when this is 0.
func (a *Agent) SetSessionTimeout(timeout time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionTimeout.timeout = timeout
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
	// Initialize guidance promoter now that workingDir and sessionID are both known.
	a.guidancePromoter = NewGuidancePromoter(a.workingDir, id)
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

// userPromptForStatsSafe extracts text from content blocks for journaling.
func userPromptForStatsSafe(content []provider.ContentBlock) string {
	var sb strings.Builder
	for _, b := range content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
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

	// Write run-start journal entry for crash detection. If the process dies
	// before the defer below runs, CheckCrashedRun() on next startup will detect
	// the stale "running" entry and alert the user.
	a.mu.RLock()
	sid := a.sessionID
	a.mu.RUnlock()
	MarkRunning(sid, userPromptForStatsSafe(content), os.Getpid())

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
	a.fulfillmentGate.reset()
	a.ambiguityPoint.reset()
	a.planDrift.reset()
	a.unverifiedClaim.reset()
	a.companionGuard.reset()
	a.specGaming.reset()
	a.scopeNarrow.reset()
	a.complexityGate.reset()
	a.behaviorPattern.reset()
	a.crossDetectorConsensus.reset()
	a.taintInfluence.reset()
	a.perfBaseline.reset()
	a.argSizeGuardFires = 0
	a.redundantRead.reset()
	a.searchParamGuard.reset()
	a.toolRedundancy.reset()
	a.toolSequence.reset()
	a.shellNativeHint.reset()
	a.monorepoScoper.reset()
	a.mcpEcosystem.reset()
	a.resetBgOrphan()
	a.actionAnnihil.reset()
	a.temporalBlindness.reset()
	a.resetWastedExplore()
	a.resetSelfMod()
	if a.delegationOrch != nil {
		a.delegationOrch.resetForNewTurn()
	}
	if a.effortAdapter != nil {
		a.effortAdapter.reset()
	}
	if a.adaptiveSampling != nil {
		a.adaptiveSampling.reset()
	}
	if a.iterPressure != nil {
		a.iterPressure.reset(a.maxIter)
	}
	if a.momentumLoss != nil {
		a.momentumLoss.reset()
		a.diminishingEdit.reset()
		a.overcorrection.reset()
		a.prematureRefactor.reset()
		a.errorCompound.reset()
		a.correctionSpiral.reset()
		a.bareEditStreak.reset()
		a.strategyFixation.reset()
		a.errorRush.reset()
		if a.recklessExec != nil {
			a.recklessExec.reset()
		}
		if a.irrevGate != nil {
			a.irrevGate.reset()
		}
		if a.prematureSurrender != nil {
			a.prematureSurrender.reset()
			a.subgoalTrack.reset()
		}
		a.futileCycle.reset()
		a.toolResultRedundancy.reset()
		a.anchorErosion.reset()
		a.verifyDebt.reset()
		a.editPropagation.reset()
		a.successDeclare.reset()
		a.criteriaDrift.reset()
		a.reasonAction.reset()
		a.attemptBrief.reset()
	}

	defer func() {
		// Mark the run as completed in the journal (crash detection cleanup).
		// This runs for all exit paths: success, error, and cancellation.
		MarkCompleted(sid, err == nil, runStats.Iterations, len(runStats.FilesEdited))

		runStats.finalize(err)
		a.mu.Lock()
		a.lastRunStats = runStats
		a.mu.Unlock()
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
		// Record completed run stats for cross-run behavioral pattern detection.
		// Runs even on cancellation since partial work is still observable behavior.
		if a.behaviorPattern != nil {
			a.behaviorPattern.recordRun(runStats)
		}
		// Post-run trajectory intelligence extraction (arXiv:2603.10600).
		// Extracts strategy/recovery/optimization learnings from the
		// completed run and persists them for future improvement.
		if a.trajIntel != nil {
			a.trajIntel.maybeExtractAndPersist(a.WorkingDir(), runStats)
		}
		// Record run metrics for cross-session regression detection.
		recordPerfBaseline(a.WorkingDir(), runStats)
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

	// Record explicit constraints from user message for amnesia detection.
	a.constraintAmnesia.recordConstraints(userText, 1)
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

		// Cross-session guidance promotion: persist recurrence data so that
		// frequently recurring guidance tags can be promoted to proactive
		// reminders in future sessions (inter-test-time evolution).
		a.guidancePromoter.RunEndHook()
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
	a.maybeInjectCorrectionFeedback()
	a.maybeInjectSentimentFeedback(userPromptForStats)

	// Ambiguity point detector: check the user's request for phrases with
	// multiple valid interpretations. If detected, inject guidance to clarify
	// before starting work. Zero-LLM-cost heuristic. Research: arXiv:2603.17150
	if ambMsg := a.checkAmbiguityPoints(userPromptForStats); ambMsg != "" {
		debug.Log("agent", "ambiguity point detector: injecting disambiguation guidance")
		a.contextManager.Add(provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: ambMsg,
			}},
		})
	}

	a.maybeInjectBehaviorPattern()
	a.maybeInjectPerfRegression()
	a.maybeInjectDynamicSystemPrompt()
	a.maybeInjectRatchetRules()
	a.maybeCheckPromptOps()

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
	todoCheckCount := 0

	a.autopilotStrategistCount = 0
	a.strategistBudgetAnnounced = false
	a.strategistNoProgressCount = 0

	// Reset monitoring systems once at run start, NOT inside the iteration
	// loop. These systems accumulate state across iterations within a run.
	a.resetOverseer()
	a.resetPlanner()
	a.resetTodoStaleness()
	a.resetScopeDrift()
	a.resetDriftRecurrence()
	a.resetLastGoodCheckpoint()
	a.recurringError.reset()
	a.errStrategyLoop.reset()
	a.fixCascade.reset()
	a.errRegression.reset()
	a.stalledConvergence.reset()
	a.speculator.resetSequence()
	a.toolMemo.reset()
	a.commandCache.reset()
	a.confidence.reset()
	a.verifDebt.reset()
	a.editAbandon.reset()
	a.costBudget.reset()
	a.toolCallBudget.reset()
	a.toolCallBudget.SetDefaultBudget(deriveDefaultBudget(a.maxIter))
	a.emptySearch.reset()
	// Reset the unread-file edit tracker so each run starts fresh.
	a.unreadEdit.reset()
	a.expiredRead.reset()
	a.falsePremise.reset()
	// Reset the edit failure recovery tracker.
	a.editFailRecovery.reset()
	// Reset the export guard so each run starts with a clean checked set.
	a.exportGuard.reset()
	a.hubPackageGuard.reset()
	a.artifactGuard.reset()
	a.branchGuard.reset()
	a.destructiveGuard.reset()
	a.fulfillmentGate.reset()
	a.ambiguityPoint.reset()
	a.planDrift.reset()
	a.unverifiedClaim.reset()
	a.companionGuard.reset()
	a.specGaming.reset()
	a.scopeNarrow.reset()
	a.complexityGate.reset()
	a.verifyRegression.reset()
	a.resetSelfCorrectionGate()

	a.argSizeGuardFires = 0
	a.redundantRead.reset()
	a.searchParamGuard.reset()
	a.toolRedundancy.reset()
	a.toolSequence.reset()
	a.taskAnchor.reset(userPromptForStats, time.Now())
	a.toolFilter = tool.NewRelevanceFilter()
	a.resetTransientRetryBudget()
	a.compoundingFailure.reset()
	a.toolDiversity.reset()
	a.fileChurn.reset()
	a.editOscillation.reset()
	a.analysisParalysis.reset()
	a.overReflection.reset()
	a.toolCallEconomy.reset()
	a.silentError.reset()
	a.verifySuppress.reset()
	a.verbosityDrift.reset()
	a.cusumDrift.reset()
	a.toolOveruse.reset()
	a.assumptionTracker.reset()
	a.sycophancyGuard.reset()
	a.unverifiedConfidence.reset()
	a.evidenceOverconfidence.reset()
	a.verifyDisconnect.reset()
	a.toolStorm.reset()
	a.serialRead.reset()
	a.reasoningRedund.reset()
	a.selectiveEvidence.reset()
	a.deferredWork.reset()
	a.circularReasoning.reset()
	a.selfDiagState.reset()
	a.contradiction.reset()
	a.actionHedging.reset()
	a.scopeCreep.reset()
	a.prematureAbstr.reset()
	a.capBoundary.reset()
	a.planAbandon.reset()
	a.compoundedUncert.reset()
	a.trajectoryHealth.reset()
	a.mindlessAction.reset()
	a.ungroundedReflect.reset()
	a.strategyStagnation.reset()
	a.infoScent.reset()
	a.causalAttribution.reset()
	a.reversibility.reset()
	a.constraintAmnesia.reset()
	a.constraintViolation.reset()
	a.symbolGrounding.reset()
	a.inputUnderspec.reset()
	a.tunnelVision.reset()
	a.queryConverge.reset()
	a.prematureCommit.reset()
	a.diagnosticDisconnect.reset()
	a.failureMode.reset()
	a.toolFallback.reset()
	a.errorCascade.reset()
	a.fileFreshness.reset()
	a.readHash.reset()
	a.toolThermal.reset()
	a.contextFootprint.reset()
	a.cacheEffMonitor.reset()
	a.pressureForecaster.reset()
	a.promptOps.reset()

	// Capture the git working tree state BEFORE the agent makes any changes.
	// This lets the reconciliation gate distinguish pre-existing dirty files
	// (user's own uncommitted work) from genuine side-effect changes introduced
	// by the agent's tool calls. Also resets the gate for the new run.
	a.changeReconcile.reset()
	a.claimVerify.reset()
	a.crossFileImpact.reset()
	a.diffSummary.reset()
	a.commitHint.reset()
	if workingDir := a.WorkingDir(); workingDir != "" {
		a.changeReconcile.capturePreRunState(workingDir)
		// Inject awareness if the tree is dirty — the agent should know about
		// pre-existing uncommitted changes so it can avoid accidentally
		// staging or committing them.
		if n := a.changeReconcile.dirtyFileCount(); n > 0 {
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf(
						"[workspace] Note: %d file(s) have uncommitted changes in the working tree "+
							"(your own work before this session). When committing, stage only the files "+
							"you modified during this task — do not use 'git add -A' or 'git commit -a' "+
							"unless the user explicitly asks.",
						n,
					),
				}},
			})
		}
	}

	// Check disk space on the workspace volume. If critically low, inject an
	// advisory so the agent can prioritize cleanup before file operations fail.
	// Zero-LLM-cost, fires at most once per run.
	a.diskSpace.reset()
	if workingDir := a.WorkingDir(); workingDir != "" {
		if diskMsg := a.diskSpace.check(workingDir); diskMsg != "" {
			debug.Log("disk-space", "low disk space detected, injecting advisory")
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: diskMsg}},
			})
		}
	}

	// Check for env var drift: if .env.example exists but vars are missing
	// from the local .env or shell environment, inject an advisory so the
	// agent knows commands may fail. Zero-LLM-cost, fires at most once per run.
	a.envDrift.reset()
	if workingDir := a.WorkingDir(); workingDir != "" {
		// Detect monorepo structure for package-scoped intelligence.
		a.monorepoScoper.detectMonorepo(workingDir)
		if envMsg := a.envDrift.check(workingDir); envMsg != "" {
			debug.Log("env-drift", "env var drift detected, injecting advisory")
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: envMsg}},
			})
		}
	}

	// Mark a new run boundary in the checkpoint manager so UndoRun() can
	// batch-revert all file changes from this run in one operation.
	if a.checkpoints != nil {
		a.checkpoints.StartRun(runStats.RunID())
	}

	// Start the session wall-clock timeout timer.
	a.sessionTimeout.start(a.currentMode() == permission.AutopilotMode)

	// Input underspecification detection: if the user's initial request is
	// vague/underspecified (no concrete identifiers, short, vague verbs),
	// inject an advisory before the agent starts exploring. Zero-LLM-cost,
	// fires at most once per run. Based on Ambig-SWE (arXiv 2502.13069).
	if underspecHint := a.maybeWarnInputUnderspec(userText); underspecHint != "" {
		debug.Log("input-underspec", "underspecified user request detected, injecting advisory")
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: underspecHint}},
		})
	}

	// Sycophancy detection: capture candidate factual premises from the user
	// message so the agent's response can be checked for unverified agreement.
	a.sycophancyGuard.captureUserPremises(userText)

	// Cross-session guidance promotion: inject proactive reminders for tags
	// that have recurred across 3+ past sessions (inter-test-time evolution).
	if reminder := a.guidancePromoter.RunStartHook(); reminder != "" {
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: reminder}},
		})
	}
	for i := 0; a.maxIter <= 0 || i < a.maxIter; i++ {
		runStats.Iterations = i + 1
		if err := ctx.Err(); err != nil {
			return err
		}
		// Check session wall-clock timeout: emit user-visible notifications or stop.
		// Timeout messages are infrastructure notifications for the user only;
		// they are NOT injected into LLM context to avoid distracting the model.
		if msg := a.sessionTimeout.check(); msg != "" {
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventSystem,
				Text: msg,
			})
			if a.sessionTimeout.shouldStop() {
				debug.Log("session-timeout", "wall-clock timeout exceeded, stopping agent loop")
				break
			}
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

		// Task re-anchoring: prevent context collapse on long repair chains.
		// After 8-10+ tool calls, models lose track of the original task
		// (AgentMarketCap 2026 survey, arXiv:2603.07670). Re-inject a compact
		// reminder periodically based on cumulative tool call count — not
		// iteration count, since parallel tools accelerate collapse.
		if anchorMsg := a.taskAnchor.maybeReanchorTask(
			runStats.totalToolCalls(), i+1, runStats.FilesEdited,
		); anchorMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: anchorMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// File freshness sentinel: proactively detect externally modified files
		// (IDE save, formatter, git pull, another agent). Injects a notification
		// BEFORE the agent uses stale content, not reactively at edit time.
		if staleMsg := a.fileFreshness.maybeCheckStaleFiles(i + 1); staleMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: staleMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Tool thermal profile: detect imbalanced tool-call distribution
		// (e.g., 90% reads with no edits = agent is spinning). Zero-LLM-cost
		// heuristic based on cross-tool category analysis.
		if thermalMsg := a.toolThermal.maybeWarn(i); thermalMsg != "" {
			debug.Log("thermal-profile", "imbalanced tool usage detected at iteration %d: %s", i+1, a.toolThermal.categoryBreakdown())
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: thermalMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Momentum loss detection: track per-iteration productivity and
		// warn when late-phase productivity collapses after early progress.
		a.momentumLoss.startIteration(i + 1)
		if momentumMsg := a.momentumLoss.checkMomentumLoss(a.maxIter); momentumMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: momentumMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Error compounding risk: compute geometric compounding probability
		// and warn when accumulated errors make the trajectory unreliable.
		if ecMsg := a.errorCompound.maybeWarn(i + 1); ecMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: ecMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Correction spiral: detect error severity escalation across fix attempts.
		// Warns when each correction introduces a worse error (feedback control instability).
		if csMsg := a.correctionSpiral.maybeWarn(i + 1); csMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: csMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Verification debt: warn when source edits accumulate without a
		// successful build. Prevents last-mile failure from compounding
		// unverified changes (arXiv:2602.16666).
		if vdMsg := a.verifyDebt.maybeWarn(i + 1); vdMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: vdMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Cross-file edit propagation risk: warn when many DISTINCT files
		// are edited without verification. Cross-file dependency chains
		// create error propagation paths (MAST taxonomy, Cemri et al. 2025).
		if epMsg := a.editPropagation.maybeWarn(i + 1); epMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: epMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Premature success declaration: if the agent claimed completion in a
		// prior iteration but has since continued making tool calls, flag the
		// metacognitive calibration gap.
		if sgMsg := a.subgoalTrack.maybeWarn(i + 1); sgMsg != "" {
			debug.Log("agent", "Iteration %d: subgoal completion gap detected", i+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: sgMsg}},
			})
			msgs = a.contextManager.Messages()
		}
		if sdMsg := a.successDeclare.maybeWarn(i + 1); sdMsg != "" {
			debug.Log("agent", "Iteration %d: premature success declaration detected", i+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: sdMsg}},
			})
			msgs = a.contextManager.Messages()
		}
		if cdMsg := a.criteriaDrift.maybeWarn(i + 1); cdMsg != "" {
			debug.Log("agent", "Iteration %d: success criteria drift detected", i+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: cdMsg}},
			})
			msgs = a.contextManager.Messages()
		}
		// Attempt brief: compact summary of failed approaches to prevent
		// repeating the same dead-end strategy.
		if abMsg := a.attemptBrief.maybeBrief(i + 1); abMsg != "" {
			debug.Log("agent", "Iteration %d: injecting attempt brief", i+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: abMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Wasted exploration detection: nudge the agent when previous
		// search results containing file paths were never acted upon.
		if wastedExploreMsg := a.maybeWarnWastedExplore(i + 1); wastedExploreMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: wastedExploreMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Information scent decay detection: nudge when consecutive
		// exploration calls yield diminishing novel information.
		if scentMsg := a.infoScent.maybeWarn(i + 1); scentMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: scentMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Orphaned background command detection: nudge the agent to check
		// output of background commands (start_command) that haven't been
		// read for several iterations.
		// Query convergence failure: detect repeated similar search queries
		// across iterations without progressing to code action.
		if qcMsg := a.queryConverge.maybeWarn(i + 1); qcMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: qcMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		if bgOrphanMsg := a.maybeWarnBgOrphan(i + 1); bgOrphanMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: bgOrphanMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Tool call storm detection: when diverse tools are fired in
		// rapid succession without interleaved reasoning, inject a
		// guidance nudge to pause and synthesize (test-time scaling).
		if stormMsg := a.toolStorm.maybeWarn(); stormMsg != "" {
			debug.Log("agent", "Iteration %d: tool call storm detected", i+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: stormMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Reasoning redundancy detection: consecutive text-only iterations with
		// near-duplicate content indicate overthinking (arXiv:2503.16419).
		// Nudge the agent to stop deliberating and act.
		if rrMsg := a.reasoningRedund.maybeWarn(i+1, a.maxIter); rrMsg != "" {
			debug.Log("reasoning-redund", "Iteration %d: reasoning redundancy detected -- consecutive text-only overthinking", i+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: rrMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Iteration pressure degradation: detect verify/edit ratio drop
		// near the iteration budget limit (metacognitive monitoring).
		if ipMsg := a.maybeWarnIterPressure(i + 1); ipMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: ipMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Unverified mutation streak: detect consecutive edits without any
		// verification (build/test/run) to encourage tight feedback loops.
		if bsMsg := a.bareEditStreak.maybeWarn(i + 1); bsMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: bsMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Strategy fixation: detect when the agent has edited the same file
		// multiple times with intervening failed verifications, suggesting an
		// approach-level failure (PARC arXiv:2512.03549).
		if sfMsg := a.strategyFixation.check(); sfMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: sfMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Error rush: detect panic coding -- blind-fixing after consecutive
		// errors without diagnostic reads in between (Agentic Overconfidence,
		// arXiv 2026; AgentDiet, FSE 2026).
		if erMsg := a.errorRush.check(); erMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: erMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Target scatter: detect world model miscalibration -- the agent
		// examines many unrelated files without converging, indicating it
		// does not know where to look (Qwen-AgentWorld 2026; SICA NeurIPS 2025).
		if tsMsg := a.targetScatter.check(); tsMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: tsMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Attention fragmentation: detect rapid directory context-switching
		// that creates extraneous cognitive load (CLT for LLM agents,
		// arXiv:2506.06843). High switch density means the model is thrashing
		// between unrelated concerns instead of maintaining coherent focus.
		if afMsg := a.attentionFragment.analyze(); afMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: afMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Futile cycle: detect when the agent re-reads the same set of files
		// that it explored earlier without making any edits in between.
		if fcMsg := a.futileCycle.maybeWarn(i + 1); fcMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: fcMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Constraint amnesia: remind the agent of user-specified constraints
		// that may have scrolled out of effective attention after many iterations.
		// Catastrophic forgetting in token space (Letta/MemGPT 2025).
		if caMsg := a.constraintAmnesia.maybeWarn(i + 1); caMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: caMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Diagnostic-action disconnect detection: when the agent has received
		// diagnostic content (errors, undefined symbols) but subsequent actions
		// don't address it, inject guidance to refocus on the known issue.
		if ddMsg := a.diagnosticDisconnect.check(); ddMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: ddMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// Delegation orchestration intelligence: detect orphaned delegations
		// (spawned agents whose results were never consumed), serial delegation
		// anti-pattern (should batch parallelizable tasks), and over-delegation
		// (excessive delegation ratio). Zero-LLM-cost deterministic heuristics.
		if a.delegationOrch != nil {
			if delOrchMsg := a.delegationOrch.maybeWarnOrphanedDelegations(i + 1); delOrchMsg != "" {
				debug.Log("agent", "Iteration %d: delegation orphan gate injected guidance", i+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: delOrchMsg}},
				})
				msgs = a.contextManager.Messages()
			}
			if serialMsg := a.delegationOrch.maybeWarnSerialDelegation(); serialMsg != "" {
				debug.Log("agent", "Iteration %d: serial delegation gate injected guidance", i+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: serialMsg}},
				})
				msgs = a.contextManager.Messages()
			}
			if overDelMsg := a.delegationOrch.maybeWarnOverDelegation(); overDelMsg != "" {
				debug.Log("agent", "Iteration %d: over-delegation gate injected guidance", i+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: overDelMsg}},
				})
				msgs = a.contextManager.Messages()
			}
		}

		// Monorepo scope sprawl detection: if the agent is editing across many
		// packages in a monorepo without apparent cross-package intent, inject
		// a one-time hint to confirm scope and consider package-scoped ops.
		if monorepoMsg := a.monorepoScoper.maybeWarnScopeSprawl(); monorepoMsg != "" {
			debug.Log("monorepo-scope", "package scope sprawl detected: %s", monorepoMsg)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: monorepoMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		// MCP ecosystem intelligence: one-time check at session start for
		// failed servers, tool name conflicts, empty servers, and auth issues.
		if mcpMsg := a.maybeWarnMCP(i + 1); mcpMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: mcpMsg}},
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

		// Adaptive effort: adjust reasoning budget per-turn based on recent
		// tool complexity. Only activates when user hasn't explicitly set effort.
		effortApplied, effortPrev := a.applyAdaptiveEffort()

		// Adaptive sampling: DISABLED. Some models (e.g. Kimi k3-256k) reject
		// any temperature value other than 1, causing 400 errors. The benefit
		// of micro-adjusting temperature per task phase does not justify the
		// risk of breaking model compatibility. Temperature is left at the
		// provider default unless the user explicitly sets it.
		var samplingApplied float64 = -1
		var samplingPrev float64 = 0
		_ = samplingApplied
		_ = samplingPrev

		// Dynamic tool pruning: filter out low-relevance MCP tools to reduce
		// context overhead and improve tool-selection accuracy. Only activates
		// when total tool count exceeds a threshold. Pass context pressure so
		// tool descriptions are trimmed more aggressively as context fills up.
		var ctxPressure float64
		if cw := a.contextManager.ContextWindow(); cw > 0 {
			ctxPressure = float64(a.contextManager.TokenCount()) / float64(cw)
		}
		activeToolDefs := a.toolFilter.FilterWithPressure(toolDefs, tool.ExtractContextFromMessages(msgs, 6), ctxPressure)

		resp, textBuf, toolCalls, truncated, err := a.streamChatResponse(ctx, a.ensureMessagesSendable(msgs), activeToolDefs, onEvent)
		if samplingApplied >= 0 {
			a.restoreSampling(samplingPrev)
		}
		if effortApplied != "" {
			a.restoreEffort(effortPrev)
		}
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

		// Context Engineering: monitor cache efficiency and forecast context
		// window pressure after each LLM call. Both are zero-LLM-cost
		// deterministic analysis. Guidance is injected into the context
		// manager as a low-priority system note.
		if cacheGuidance := a.cacheEffMonitor.record(resp.Usage); cacheGuidance != "" {
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: cacheGuidance,
				}},
			})
			debug.Log("cache-efficiency", "injecting cache bust storm guidance at iteration %d", i+1)
		}
		// Sync context window size from the context manager for forecasting.
		if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
			a.pressureForecaster.setContextWindow(cm.ContextWindow())
		}
		if pressureGuidance := a.pressureForecaster.record(i+1, resp.Usage); pressureGuidance != "" {
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: pressureGuidance,
				}},
			})
			debug.Log("context-pressure", "injecting pressure forecast guidance at iteration %d", i+1)
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
			a.toolStorm.recordReasoning(assistantText)
			a.constraintViolation.recordReasoning(assistantText, i+1)
			a.reasoningRedund.recordReasoning(assistantText, false)

			// Over-reflection detection: check if the agent is producing
			// text-heavy turns without tool calls (wasted test-time compute).
			// arXiv:2506.12928 -- "Knowing when to reflect is important".
			if orHint := a.maybeWarnOverReflection(assistantText, len(toolCalls) > 0, i+1); orHint != "" {
				debug.Log("over-reflection", "Iteration %d: over-reflection detected", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: orHint,
					}},
				})
			}

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

			// Assumption tracker: scan assistant text for implicit unverified
			// assumption language ("I assume", "probably", etc.). If threshold
			// is exceeded, inject guidance to verify before proceeding.
			if assumptionHint := a.maybeWarnAssumptions(assistantText); assumptionHint != "" {
				debug.Log("agent", "Iteration %d: assumption tracker detected implicit assumptions", i+1)
				a.recordUncertainty("assumption", weightAssumption)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: assumptionHint,
					}},
				})
			}

			// Sycophancy detector: detect when the agent agrees with a
			// user-stated premise without independent verification.
			if syncHint := a.sycophancyGuard.checkSycophancy(assistantText); syncHint != "" {
				debug.Log("agent", "Iteration %d: sycophancy guard detected unverified agreement with user premise", i+1)
				a.recordUncertainty("sycophancy", weightAssumption)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: syncHint,
					}},
				})
			}

			// Premature surrender detection: scan assistant text for give-up
			// language ("this isn't possible", "I can't do this", etc.) and
			// push the agent to try alternative strategies before abandoning.
			// arXiv:2506.05109 -- intrinsic metacognitive awareness.
			if surrenderMsg := a.prematureSurrender.checkSurrender(assistantText, i+1, a.maxIter); surrenderMsg != "" {
				debug.Log("surrender-detect", "Iteration %d: premature surrender language detected", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: surrenderMsg,
					}},
				})
			}

			// False premise detection: scan assistant text for success claims
			// that contradict recent tool error results (world-model drift).
			if fpMsg := a.falsePremise.checkFalsePremise(assistantText); fpMsg != "" {
				debug.Log("agent", "Iteration %d: false premise detected (ungrounded success claim)", i+1)
				a.recordUncertainty("false_premise", weightFalsePremise)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: fpMsg,
					}},
				})
			}

			// Unverified confidence detector: scan for overconfident completion
			// claims ("this definitely works", "fix is complete") that aren't
			// backed by actual verification (build/test/lint). EpiCaR-inspired
			// calibration gap detection.
			if confHint := a.maybeWarnUnverifiedConfidence(assistantText); confHint != "" {
				debug.Log("agent", "Iteration %d: unverified confidence detector found overconfident claims without verification", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: confHint,
					}},
				})
			}

			// Evidence-induced overconfidence: detect definitive claims or code
			// edits derived from evidence tools (web_search, grep, read) without
			// cross-verification. Tool-type calibration asymmetry (arXiv:2601.15778).
			if evidHint := a.maybeWarnEvidenceOverconfidence(assistantText); evidHint != "" {
				debug.Log("agent", "Iteration %d: evidence overconfidence detector found unverified evidence-derived certainty", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: evidHint,
					}},
				})
			}

			// Verification outcome disconnect: detect verification failures
			// that the agent advances past without addressing. Behavioral
			// overconfidence gap (arXiv:2508.06225).
			if vdHint := a.maybeWarnVerifyDisconnect(assistantText, i+1); vdHint != "" {
				debug.Log("agent", "Iteration %d: verification disconnect detector found unresolved failure", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: vdHint,
					}},
				})
			}

			// Phantom verification: detect category-specific verification claims
			// ("tests pass", "build compiles") without a matching verification
			// command in the trajectory. Process supervision gap (AgentPro, EMNLP 2025).
			if pvHint := a.maybeWarnPhantomVerify(assistantText); pvHint != "" {
				debug.Log("agent", "Iteration %d: phantom verification detector found unverified category claims", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: pvHint,
					}},
				})
			}
			a.successDeclare.recordAssistantText(assistantText, i)
			a.criteriaDrift.recordAssistantText(assistantText, i)
			a.subgoalTrack.recordAssistantText(assistantText, i)

			// Symbol grounding verifier: detect code symbols mentioned in
			// assistant text that were never found via tool calls.
			if groundingHint := a.maybeWarnGrounding(assistantText, i); groundingHint != "" {
				debug.Log("agent", "Iteration %d: symbol grounding verifier detected ungrounded symbol references", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: groundingHint,
					}},
				})
			}

			// Selective evidence detector: detect confirmation bias pattern where
			// the agent emphasizes positive evidence while dismissing negatives.
			if biasHint := a.maybeWarnSelectiveEvidence(assistantText); biasHint != "" {
				debug.Log("agent", "Iteration %d: selective evidence detector detected confirmation bias", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: biasHint,
					}},
				})
			}

			// Temporal blindness: detect when agent claims a verification
			// result is still valid after mutations invalidated it.
			if tbHint := a.temporalBlindness.maybeWarnTemporalBlindness(assistantText); tbHint != "" {
				debug.Log("agent", "Iteration %d: temporal blindness detector triggered (%d mutations since last verification)", i+1, a.temporalBlindness.mutationCount)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: tbHint,
					}},
				})
			}

			// Unverified self-diagnosis: detect definitive diagnosis claims
			// about recent errors without verification (correlated failure).
			if diagHint := a.maybeWarnSelfDiagnosis(assistantText, i+1); diagHint != "" {
				debug.Log("agent", "Iteration %d: unverified self-diagnosis detector found correlated failure risk", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: diagHint,
					}},
				})
			}

			// Deferred work tracker: detect when the agent defers work to
			// "later" or "next" but never circles back. If stale deferrals
			// accumulate or the agent declares completion with open items,
			// inject guidance to address them.
			if deferredHint := a.maybeWarnDeferredWork(assistantText, i); deferredHint != "" {
				debug.Log("agent", "Iteration %d: deferred work tracker detected unaddressed deferrals", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: deferredHint,
					}},
				})
			}

			// Circular reasoning detector: scan assistant text for tautological
			// or circular justification patterns. When 2+ instances accumulate,
			// inject guidance to provide concrete evidence instead.
			if circularHint := a.maybeWarnCircularReasoning(assistantText, i); circularHint != "" {
				debug.Log("agent", "Iteration %d: circular reasoning detector triggered (%d instances)", i+1, len(a.circularReasoning.instances))
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: circularHint,
					}},
				})
			}

			// Cross-turn contradiction detector: tracks root-cause/location
			// claims across iterations. When the agent contradicts its own prior
			// claim about where the bug/issue is, injects guidance to reconcile.
			if contradictionHint := a.maybeWarnContradiction(assistantText, i); contradictionHint != "" {
				debug.Log("agent", "Iteration %d: cross-turn contradiction detector triggered (%d contradictions)", i+1, len(a.contradiction.contradictions))
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: contradictionHint,
					}},
				})
			}

			// Scope creep detector: scan assistant text for language indicating
			// unsolicited expansion beyond the user's request ("while I'm at it",
			// "I've gone ahead and also fixed...", etc.). If threshold is exceeded,
			// inject guidance to stay within scope.
			if scopeHint := a.maybeWarnScopeCreep(assistantText); scopeHint != "" {
				debug.Log("agent", "Iteration %d: scope creep detector detected unsolicited expansion", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: scopeHint,
					}},
				})
			}

			// Premature abstraction detector: scan assistant text for language
			// indicating over-engineering within the task scope (factory patterns,
			// interface hierarchies with single implementations, config systems).
			if abstrHint := a.maybeWarnPrematureAbstraction(assistantText); abstrHint != "" {
				debug.Log("agent", "Iteration %d: premature abstraction detector detected over-engineering", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: abstrHint,
					}},
				})
			}

			// Capability boundary detector: tracks repeated approach pivots
			// after failures. If the agent has tried 3+ distinct strategies that
			// all failed, inject guidance to escalate to user or reconsider.
			if capHint := a.maybeWarnCapabilityBoundary(assistantText); capHint != "" {
				debug.Log("agent", "Iteration %d: capability boundary detector detected stubborn persistence", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: capHint,
					}},
				})
			}

			// Plan abandonment detector: tracks multi-step plans declared by
			// the agent across iterations. If a completion claim appears after
			// 3+ plan steps were declared in a prior turn, inject guidance to
			// verify all steps were actually executed. Prevents half-finished
			// work from being shipped as "done".
			if planHint := a.maybeWarnPlanAbandon(assistantText); planHint != "" {
				debug.Log("agent", "Iteration %d: plan abandonment detector triggered (declared %d steps)", i+1, len(a.planAbandon.declaredSteps))
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: planHint,
					}},
				})
			}

			// Tool-target mismatch detector: compares the agent's stated intent
			// ("I'll read X") against the actual tool call target. When they
			// diverge, inject guidance to verify the correct target.
			if ttHint := a.maybeWarnToolTargetMismatch(assistantText, toolCalls); ttHint != "" {
				debug.Log("agent", "Iteration %d: tool-target mismatch detector triggered", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: ttHint,
					}},
				})
			}

			// Outcome misattribution detector: checks whether the agent
			// claims success ("done", "fixed", "works") in its narrative
			// despite a failure indicator in the preceding tool result.
			if omHint := a.outcomeMisattrib.checkMisattribution(assistantText, i+1); omHint != "" {
				debug.Log("agent", "Iteration %d: outcome misattribution detector triggered", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: omHint,
					}},
				})
			}

			// Reasoning-action alignment verifier: checks whether the cognitive
			// category of the agent's stated reasoning matches the cognitive
			// category of its actual tool calls. Metacognition-driven LLM
			// frameworks (SAGE Journals 2025) show this alignment is a core
			// predictor of agent success.
			if raHint := a.maybeWarnReasonAction(assistantText, toolCalls); raHint != "" {
				debug.Log("agent", "Iteration %d: reasoning-action alignment verifier triggered", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: raHint,
					}},
				})
			}

			// Mindless action detector: tracks consecutive tool-call steps
			// with minimal reasoning text. When 4+ consecutive mindless steps
			// occur, inject guidance to pause and reflect before continuing.
			if a.mindlessAction.recordStep(len(assistantText), len(toolCalls) > 0) {
				debug.Log("agent", "Iteration %d: mindless action detector triggered (streak=%d)", i+1, a.mindlessAction.streak)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: mindlessActionWarning(a.mindlessAction.streak),
					}},
				})
			}

			// Ungrounded reflection detector: detect consecutive iterations
			// with substantial text but no tool calls (overthinking loops).
			// Research (2024-2025) shows intrinsic self-correction without
			// external grounding degrades performance.
			if ugrMsg := a.ungroundedReflect.recordIteration(i+1, len(toolCalls) > 0, len(assistantText)); ugrMsg != "" {
				debug.Log("agent", "Iteration %d: ungrounded reflection detector triggered", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: ugrMsg,
					}},
				})
			}

			// Trajectory health synthesizer (metacognitive layer): record
			// per-iteration tool activity stats for composite health scoring.
			if a.trajectoryHealth != nil {
				eCnt, rCnt := countToolTypes(toolCalls)
				assCnt := len(scanAssumptions(assistantText))
				a.trajectoryHealth.recordIteration(eCnt, 0, len(toolCalls), rCnt, assCnt)
			}

			// Trajectory health warning: when composite score exceeds threshold,
			// inject holistic guidance about accumulating risk.
			if healthHint := a.maybeWarnTrajectoryHealth(); healthHint != "" {
				debug.Log("agent", "Iteration %d: trajectory health synthesizer detected composite degradation", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: healthHint,
					}},
				})
			}

			// Foresight calibration (WorldEvolver arXiv:2606.30639): record
			// the agent's predictions about upcoming tool outcomes BEFORE
			// execution. After execution, checkCalibration compares them
			// against actual results to detect prediction-observation gaps.
			a.foresightCalib.recordPrediction(assistantText, toolCalls, i+1)

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
			// Budget: 100 calls per Run. With ~5 tool iterations between each
			// strategist call, this covers ~500 tool operations — enough for
			// large-scale implementation tasks. For very large projects, the user
			// sends another message ("continue") to reset the budget.
			if a.currentMode() == permission.AutopilotMode && a.hasAutopilotGoal() && a.autopilotStrategistCount < maxAutopilotStrategistCalls {
				a.strategistNoProgressCount++
				a.autopilotStrategistCount++

				// Deadlock detection: if the agent has made NO tool calls for
				// several consecutive strategist rounds, the agent believes it's
				// done but the strategist keeps asking for verification. This is
				// a deadlock that wastes the entire 100-call budget. Force-terminate.
				if a.strategistNoProgressCount >= maxConsecutiveStrategistNoProgress {
					debug.Log("agent", "Iteration %d: autopilot force-terminate after %d consecutive no-progress rounds", i+1, a.strategistNoProgressCount)
					preview := textBuf
					if len(preview) > 200 {
						preview = preview[:200]
					}
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Autopilot: agent idle for %d consecutive rounds — terminating to avoid deadlock. Last output: %s]", a.strategistNoProgressCount, preview)})
					a.ClearAutopilotGoal()
					return nil
				}

				debug.Log("agent", "Iteration %d: autopilot calling strategist (call #%d/%d, no-progress=%d/%d)", i+1, a.autopilotStrategistCount, maxAutopilotStrategistCalls, a.strategistNoProgressCount, maxConsecutiveStrategistNoProgress)
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist #%d/%d: analyzing conversation and deciding next steps...] ", a.autopilotStrategistCount, maxAutopilotStrategistCalls)})

				result, sErr := a.runAutopilotStrategist(ctx, textBuf)
				if sErr != nil {
					debug.Log("agent", "autopilot strategist failed: %v", sErr)
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist unavailable (%v) — autopilot stopping]", sErr)})
					// Fall through to normal return — can't drive autonomously.
				} else if result.Complete {
					debug.Log("agent", "Iteration %d: strategist declared goal achieved", i+1)
					summary := result.Guidance
					// Strip the completion marker (possibly markdown-wrapped); the
					// rest is the strategist's summary of what was accomplished.
					if idx := strings.Index(strings.ToUpper(summary), strategistCompleteMarker); idx >= 0 {
						after := summary[idx+len(strategistCompleteMarker):]
						summary = strings.TrimSpace(after)
					}
					msg := "[Strategist: goal achieved — autopilot complete.]"
					if summary != "" {
						msg = fmt.Sprintf("[Strategist: goal achieved — autopilot complete. %s]", summary)
					}
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: msg})
					a.ClearAutopilotGoal()
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
					a.ClearAutopilotGoal()
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

				// Plan drift gate: before returning, check if plan items (from
				// exit_plan_mode) were actually addressed by the agent's work.
				// Zero-LLM-cost heuristic inspired by Kiro/GitHub Spec Kit.
				if driftMsg := a.planDrift.checkPlanDrift(runStats, textBuf); driftMsg != "" {
					debug.Log("agent", "Iteration %d: plan drift detected, injecting reminder", i+1)
					a.contextManager.Add(provider.Message{
						Role: "user",
						Content: []provider.ContentBlock{{
							Type: "text",
							Text: driftMsg,
						}},
					})
					continue
				}

				// Request fulfillment gate: before returning, verify that the
				// agent's actual work matches the user's request. This catches
				// silent partial completion when no todo list was created.
				// Zero-LLM-cost heuristic inspired by Claude Code/Cursor/Aider
				// completion verification patterns.
				if fulfillmentMsg := a.checkFulfillmentGate(userPromptForStats, runStats, textBuf); fulfillmentMsg != "" {
					debug.Log("agent", "Iteration %d: fulfillment gate detected gap, injecting reminder", i+1)
					a.contextManager.Add(provider.Message{
						Role: "user",
						Content: []provider.ContentBlock{{
							Type: "text",
							Text: fulfillmentMsg,
						}},
					})
					continue
				}

				// Unverified success claim detection: before returning, check if
				// the agent's response claims verification results ("tests pass",
				// "build succeeds") without having actually run verification
				// commands. Zero-LLM-cost heuristic.
				if claimMsg := a.checkUnverifiedClaim(textBuf, runStats); claimMsg != "" {
					debug.Log("agent", "Iteration %d: unverified success claim detected, injecting reminder", i+1)
					a.contextManager.Add(provider.Message{
						Role: "user",
						Content: []provider.ContentBlock{{
							Type: "text",
							Text: claimMsg,
						}},
					})
					continue
				}

				// Companion file guard: before returning, check if the agent
				// edited source files that have existing test companions but
				// did not update those tests. Zero-LLM-cost heuristic.
				if companionMsg := a.companionGuard.checkCompanionFiles(runStats, a.WorkingDir()); companionMsg != "" {
					debug.Log("agent", "Iteration %d: companion file guard detected unedited test companions", i+1)
					a.contextManager.Add(provider.Message{
						Role: "user",
						Content: []provider.ContentBlock{{
							Type: "text",
							Text: companionMsg,
						}},
					})
					continue
				}

				// Specification gaming detection: before returning, check if
				// the agent is gaming verification (editing tests instead of
				// source, adding skip markers, tampering with CI config) rather
				// than fixing the actual problem. Zero-LLM-cost heuristic.
				if specGamingMsg := a.checkSpecGaming(runStats, userPromptForStats); specGamingMsg != "" {
					debug.Log("agent", "Iteration %d: specification gaming detected, injecting warning", i+1)
					a.contextManager.Add(provider.Message{
						Role: "user",
						Content: []provider.ContentBlock{{
							Type: "text",
							Text: specGamingMsg,
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

			// Complexity quality gate: after build verification passes (or no
			// build was needed), check edited Go files for complexity hotspots.
			// This is an advisory warning — it doesn't block completion but
			// alerts the agent to refactor-worthy functions before finishing.
			if complexityMsg := a.checkComplexityGate(runStats); complexityMsg != "" {
				debug.Log("agent", "Iteration %d: complexity gate detected quality issues, injecting advisory", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: complexityMsg,
					}},
				})
				continue
			}

			// Cross-file impact analysis gate: detect removed/renamed exported symbols
			// that are referenced by sibling files the agent did NOT edit. This catches
			// breakage from function/type/method removal before the agent declares done.
			if impactMsg := a.checkCrossFileImpact(runStats); impactMsg != "" {
				debug.Log("agent", "Iteration %d: cross-file impact analysis detected potential breakage", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: impactMsg,
					}},
				})
				continue
			}

			// Change reconciliation gate: after all other gates pass, check
			// whether shell commands caused unexpected source file changes.
			// This catches side effects from tools like go mod tidy, code
			// generators, or format-on-save hooks.
			if reconcileMsg := a.checkChangeReconcile(runStats); reconcileMsg != "" {
				debug.Log("agent", "Iteration %d: change reconciliation detected unexpected files", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: reconcileMsg,
					}},
				})
				continue
			}

			// Diff summary self-review gate: inject a compact git diff --stat
			// summary so the agent can holistically review ALL its changes before
			// returning to the user. Fires once per run, only for multi-file edits.
			if diffSummaryMsg := a.checkDiffSummaryGate(runStats); diffSummaryMsg != "" {
				debug.Log("agent", "Iteration %d: diff summary gate injected self-review", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: diffSummaryMsg,
					}},
				})
				continue
			}

			// Post-completion commit hint: after all gates pass, remind the agent
			// to stage and commit its work if it hasn't already. This is the last
			// gate and is advisory (non-blocking) -- it does not force a continue.
			if commitHintMsg := a.checkCommitHintGate(runStats); commitHintMsg != "" {
				debug.Log("agent", "Iteration %d: commit hint gate injected reminder", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: commitHintMsg,
					}},
				})
				continue
			}

			debug.Log("agent", "Iteration %d: no tool calls, returning", i+1)
			return nil
		}

		debug.Log("agent", "Iteration %d: tool_calls=%d", i+1, len(toolCalls))

		a.contextManager.Add(resp.Message)

		// Action hedging detector: scan assistant text for verbalized
		// uncertainty ("hopefully this fixes", "let's try", "best guess")
		// when the iteration includes mutation tools. If threshold exceeded,
		// inject guidance to verify before proceeding with edits.
		hedgingHasMutation := false
		for _, tc := range toolCalls {
			if isMutationTool(tc.Name) {
				hedgingHasMutation = true
				break
			}
		}
		if hedgingHint := a.maybeWarnActionHedging(textBuf, hedgingHasMutation); hedgingHint != "" {
			debug.Log("agent", "Iteration %d: action hedging detector detected verbalized uncertainty during mutation", i+1)
			a.recordUncertainty("hedging", weightHedging)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: hedgingHint,
				}},
			})
		}

		// Compounded trajectory uncertainty check: after all per-turn epistemic
		// detectors have run, check if the accumulated trajectory-level
		// uncertainty has crossed the reliability threshold.
		if compHint := a.maybeWarnCompoundedUncertainty(); compHint != "" {
			debug.Log("agent", "Iteration %d: compounded trajectory uncertainty threshold crossed", i+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: compHint,
				}},
			})
		}

		// Spiral of Hallucination detection: track cross-turn epistemic
		// error propagation. Record this turn's uncertainty topics and
		// check for committed assertions on previously uncertain foundations.
		a.recordSpiralTurn(textBuf)
		if spiralHint := a.maybeWarnSpiralHallucination(); spiralHint != "" {
			debug.Log("agent", "Iteration %d: spiral of hallucination pattern detected", i+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: spiralHint,
				}},
			})
		}

		// Execute tool calls and build tool_result message
		// Reset the no-progress counter — the agent is making forward progress.
		a.strategistNoProgressCount = 0
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
			a.toolCallBudget.record()
			a.toolThermal.recordToolCall(tc.Name)
			a.iterPressure.recordToolCall(tc.Name, i+1)
			a.momentumLoss.recordToolCall(tc.Name)
			extractPathsFromToolCall(tc.Name, tc.Arguments, runStats)
			// Check for consecutive duplicate tool calls (loop detection).
			// If detected, inject a guidance message into the tool result.
			var loopGuidance string
			if guidance := a.loopDetectionInjection(tc); guidance != "" {
				loopGuidance = guidance
			}
			// Search parameter quality guard: detect overly broad/vague search
			// parameters BEFORE execution to prevent context flooding.
			var searchParamHint string
			if hint := a.searchParamGuard.checkParamQuality(tc.Name, tc.Arguments); hint != "" {
				searchParamHint = hint
			}
			// Pre-action reversibility assessment: evaluate high-stakes actions
			// BEFORE execution. Counterfactual Pre-Mortem Loops (Curve Labs 2026).
			if revGuidance := a.reversibility.checkPreAction(tc.Name, string(tc.Arguments)); revGuidance != "" {
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: revGuidance,
					}},
				})
				msgs = a.contextManager.Messages()
			}

			// Record safety signals for subsequent reversibility checks.
			a.reversibility.recordSafetySignal(tc.Name, string(tc.Arguments))

			// Tool call redundancy analyzer: detect scattered (non-consecutive)
			// duplicate calls to the same tool with identical arguments.
			var redundancyHint string
			if hint := a.toolRedundancy.recordCall(tc.Name, tc.Arguments); hint != "" {
				redundancyHint = hint
			}
			// Tool overuse / self-awareness detection: warn when the agent
			// calls tools to retrieve information it already has (read-after-
			// write, unchanged dir re-list, trivial env commands).
			var overuseHint string
			if hint := a.toolOveruse.maybeWarn(tc.Name, string(tc.Arguments), i+1); hint != "" {
				overuseHint = hint
				debug.Log("agent", "Iteration %d: tool overuse detected for %s", i+1, tc.Name)
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

				// Git whole-tree operations (checkout, reset, revert) change
				// potentially all files at once. They need nuclear invalidation:
				// clear mtime-based entries too, because cached reads from the
				// old branch are now wrong even if individual file mtimes
				// happened to not change.
				if gitWholeTreeTools[tc.Name] {
					a.toolMemo.invalidateAll()
					debug.Log("agent", "whole-tree git operation %s: invalidated all caches", tc.Name)
				} else {
					// Normal file edit or partial git op: invalidate
					// TTL-based memoize entries (grep, LSP, git) whose
					// results may be stale. mtime-based entries are kept.
					a.toolMemo.invalidateTTLBased()
				}

				// Invalidate the deterministic command cache: any build/test
				// results are now stale because source files changed.
				a.commandCache.invalidate()
				// Record created files so the unread-edit guard exempts them.
				for _, p := range extractCreateFilePaths(tc.Name, tc.Arguments) {
					a.unreadEdit.recordCreated(p)
					a.tunnelVision.recordFile(p)
					a.fileFreshness.recordWrite(p)
					a.readHash.recordWriteHash(p)
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
					a.tunnelVision.recordFile(p)
					a.editFailRecovery.recordRead(p)
					a.fileFreshness.recordRead(p)
					a.readHash.recordReadHash(p)
					if hint := a.redundantRead.checkRedundantRead(p); hint != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + hint
						} else {
							result.Content = hint
						}
					}
				}
			}
			// Unread-file edit guard: warn when editing a file not read in
			// this run. Fires before the tool executes so the hint is in the
			// result alongside any error from the edit attempt.
			if !result.IsError && (tc.Name == "edit_file" || tc.Name == "multi_edit_file" || tc.Name == "multi_file_edit") {
				for _, p := range extractEditFilePaths(tc.Name, tc.Arguments) {
					a.editFailRecovery.recordEditSuccess(p)
					a.fileFreshness.recordWrite(p)
					a.readHash.recordWriteHash(p)
					a.redundantRead.recordWrite(p)
					// Convergence lock: track post-verification edits.
					a.convergenceRecordEdit(tc.Name)
					// Diminishing edit: track edit substance size for polish-spiral detection.
					a.diminishingRecordEdit(tc.Name, tc.Arguments)
					// Overcorrection cascade: track edit size vs error severity.
					if ocHint := a.overcorrectionRecordEdit(tc.Name, tc.Arguments); ocHint != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + ocHint
						} else {
							result.Content = ocHint
						}
					}
					a.prematureRefactorRecordEdit(tc.Name, tc.Arguments)
					// Anchor erosion: track edit precision decay over run lifecycle.
					if aeHint := a.anchorErosion.recordEditAnchor(tc.Name, string(tc.Arguments)); aeHint != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + aeHint
						} else {
							result.Content = aeHint
						}
					}
					// Fix cascade: track edits for wrong-hypothesis lock-in detection.
					a.fixCascade.recordEdit()
					// Error regression: track edits for negative progress detection.
					a.errRegression.recordEdit()
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
					// Expired-read detection: warn when the agent edits a file
					// it previously read, marking the prior read as expired.
					if hint := a.expiredRead.recordEdit(p); hint != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + hint
						} else {
							result.Content = hint
						}
					}
					// Content-fingerprint validation: catches sub-second edits that
					// mtime misses, suppresses false positives from touch/NFS.
					if hint := a.readHash.validateContentAtEdit(p, 0); hint != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + hint
						} else {
							result.Content = hint
						}
					}
					// Export guard: detect breaking changes to exported Go symbols
					// (removed functions, changed signatures) by comparing against
					// git HEAD. Fires once per file per run.
					if hint := a.checkExportGuard(p); hint != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + hint
						} else {
							result.Content = hint
						}
					}
					// Hub package guard: per-edit blast-radius awareness for
					// widely-imported packages. Complements export_guard by
					// providing scale context even for non-breaking edits.
					if hint := a.checkHubPackage(p); hint != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + hint
						} else {
							result.Content = hint
						}
					}
					// Generated artifact guard: warn when editing lock files,
					// generated code, vendored files, or files with DO NOT EDIT
					// headers. Suggests the correct regeneration command.
					if hint := a.artifactGuard.checkGeneratedArtifact(p); hint != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + hint
						} else {
							result.Content = hint
						}
					}
					// Branch guard: warn once per run when editing on a protected
					// branch (main, master, develop, release/*).
					if hint := a.checkBranchGuard(); hint != "" {
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
				a.convergenceRecordEditError()
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

			// False premise detection: record tool errors for later contradiction
			// analysis against assistant success claims.
			a.falsePremise.recordToolResult(tc.Name, result.Content, result.IsError)

			// Overcorrection cascade: record error signals for proportionality analysis.
			a.overcorrectionRecordError(tc.Name, result.Content, result.IsError)

			// Sycophancy detection: a verification tool was used, so mark
			// pending user premises as independently verified.
			if isPremiseVerificationTool(tc.Name) {
				a.sycophancyGuard.markVerified()
			}

			// Capability boundary: track consecutive tool failures for
			// stubborn-persistence detection.
			a.capBoundary.recordToolResult(result.IsError)

			// Argument size guard: detect oversized tool arguments (e.g., huge
			// old_text anchors in edit_file, massive write_file content) and
			// inject a context-efficiency hint. Fires at most once per run.
			if argSizeHint := a.checkArgSizeGuard(tc.Name, tc.Arguments); argSizeHint != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + argSizeHint
				} else {
					result.Content = argSizeHint
				}
			}

			// Tool call sequence validator: detect cross-iteration anti-patterns
			// (e.g., full read then targeted re-read, sequential individual reads
			// instead of batch, list_directory then glob on same dir). Each
			// pattern type fires at most once per run.
			if seqHint := a.toolSequence.record(tc, i+1); seqHint != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + seqHint
				} else {
					result.Content = seqHint
				}
			}

			// Orphaned background command tracking: record start_command jobs
			// and mark output checks. Detects forgotten background processes.
			a.recordBgToolCall(tc.Name, tc.Arguments, result.Content, i+1)

			// Action annihilation detection: check if this tool call cancels
			// a prior tool call's side effects (git_add→git_reset, edit→undo, etc.).
			if annihilWarn := a.actionAnnihil.recordToolCall(tc.Name, tc.Arguments, i+1); annihilWarn != "" {
				debug.Log("agent", "Iteration %d: action annihilation detected", i+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: annihilWarn}},
				})
				msgs = a.contextManager.Messages()
			}

			// Tainted data influence detection (IFC): check if untrusted content
			// from prior tool outputs has flowed into the arguments of this
			// privileged tool call. Warns when tainted content influences
			// write/exec operations. Research: Microsoft IFC (arXiv:2505.23643).
			if taintWarn := a.taintInfluence.checkInfluence(tc.Name, string(tc.Arguments)); taintWarn != "" {
				debug.Log("agent", "Iteration %d: tainted data influence detected on %s", i+1, tc.Name)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: taintWarn}},
				})
				msgs = a.contextManager.Messages()
			}

			// Tool call storm tracking: record each tool call to detect
			// diverse-tool bursts without interleaved reasoning.
			a.toolStorm.recordToolCall(tc.Name, i+1)
			a.serialRead.recordToolCall(tc.Name)
			a.reasoningRedund.recordReasoning("", true) // tool call breaks text-only streak

			// Unverified confidence tracking: record tool calls to track
			// whether verification (build/test/lint) was run after edits.
			a.unverifiedConfidence.recordToolCall(tc.Name, string(tc.Arguments))
			a.evidenceOverconfidence.recordToolCall(tc.Name, string(tc.Arguments))
			a.phantomVerify.recordToolCall(tc.Name, string(tc.Arguments))

			// Outcome misattribution: record tool calls to track corrective
			// actions between failure and success claim.
			a.outcomeMisattrib.recordToolCallForOM(tc.Name)

			// Verification disconnect: record result to detect failures
			// that get advanced past without resolution.
			a.verifyDisconnect.recordVerificationResult(tc.Name, string(tc.Arguments), result.Content, i+1)
			a.verifyDisconnect.recordToolCallForVD(tc.Name)

			// Outcome misattribution: record failure indicators in tool
			// results to detect success claims that follow failures.
			a.outcomeMisattrib.recordResult(tc.Name, result.Content, result.IsError, i+1)

			// Foresight calibration: compare predicted outcome against
			// actual result to detect prediction-observation mismatches.
			if fcHint := a.foresightCalib.checkCalibration(tc.Name, result.Content, result.IsError, i+1); fcHint != "" {
				debug.Log("agent", "Iteration %d: foresight calibration detector triggered", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: fcHint,
					}},
				})
			}

			// Self-declared constraint violation: check if this tool call
			// violates constraints the agent declared in its own reasoning.
			if cvMsg := a.constraintViolation.checkToolCall(tc.Name, parseToolArgs(tc.Arguments), i+1); cvMsg != "" {
				debug.Log("agent", "Iteration %d: constraint violation detector triggered", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: cvMsg,
					}},
				})
			}

			// Error strategy loop: track error categories across all tool
			// calls to detect systemic approach failures (procedural memory
			// gap from ProcMEM arXiv:2602.01869).
			a.errStrategyLoop.recordResult(result.Content, result.IsError)

			// Solution fixation: track failed edit attempts per file to
			// detect diagnosis anchoring (arXiv:2505.15392, arXiv:2509.25370).
			a.solutionFixation.recordEdit(tc.Name, string(tc.Arguments), result.IsError)
			if fixationHint := a.solutionFixation.checkAndWarn(); fixationHint != "" {
				debug.Log("agent", "Iteration %d: solution fixation detector triggered", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: fixationHint,
					}},
				})
			}

			// Unverified self-diagnosis: record tool results to track errors
			// and verification calls for correlated failure detection.
			a.selfDiagState.recordToolCall(i+1, tc.Name, result.IsError)
			if strategyHint := a.errStrategyLoop.checkAndWarn(); strategyHint != "" {
				debug.Log("agent", "Iteration %d: error strategy loop detector triggered", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: strategyHint,
					}},
				})
			}

			// Temporal blindness: track verification results and mutations
			// to detect stale verification claims after code changes.
			a.temporalBlindness.recordVerification(tc.Name, string(tc.Arguments), result.Content, i+1)
			a.temporalBlindness.recordMutation(tc.Name)

			// Wasted exploration tracking: record search tool results and
			// file path consumption. Detects searches whose results were
			// never acted upon.
			a.recordWastedExploreToolCall(tc.Name, tc.Arguments, result.Content, i+1)

			// Self-modification safety: check write tool calls for targets
			// that modify the agent's own infrastructure (config, memory,
			// hooks, permissions, system prompts).
			if selfModMsg := a.checkSelfModification(tc.Name, tc.Arguments); selfModMsg != "" {
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: selfModMsg}},
				})
				msgs = a.contextManager.Messages()
			}

			// Information scent tracking: record exploration calls and their
			// path novelty to detect depleted information patches.
			a.infoScent.recordExploration(tc.Name, string(tc.Arguments), result.Content, i+1)

			// Query convergence tracking: record search queries and code
			// actions to detect repeated similar searches without progress.
			a.queryConverge.recordToolCall(tc.Name, string(tc.Arguments), i+1)

			// Plan drift capture: when exit_plan_mode fires, extract plan items
			// for later drift detection (spec-driven development tracking).
			if tc.Name == "exit_plan_mode" {
				a.planDrift.capturePlan(extractPlanFromArgs(tc.Arguments))
			}

			// Delegation orchestration: track spawned agents and result consumption.
			if a.delegationOrch != nil {
				if delegationToolNames[tc.Name] {
					taskSum := extractDelegationTaskSummary(tc.Name, tc.Arguments)
					a.delegationOrch.recordDelegationCall(tc.ID, tc.Name, taskSum, i+1)
				} else if delegationResultTools[tc.Name] {
					a.delegationOrch.recordResultCheck(tc.Name, i+1)
				}
				a.delegationOrch.recordToolCallCount()
			}

			// Record tool errors for reflection/ratchet rule extraction.
			if result.IsError {
				runStats.recordToolError(tc.Name, result.Content)
			}
			// Silent error advancement detection: track when errors go unaddressed.
			if result.IsError {
				rKey := extractErrorResourceKey(tc.Name, tc.Arguments)
				a.silentError.recordToolError(tc.Name, rKey, result.Content, i+1)
			} else {
				rKey := extractErrorResourceKey(tc.Name, tc.Arguments)
				if silentMsg := a.silentError.recordToolAction(tc.Name, rKey); silentMsg != "" {
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + silentMsg
					} else {
						result.Content = silentMsg
					}
				}
			}
			// Verification suppression detection: check shell commands for error-masking operators.
			if tc.Name == "run_command" || tc.Name == "start_command" {
				cmd := extractCommandFromToolCall(tc.Arguments)
				if cmd != "" {
					if suppressMsg := a.verifySuppress.checkVerificationSuppression(tc.Name, cmd); suppressMsg != "" {
						if result.Content != "" {
							result.Content = result.Content + "\n\n" + suppressMsg
						} else {
							result.Content = suppressMsg
						}
					}
					// Verification scope narrowing: detect progressively narrowing
					// test/build commands that mask failures (command-level spec gaming).
					if narrowMsg := a.scopeNarrow.recordVerificationCommand(tc.Name, cmd, result.Content, result.IsError); narrowMsg != "" {
						a.contextManager.Add(provider.Message{
							Role:    "user",
							Content: []provider.ContentBlock{{Type: "text", Text: narrowMsg}},
						})
						msgs = a.contextManager.Messages()
					}
				}
			}
			// Record tool result for adaptive effort classification.
			if a.effortAdapter != nil {
				a.effortAdapter.recordToolResult(tc.Name, result.IsError)
			}
			// Record tool result for adaptive sampling classification.
			if a.adaptiveSampling != nil {
				a.adaptiveSampling.recordToolResult(tc.Name, result.IsError)
			}

			// Strategy stagnation detector: tracks same-tool+target retries
			// after failure. When 2+ consecutive failures with identical
			// approach occur, inject guidance to pivot strategy.
			if a.strategyStagnation.recordAttempt(tc.Name, string(tc.Arguments), !result.IsError) {
				debug.Log("agent", "Iteration %d: strategy stagnation detected (tool=%s)", i+1, tc.Name)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: strategyStagnationWarning(tc.Name, extractStagnationTarget(tc.Name, string(tc.Arguments)), stagnationFailureThreshold),
					}},
				})
				msgs = a.contextManager.Messages()
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

			// Tool error fallback chain: on tool failure, inject actionable
			// alternative strategy suggestions. Fires once per tool per run.
			if result.IsError {
				if fallbackHint := a.toolFallbackCheck(tc.Name, result.Content); fallbackHint != "" {
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + fallbackHint
					} else {
						result.Content = fallbackHint
					}
				}
			}

			// Shell-to-native tool suggestion: when the agent uses run_command
			// for something a native tool does better (cat, grep, git log, etc.),
			// suggest the native tool for richer output and better integration.
			if nativeHint := a.shellNativeHint.maybeShellNativeHint(tc.Name, tc.Arguments); nativeHint != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + nativeHint
				} else {
					result.Content = nativeHint
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

			// Compounding failure detection: sliding-window cross-tool failure
			// rate analysis. Catches interleaved fail-succeed-fail patterns that
			// consecutive-error detection cannot (any success resets the streak).
			a.compoundingFailure.recordResult(tc.Name, result.IsError)
			if compoundingGuidance := a.compoundingFailure.check(); compoundingGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + compoundingGuidance
				} else {
					result.Content = compoundingGuidance
				}
			}

			// Diagnostic-action disconnect detection: when a tool result contains
			// diagnostic content (errors, undefined symbols, etc.), track whether
			// subsequent actions address it. Fires after N disconnected actions.
			a.diagnosticDisconnect.recordToolResult(tc.Name, extractFilePathFromArgs(tc.Name, tc.Arguments), result.Content, i+1)
			a.diagnosticDisconnect.recordAction(i + 1)

			// Tool output claim verification: detect commonly misread failure
			// signals in nominally-successful tool results (AgentRx-inspired).
			// Catches "exit code 1" in output, panics, "no results", etc. that
			// IsError does not capture, preventing the agent from claiming
			// success when the tool output contradicts that interpretation.
			if claimGuidance := a.claimVerify.check(tc.Name, result.Content, result.IsError); claimGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + claimGuidance
				} else {
					result.Content = claimGuidance
				}
			}

			// Failure mode classification: meta-level strategy guidance.
			// Classifies each error into transient/structural/systemic and
			// injects high-level strategy when a dominant mode emerges.
			if modeGuidance := a.failureMode.recordResult(tc.Name, result.IsError, result.Content); modeGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + modeGuidance
				} else {
					result.Content = modeGuidance
				}
			}

			// Error cascade detection: when multiple errors share a common root
			// resource (file path or symbol), inject root-cause-first guidance.
			if result.IsError {
				if cascadeGuidance := a.errorCascade.recordError(tc.Name, result.Content); cascadeGuidance != "" {
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + cascadeGuidance
					} else {
						result.Content = cascadeGuidance
					}
				}
			}

			// Scope drift: track productive file edits for semantic scope creep.
			a.scopeDriftRecord(tc.Name, extractFileHint(tc.Name, tc.Arguments))
			// Drift recurrence: track edits and verifications relative to any drift warning.
			a.driftRecurrenceRecord(tc.Name, extractFileHint(tc.Name, tc.Arguments))
			// Last-known-good checkpoint: track edits for revert targeting.
			a.lastGoodCheckpointRecordEdit(tc.Name, extractFileHint(tc.Name, tc.Arguments))
			// Monorepo scoper: track which packages are being edited.
			if fh := extractFileHint(tc.Name, tc.Arguments); fh != "" {
				a.monorepoScoper.recordEdit(fh)
			}
			if scopeGuidance := a.scopeDriftCheck(); scopeGuidance != "" {
				// Mark that a drift warning fired, so drift recurrence can track behavior.
				a.driftRecurrenceMarkWarn(runStats.Iterations)
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + scopeGuidance
				} else {
					result.Content = scopeGuidance
				}
			}
			// Drift recurrence: check if the agent continued the warned pattern.
			if recurrenceGuidance := a.driftRecurrenceCheck(); recurrenceGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + recurrenceGuidance
				} else {
					result.Content = recurrenceGuidance
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
			// Causal attribution: record edit steps for failure root-cause tracing.
			a.causalAttribution.recordEdit(tc.Name, extractFileHint(tc.Name, tc.Arguments), i)

			a.confidence.recordResult(tc.Name, result.IsError, extractFileHint(tc.Name, tc.Arguments))
			// Causal attribution: on failures, trace backward to the likely causal edit.
			if result.IsError || looksLikeFailure(result.Content) {
				if causalHint := a.causalAttribution.attributeFailure(result.Content); causalHint != "" {
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + causalHint
					} else {
						result.Content = causalHint
					}
				}
			}

			if confidenceGuidance := a.confidence.maybeIntervene(); confidenceGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + confidenceGuidance
				} else {
					result.Content = confidenceGuidance
				}
			}

			// Verification debt: track unverified modifications (SAUP-inspired).
			// Detects when the agent stacks edits without building/testing.
			a.verifDebt.recordToolCall(tc.Name, string(tc.Arguments))

			// Edit abandonment: track qualitative attention shift away
			// from edited files without verification (PASTE-inspired).
			a.editAbandon.recordToolCall(tc.Name, string(tc.Arguments))

			// Premature commitment: record exploratory actions to track
			// evidence gathering before the first edit.
			a.prematureCommit.recordExploration(tc.Name, extractFileHint(tc.Name, tc.Arguments))
			if debtGuidance := a.verifDebt.maybeWarn(); debtGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + debtGuidance
				} else {
					result.Content = debtGuidance
				}
			}

			if abandonGuidance := a.editAbandon.maybeWarn(); abandonGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + abandonGuidance
				} else {
					result.Content = abandonGuidance
				}
			}

			// Tool diversity stagnation: detect when one tool category dominates
			// recent calls (strategy imbalance). AgentForesight-inspired leading
			// indicator of trajectory failure.
			a.toolDiversity.recordCall(tc.Name)
			if diversityGuidance := a.toolDiversity.check(); diversityGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + diversityGuidance
				} else {
					result.Content = diversityGuidance
				}
			}

			// File churn detection: track repeated edits to the same file.
			// Each re-edit signals an invalidated assumption about the file.
			if isEditTool(tc.Name) {
				a.fileChurn.recordEdit(extractEditedPaths(tc))
				for _, p := range extractEditedPaths(tc) {
					a.tunnelVision.recordFile(p)
				}

				// Premature commitment detection: check evidence sufficiency
				// at the first edit. ECLoop (arXiv:2607.28815) shows that
				// editing before gathering sufficient context (callers, tests,
				// related code) leads to incorrect patches in 20-27% of cases.
				pcMsg := a.prematureCommit.checkFirstEdit(extractEditedPaths(tc))
				if pcMsg != "" {
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + pcMsg
					} else {
						result.Content = pcMsg
					}
				}

				if churnGuidance := a.fileChurn.check(); churnGuidance != "" {
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + churnGuidance
					} else {
						result.Content = churnGuidance
					}
				}

				// Edit oscillation detection: track content signature reversals.
				// Convergence Detection (agentpatterns.ai, 2026) identifies
				// oscillation as a critical failure pattern where the agent
				// alternates between two versions without resolving trade-offs.
				a.editOscillation.recordEdit(tc.Name, tc.Arguments, i+1)
				if oscMsg := a.editOscillation.check(); oscMsg != "" {
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + oscMsg
					} else {
						result.Content = oscMsg
					}
				}
			}

			// Analysis paralysis: detect prolonged exploration without action.
			// SICA (arXiv:2504.15228) identifies exploration-heavy / action-starved
			// trajectories as a leading indicator of task failure.
			a.analysisParalysis.recordCall(tc.Name)
			a.toolCallEconomy.recordCall(tc.Name)

			// Verbosity drift: track token-to-productivity ratio degradation.
			// Agent Drift paper (arXiv:2601.04170) identifies verbosity growth
			// without productive output as a key drift indicator.
			a.verbosityDrift.recordIteration(a.contextManager.TokenCount(), len(runStats.FilesEdited))
			if vdMsg := a.verbosityDrift.maybeWarn(runStats.Iterations); vdMsg != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + vdMsg
				} else {
					result.Content = vdMsg
				}
			}

			// CUSUM drift: cumulative behavioral drift detection across
			// error-rate, read-write ratio, and token velocity signals.
			// Uses CUSUM (Page 1954) to detect gradual directional shifts
			// that no single tool call would flag.
			cdMsg := a.cusumDrift.record(cusumRecord{
				failed:     result.IsError,
				isRead:     isReadTool(tc.Name),
				isWrite:    isEditTool(tc.Name),
				tokenDelta: a.contextManager.TokenCount(),
			})
			if cdMsg != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + cdMsg
				} else {
					result.Content = cdMsg
				}
			}
			if apGuidance := a.analysisParalysis.check(); apGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + apGuidance
				} else {
					result.Content = apGuidance
				}
			}
			if tcEconomy := a.toolCallEconomy.check(); tcEconomy != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + tcEconomy
				} else {
					result.Content = tcEconomy
				}
			}

			// Tunnel vision detection: warn when the agent has done many
			// iterations but only touched a few files (under-exploration).
			// Coppersun.dev 2026: "context window holds 1-2 files; bugs span 3+"
			if tvMsg := a.tunnelVision.check(runStats.Iterations); tvMsg != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + tvMsg
				} else {
					result.Content = tvMsg
				}
			}

			// Context footprint: track per-tool result size and warn when a
			// category dominates context consumption (Cost Intelligence).
			a.contextFootprint.recordResult(tc.Name, result.Content, runStats.Iterations)
			if footprintGuidance := a.contextFootprint.check(); footprintGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + footprintGuidance
				} else {
					result.Content = footprintGuidance
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
			a.verifyDebt.recordVerifyCommand(extractCommandFromArgs(tc.Arguments), result.IsError)
			a.prematureRefactorRecordVerify()
			if !result.IsError {
				a.editPropagation.recordGreenBuild()
			}

			// Convergence lock: record verification result to detect post-verify
			// unnecessary edit drift. A successful verify arms the lock; a failed
			// verify disarms it (agent is legitimately fixing issues).
			a.convergenceRecordVerify(tc.Name, tc.Arguments, result.IsError)

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

			// Fix cascade detection: tracks edit->verify->fail cycles regardless
			// of specific errors. Detects wrong-hypothesis lock-in where each
			// edit produces a DIFFERENT error (so recurring_error never fires).
			// Stalled convergence: track error counts across verifications
			// to detect diminishing returns (convergence plateau).
			if stalledGuidance := a.stalledConvergenceCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); stalledGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + stalledGuidance
				} else {
					result.Content = stalledGuidance
				}
			}
			if regressionGuidance := a.errorRegressionCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); regressionGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + regressionGuidance
				} else {
					result.Content = regressionGuidance
				}
			}
			// Error compounding risk: track all error signals across the run.
			// Computes geometric compounding probability to detect systemic risk.
			if hadError := a.errorCompound.recordResult(tc.Name, result.IsError, i+1); true {
				a.errorCompound.recordStep(hadError)
			}
			// Correction spiral: track edits and verify results to detect
			// error severity escalation across fix attempts.
			if csIsEditTool(tc.Name) {
				a.correctionSpiral.recordEdit(i + 1)
			} else if csIsVerifyTool(tc.Name) {
				a.correctionSpiral.recordVerifyResult(tc.Name, result.Content, result.IsError, i+1)
			}
			// Unverified mutation streak: track consecutive edits without verification.
			a.bareEditStreak.recordToolCall(tc.Name)
			// Strategy fixation: track per-file edits and verification outcomes.
			if strategyFixationIsMutation(tc.Name) {
				fp := extractFilePathFromArgs(tc.Name, tc.Arguments)
				a.strategyFixation.recordEdit(fp)
			} else if strategyFixationIsVerification(tc.Name) {
				a.strategyFixation.recordVerification(tc.Name, result.Content, result.IsError)
			}
			// Error rush: track error-to-action dynamics for panic coding detection.
			a.errorRush.recordToolCall(tc.Name, result.Content, result.IsError)
			// Target scatter: track diagnostic tool targets for world model calibration detection.
			a.targetScatter.recordToolCall(tc.Name, string(tc.Arguments))
			// Attention fragmentation: track directory switches for CLT extraneous load detection.
			var afArgs map[string]interface{}
			if len(tc.Arguments) > 0 {
				_ = json.Unmarshal(tc.Arguments, &afArgs)
			}
			a.attentionFragment.recordToolCall(tc.Name, afArgs)
			// Reckless execution: track exploration vs edit targets.
			if a.recklessExec != nil {
				argsStr := string(tc.Arguments)
				if recklessIsExplorationTool(tc.Name) {
					a.recklessExec.recordReadTool(tc.Name, argsStr)
				} else if recklessIsEditTool(tc.Name) {
					a.recklessExec.iteration = i + 1
					if a.recklessExec.recordEditTool(tc.Name, argsStr) {
						msg := recklessWarning(a.recklessExec.unexplored)
						a.contextManager.Add(provider.Message{
							Role:    "user",
							Content: []provider.ContentBlock{{Type: "text", Text: msg}},
						})
					}
				}
			}
			// Irreversibility gate: warn on under-grounded high-impact actions.
			if a.irrevGate != nil {
				if warn := a.irrevGate.recordAction(tc.Name, string(tc.Arguments)); warn != "" {
					a.contextManager.Add(provider.Message{
						Role:    "user",
						Content: []provider.ContentBlock{{Type: "text", Text: warn}},
					})
				}
			}
			// Futile cycle: track reads vs writes to detect circular exploration.
			if fileEditingTools[tc.Name] {
				a.futileCycle.recordWrite()
			} else if filePath := extractToolFilePath(tc.Name, tc.Arguments); filePath != "" {
				a.futileCycle.recordRead(filePath)
				// Expired-read: track reads for self-invalidation detection.
				a.expiredRead.recordRead(filePath)
				// Post-edit re-read check: warn if re-reading shortly after edit.
				if hint := a.expiredRead.checkPostEditReread(filePath); hint != "" {
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + hint
					} else {
						result.Content = hint
					}
				}
			}
			if fileEditingTools[tc.Name] && !result.IsError {
				a.verifyDebt.recordSourceEdit()
				a.stalledConvergence.recordEdit()
				a.editPropagation.recordEdit(tc.Name, string(tc.Arguments))
			}
			a.successDeclare.recordToolCall()
			a.subgoalTrack.recordToolCall(tc.Name, string(tc.Arguments))
			// Attempt brief: record outcome for knowledge reuse.
			a.attemptBrief.recordOutcome(tc.Name, extractToolTarget(tc.Name, string(tc.Arguments)), !result.IsError, i, result.Content)
			// Symbol grounding: record file paths and code identifiers from
			// tool I/O so we can detect ungrounded references later.
			a.symbolGrounding.recordGrounding(string(tc.Arguments), result.Content)
			// Tool result redundancy: detect when result content substantially
			// overlaps with a prior result still in context (AgentDiet waste).
			if trMsg := a.toolResultRedundancy.recordResult(tc.Name, result.Content, i+1); trMsg != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + trMsg
				} else {
					result.Content = trMsg
				}
			}
			if cascadeGuidance := a.fixCascadeCheckCommand(tc.Name, tc.Arguments, result.IsError); cascadeGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + cascadeGuidance
				} else {
					result.Content = cascadeGuidance
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

			// Convergence lock: detect post-verification unnecessary edits.
			// Fires when the agent continues editing after its changes verified.
			if convergenceGuidance := a.convergenceCheck(); convergenceGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + convergenceGuidance
				} else {
					result.Content = convergenceGuidance
				}
			}

			// Diminishing edit: detect polish-spiral (progressively smaller edits).
			if diminishingGuidance := a.diminishingCheck(); diminishingGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + diminishingGuidance
				} else {
					result.Content = diminishingGuidance
				}
			}

			// Premature refactoring: detect unverified code restructuring.
			if refactorGuidance := a.prematureRefactorCheck(); refactorGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + refactorGuidance
				} else {
					result.Content = refactorGuidance
				}
			}

			// Cross-detector consensus: scan accumulated result content for
			// detector tag signatures. When 3+ independent detectors fire within
			// a narrow window, inject a systemic-failure "step back" alert.
			// Research: Nelson-Narens metacognition; MAPE-K cross-stream anomalies.
			if consensusGuidance := a.crossDetectorConsensus.scanAndCheck(result.Content); consensusGuidance != "" {
				if result.Content != "" {
					result.Content = result.Content + "\n\n" + consensusGuidance
				} else {
					result.Content = consensusGuidance
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

			// Tainted data influence tracking (IFC): when the injection guard
			// flags tool output, record distinctive fingerprints so we can
			// later detect if that tainted content flows into privileged
			// tool calls (edit_file, write_file, run_command, etc.).
			// Research: Microsoft IFC (arXiv:2505.23643), OWASP ATR-2026-00032.
			a.taintInfluence.recordIfTainted(tc.Name, result.Content)

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
						result.Content = withTruncationAdvisory(truncated, tc.Name, len(result.Content))
					}
				}
			}
			if len(result.Images) > 0 && a.SupportsVision() {
				imgs := make([]provider.ContentImage, len(result.Images))
				for i, ri := range result.Images {
					imgs[i] = provider.ContentImage{MIME: ri.MIME, Base64: ri.Base64}
				}
				if loopGuidance != "" || searchParamHint != "" || redundancyHint != "" || overuseHint != "" {
					var hints []string
					if searchParamHint != "" {
						hints = append(hints, searchParamHint)
					}
					if loopGuidance != "" {
						hints = append(hints, loopGuidance)
					}
					if redundancyHint != "" {
						hints = append(hints, redundancyHint)
					}
					if overuseHint != "" {
						hints = append(hints, overuseHint)
					}
					hints = coalesceGuidance(hints)
					for _, h := range hints {
						if tag := extractHintTag(h); tag != "" {
							a.guidancePromoter.RecordTag(tag)
						}
					}
					result.Content = result.Content + "\n\n" + strings.Join(hints, "\n\n")
				}
				toolResults = append(toolResults, provider.ToolResultWithImages(tc.ID, tc.Name, result.Content, imgs, result.IsError))
			} else {
				if loopGuidance != "" || searchParamHint != "" || redundancyHint != "" || overuseHint != "" {
					var hints []string
					if searchParamHint != "" {
						hints = append(hints, searchParamHint)
					}
					if loopGuidance != "" {
						hints = append(hints, loopGuidance)
					}
					if redundancyHint != "" {
						hints = append(hints, redundancyHint)
					}
					if overuseHint != "" {
						hints = append(hints, overuseHint)
					}
					hints = coalesceGuidance(hints)
					for _, h := range hints {
						if tag := extractHintTag(h); tag != "" {
							a.guidancePromoter.RecordTag(tag)
						}
					}
					result.Content = result.Content + "\n\n" + strings.Join(hints, "\n\n")
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

		// Serial read serialization detection: check if this turn was a
		// single read-only call (batching opportunity across turns).
		if serialWarn := a.serialRead.endTurn(i + 1); serialWarn != "" {
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: serialWarn,
				}},
			})
		}

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

		// Tool call budget: check after all tools in this turn executed.
		// Progressive warnings (80%, 95%) and hard stop at 100%.
		if tcMsg, stop := a.toolCallBudget.check(); tcMsg != "" {
			debug.Log("tool-call-budget", "threshold crossed: calls=%d budget=%d stop=%v",
				a.toolCallBudget.totalCalls, a.toolCallBudget.effectiveBudget(), stop)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: tcMsg,
				}},
			})
			msgs = a.contextManager.Messages()
			if stop {
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventSystem,
					Text: tcMsg,
				})
				return nil
			}
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

	rawStream, err := a.provider.ChatStream(streamCtx, msgs, toolDefs)
	if err != nil {
		debug.Log("agent", "ChatStream error: %v", err)
		return nil, "", nil, false, fmt.Errorf("chat error: %w", err)
	}
	stream := streamWithStallDetection(rawStream, streamStallThreshold)

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

			// Proactive rate-limit check: if the provider exposes rate-limit
			// info and quotas are critical, emit a system warning event so
			// the UI/user is informed before a 429 occurs.
			if rl, ok := a.provider.(provider.RateLimitProvider); ok {
				info := rl.RateLimitInfo()
				if info.IsCritical() && !info.IsEmpty() {
					debug.Log("agent", "rate limit warning: req %d/%d (%.0f%%), tok %d/%d (%.0f%%)",
						info.RemainingRequests, info.LimitRequests, info.RequestFractionRemaining()*100,
						info.RemainingTokens, info.LimitTokens, info.TokenFractionRemaining()*100)
				}
			}

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
