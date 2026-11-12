package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/config"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

type DiffConfirmFunc func(ctx context.Context, filePath, diffText string) bool

type ApprovalFunc func(ctx context.Context, toolName string, input string) permission.Decision
type interruptionHandler func() []provider.ContentBlock
type runResultHandler func([]provider.ContentBlock, error)

var errStreamInterruptedForReplan = errors.New("stream interrupted for replan")

const maxAgentLLMRetries = 3

// Rate-limit retry policy: when the LLM is rate limited (transient 429 or a
// coding-plan window/quota that resets on a schedule), keep retrying the SAME
// call in place with a SAWTOOTH exponential backoff instead of failing the
// turn after a few short backoffs. Concurrency throttles recover in seconds,
// so retries start at 1s and double (1,2,4,...) up to the cap; plan-window
// quotas need much longer, and once the cap is reached the cycle RESTARTS at
// 1s and climbs again (1,2,4,...,cap,1,2,...). With the default 5m cap the
// per-cycle sequence is 1,2,4,8,16,32,64,128,256,300s. The attempt count
// defaults to unlimited (0) because window limits routinely need hours to
// reset, and the user can cancel at any time. Both are overridable via env.
const (
	defaultRateLimitRetryInterval = 5 * time.Minute
	rateLimitRetryIntervalEnvVar  = "GGCODE_RATE_LIMIT_RETRY_INTERVAL"
	rateLimitRetryMaxEnvVar       = "GGCODE_RATE_LIMIT_RETRY_MAX"
)

// rateLimitRetryPolicy returns the backoff CAP interval and max attempts for
// rate-limited LLM calls (max <= 0 means unlimited). The cap is the ceiling of
// each exponential climb, not a fixed sleep.
func rateLimitRetryPolicy() (interval time.Duration, maxAttempts int) {
	interval = defaultRateLimitRetryInterval
	if v := strings.TrimSpace(os.Getenv(rateLimitRetryIntervalEnvVar)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		} else if secs, err2 := strconv.Atoi(v); err2 == nil && secs > 0 {
			interval = time.Duration(secs) * time.Second
		}
	}
	maxAttempts = 0
	if v := strings.TrimSpace(os.Getenv(rateLimitRetryMaxEnvVar)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			maxAttempts = n
		}
	}
	return interval, maxAttempts
}

// rateLimitBackoffDelay returns the sleep before rate-limit retry attempt n
// (1-based): 1s,2s,4s,... doubling while below the cap, then the cap itself
// once, then the cycle restarts at 1s. Cap 5m yields
// 1,2,4,8,16,32,64,128,256,300,1,2,... (#rate-limit-sawtooth).
func rateLimitBackoffDelay(n int, cap time.Duration) time.Duration {
	if cap <= time.Second {
		return cap
	}
	// period = doublings strictly below cap, plus one capped step.
	// cap 5m: 1..256 are 9 doublings < 300s, period = 10 (9 climbs + 1 cap).
	period := 0
	for d := time.Second; d < cap; d *= 2 {
		period++
	}
	period++
	pos := (n - 1) % period
	if pos == period-1 {
		return cap
	}
	return time.Second << pos
}

// isRateLimitedLLMError reports whether the error is a rate limit that should
// be retried at the long fixed interval (transient 429s AND coding-plan
// window/quota resets both classify as FailureRateLimit).
func isRateLimitedLLMError(err error) bool {
	if err == nil {
		return false
	}
	return provider.ClassifyLLMError(err) == provider.FailureRateLimit
}

func agentLLMRetryLimit() int {
	if provider.RetryAlwaysEnabled() {
		return 1 << 30
	}
	return maxAgentLLMRetries
}

func isAgentRetryableLLMError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if provider.RetryAlwaysEnabled() {
		return true
	}
	s := strings.ToLower(err.Error())

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
		"retry attempts exhausted",
		"rate limit",
		"rate_limit",
		"too many requests",
		"overloaded",
		"resource_exhausted",
		"engine_overloaded",
		"service unavailable",
		"bad gateway",
	} {
		if strings.Contains(s, keyword) {
			return true
		}
	}
	return false
}

type Agent struct {
	provider             provider.Provider
	auxResolved          *config.ResolvedEndpoint
	auxProvider          provider.Provider
	auxModelName         string
	auxFailed            bool
	cascade              modelCascadeState
	cascadeSavedProvider provider.Provider

	// pendingAuditRationale: arg_rationale captured right before
	// stripArgRationale (#decision-audit); consumed by the next
	// auditToolResult call and cleared.
	pendingAuditRationale string

	llmOverseer                llmOverseerState
	tools                      *tool.Registry
	contextManager             ctxpkg.ContextManager
	maxIter                    int
	policy                     permission.PermissionPolicy
	onApproval                 ApprovalFunc
	onUsage                    func(usage provider.TokenUsage)
	usageSource                string
	onMetric                   func(metrics.MetricEvent)
	onCheckpoint               func(summaryMsgID, lastMsgID string, tokenCount int)
	lastCheckpointMessageCount int
	onRunResult                runResultHandler
	onRunHealth                func(error)

	autoVerify bool

	claimsSupervision bool

	adversarialReview            bool
	adversarialReviewRounds      int
	adversarialReviewLastRun     string
	hookConfig                   hooks.HookConfig
	rlCriticalWarned             bool
	sessionStartFired            bool
	sessionEndFired              bool
	workingDir                   string
	sessionID                    string
	checkpoints                  *checkpoint.Manager
	codeIndex                    *tool.CodeIndexManager
	diffConfirm                  DiffConfirmFunc
	onInterrupt                  interruptionHandler
	subagentCompletionFeed       func() []SubagentCompletion
	subagentNotified             map[string]bool
	subagentNotifiedMu           sync.Mutex
	projectMemory                map[string]struct{}
	supportsVision               bool
	lastTool                     string
	precompact                   *precompactState
	precompactCooldownUntil      time.Time
	idleMaint                    *IdleMaintainer
	shutdownCtx                  context.Context
	shutdownCancel               context.CancelFunc
	probeKey                     string
	autopilotGoal                string
	autopilotGoalAsked           bool
	autopilotGoalSet             bool
	autopilotStrategistCount     int
	suppressAutopilotInjection   bool
	strategistBudgetAnnounced    bool
	strategistNoProgressCount    int
	reflectionFunc               ReflectionFunc
	loopDetector                 loopDetector
	errorClassifier              *ErrorClassifier
	testFails                    *testFailCollector
	overseer                     *overseerState
	repetition                   *repetitionTracker
	speculator                   *speculator
	toolMemo                     *toolMemo
	confidence                   *confidenceState
	selfModel                    *AgentSelfModel
	verifDebt                    *verificationDebtState
	undoBlind                    *undoBlindState
	editAbandon                  *editAbandonState
	toolCallBudget               *toolCallBudget
	commandCache                 *commandCache
	effectLedger                 *effectLedgerState
	toolSearch                   *toolSearchState
	memoryTool                   *memoryToolState
	serverToolSearch             bool
	postEditVerify               postEditVerifyState
	retraceGate                  retraceGateState
	planner                      *planState
	todoStaleness                *todoStalenessState
	todoDrop                     *todoDropState
	recurringError               *recurringErrorState
	errStrategyLoop              *errStrategyState
	experienceFailureRecallFired bool
	experienceInjectedCaseIDs    []string
	toolflowHintFired            bool
	runToolNames                 []string
	solutionFixation             *solutionFixationState
	pivotDecision                *pivotDecisionTracker
	fixCascade                   *fixCascadeState
	errRegression                *errRegressionState
	stalledConvergence           *stalledConvergenceState
	unreadEdit                   *unreadEditState
	expiredRead                  *expiredReadState
	searchInvalidation           *searchInvalidationState
	wtInvalidation               *wtInvalidationState
	strategyExhaustion           *seStrategyExhaustionState
	editFailRecovery             *editFailState
	scopeDrift                   *scopeDriftState
	driftRecurrence              *driftRecurrenceState
	constraintAmnesia            *constraintAmnesiaState
	constraintViolation          *constraintViolationState
	exportGuard                  *exportGuardState
	hubPackageGuard              *hubPackageState
	artifactGuard                *generatedArtifactState
	fulfillmentGate              *fulfillmentGateState
	constraintAudit              *constraintAuditState
	constraintFollowup           *constraintFollowupState
	ambiguityPoint               *ambiguityPointState
	companionGuard               *companionGuardState
	specGaming                   *specGamingState
	scopeNarrow                  *scopeNarrowState
	complexityGate               *complexityGateState
	changeReconcile              *changeReconcileState
	claimVerify                  *claimVerifyState
	permDenyStreak               *permDenyStreakState
	diffSummary                  *diffSummaryState
	goalFinish                   *goalFinishState
	oversightTriage              *oversightTriageState
	autonomyDial                 *autonomyDialState
	commitHint                   *commitHintState
	docDrift                     *docDriftState
	draftPRHint                  *draftPRHintState
	staleReview                  *staleReviewState
	verifyRegression             *verifyRegressionState
	selfCorrectionGate           *selfCorrectionGateState
	lastGoodCheckpoint           *lastGoodCheckpoint
	sessionTimeout               *sessionTimeoutState
	diskSpace                    *diskSpaceState
	envDrift                     *envDriftState
	transientRetryBudget         int
	mutateLedger                 *mutatingLedger
	toolDedup                    *toolDedupLedger
	toolDedupOnce                sync.Once
	metadata                     map[string]string
	compoundingFailure           *compoundingFailureState
	failureMode                  *failureModeState
	toolFallback                 *toolFallbackState
	argSizeGuardFires            int
	fileFreshness                *fileFreshnessSentinel
	readHash                     *readHashTracker
	toolThermal                  *thermalState
	latencyTracker               *LatencyTracker
	toolSequence                 *toolSequenceValidator
	obsContract                  *observationContractTracker
	interventionLedger           *interventionLedger
	refusalLedger                *refusalLedger
	remoteRefusalInhibit         atomic.Bool
	lastExecutedTool             string
	planDrift                    *planDriftState
	unverifiedClaim              *unverifiedClaimState
	convergenceLock              *convergenceLockState
	userSentiment                *userSentimentState
	effortAdapter                *adaptiveEffortState
	branchGuard                  *branchGuardState
	destructiveGuard             *gitDestructiveState
	turnImpact                   *turnImpactRecorder
	errorBudget                  *errorBudget
	shellNativeHint              *shellNativeHintState
	monorepoScoper               *monorepoScoperState
	bgOrphan                     *bgOrphanState
	actionAnnihil                *actionAnnihilateState
	exploreFrag                  *exploreFragState
	batchCoupling                *batchCouplingState
	buildIdempot                 *buildIdempotencyState
	orphanFile                   *orphanFileState
	cfDep                        *cfDepState
	guidanceBudget               guidanceBudget
	guidanceStats                guidanceRunStats
	guidanceHints                map[string]guidanceHintRec
	reasoningRedund              *reasoningRedundancyState
	queryConverge                *queryConvergeState
	serialRead                   *serialReadState
	strategyFixation             *strategyFixationState
	errorRush                    *errorRushState
	attentionFragment            *attentionFragmentState
	errorCompound                *errorCompoundState
	fixAmnesia                   *fixAmnesiaState
	correctionSpiral             *correctionSpiralState
	toolResultRedundancy         *toolResultRedundancyState
	tunnelVision                 *tunnelVisionState
	prematureCommit              *prematureCommitState
	selfMod                      *selfModState
	bareEditStreak               *bareEditStreakState
	editCoverage                 *editCoverageState
	toolEff                      *toolEffTracker
	usageHints                   *toolUsageHintStore
	prematureSuccess             *prematureSuccessState
	bgVerifyJobs                 *bgVerifyRegistry
	recklessExec                 *recklessExecState
	irrevGate                    *irrevGateState
	mergeGate                    *mergeGateState
	argComplexityGate            *argComplexityGateState
	verifyDebt                   *verifyDebtState
	editPropagation              *editPropagationState
	errorCascade                 *errorCascadeState
	errorPropagate               *errorPropagateState
	delegationOrch               *delegationState
	integrationMonitor           *integrationState
	crossFileImpact              *crossFileImpactState
	cacheEffMonitor              *cacheEffMonitor
	redundantRead                *redundantReadState
	patchExhaust                 *patchExhaustState
	searchParamGuard             *searchParamGuardState
	toolRedundancy               *toolRedundancyState
	toolEquivDetect              *toolEquivDetectState
	ruleStore                    *RuleStore
	userEditObs                  *UserEditObserver
	ruleInjectCount              map[string]int
	approvalMemory               *permission.ApprovalMemory
	askThrottle                  *permission.AskThrottle
	fileChurn                    *churnState
	editOscillation              *oscillationState
	silentError                  *silentErrorState
	phantomVerify                *phantomVerifyState
	redundantReverify            *redundantReverifyState
	truncClaim                   *truncClaimState
	outputOffload                *outputOffloader
	circularReasoning            *circularReasoningState
	contradiction                *contradictionState
	reverify                     *reverifyState
	actionHedging                *actionHedgingState
	scopeCreep                   *scopeCreepState
	prematureAbstr               *prematureAbstrState
	capBoundary                  *capabilityBoundaryState
	planAbandon                  *planAbandonState
	toolTargetMismatch           *toolTargetState

	toolTape *toolTapeState

	auditLedger            *auditLedgerState
	invEngine              *invariantEngine
	wfEngine               *workflowEngine
	outcomeMisattrib       *outcomeMisattribState
	trajectoryHealth       *trajectoryHealthState
	tokenWasteBudget       *tokenWasteBudgetState
	reversibility          *reversibilityState
	mindlessAction         *mindlessActionState
	reproducerLifecycle    *reproducerLifecycleState
	successDeclare         *successDeclareState
	criteriaDrift          *criteriaDriftState
	goalDriftCtx           *goalDriftCtxState
	reasonAction           *reasonActionState
	inputUnderspec         *inputUnderspecState
	futileCycle            *futileCycleState
	compoundedUncert       *compoundedUncertaintyState
	spiralState            *spiralHallucinationState
	trajIntel              *trajIntelState
	strategyStagnation     *strategyStagnationState
	iterPressure           *iterPressureState
	termGuard              *prematureTerminationGuard
	askQualityGate         *askQualityGateState
	diminishingEdit        *diminishingEditState
	overcorrection         *overcorrectionState
	giveupRevert           *giveupRevertState
	prematureRefactor      *prematureRefactorState
	subgoalTrack           *subgoalState
	infoScent              *infoScentState
	foresightCalib         *foresightCalibrateState
	causalAttribution      *causalAttributionState
	attrExperiment         *attributionExperimentState
	attemptBrief           *attemptBriefState
	crossDetectorConsensus *consensusState
	taintInfluence         *taintInfluenceState
	exfilChain             *exfilChainState

	researchPlan             researchPlanState
	researchPlanSubs         []ResearchSubQuery
	falsePremise             *falsePremiseState
	perfBaseline             *perfBaselineState
	lastRunStats             *RunStats
	qualityScorer            *ResponseQualityScorer
	systemPromptInjector     func() string
	systemPromptLayers       []systemPromptLayer
	baseSystemPrompt         string
	lastInjectedSystemPrompt string
	harnessFPLast            string
	temporalAnchor           time.Time
	onVerifyProgress         func(text string)
	onVerifyResult           func(VerifyResult)
	onToolProgress           func(toolID, toolName, output string)
	mu                       sync.RWMutex
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

// defaultContextWindow is the legacy fallback used when the provider does
// not expose model metadata or the model is not in the known-window table.
const defaultContextWindow = 128000

// initialContextWindow derives the context manager window from provider
// model metadata instead of hardcoding it (#5002): a smaller real endpoint
// window previously let auto-compact fire too late, so runs hit the raw
// overflow error first. Falls back to defaultContextWindow when the
// provider lacks model metadata or the model is unknown.
func initialContextWindow(p provider.Provider) int {
	mp, ok := p.(provider.ModelNameProvider)
	if !ok {
		return defaultContextWindow
	}
	model := mp.ModelName()
	if model == "" {
		return defaultContextWindow
	}
	if w := config.LookupSyncedContextWindow(model); w > 0 {
		// #5123: the weekly models.dev sync table is the single source
		// of truth - consult it BEFORE the hand-maintained static table
		// so new model generations stop rotting the cold-start fallback.
		return w
	}
	if w := provider.LookupKnownModelContextWindow(model); w > 0 {
		return w
	}
	return defaultContextWindow
}

func NewAgent(p provider.Provider, tools *tool.Registry, systemPrompt string, maxIter int) *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	a := &Agent{
		provider:               p,
		tools:                  tools,
		maxIter:                maxIter,
		contextManager:         ctxpkg.NewManager(initialContextWindow(p)),
		projectMemory:          make(map[string]struct{}),
		baseSystemPrompt:       systemPrompt,
		shutdownCtx:            ctx,
		shutdownCancel:         cancel,
		overseer:               newOverseerState(),
		repetition:             newRepetitionTracker(),
		speculator:             newSpeculator(),
		toolMemo:               newToolMemo(),
		confidence:             newConfidenceState(),
		selfModel:              nil, // lazily bound in SetWorkingDir (#5498: cwd != workspace)
		verifDebt:              newVerificationDebtState(),
		undoBlind:              newUndoBlindState(),
		editAbandon:            newEditAbandonState(),
		toolCallBudget:         newToolCallBudget(),
		commandCache:           newCommandCache(),
		effectLedger:           newEffectLedger(),
		toolSearch:             newToolSearchState(),
		errorClassifier:        NewErrorClassifier(),
		testFails:              newTestFailCollector(),
		planner:                newPlanState(),
		todoStaleness:          newTodoStalenessState(),
		todoDrop:               newTodoDropState(),
		recurringError:         newRecurringErrorState(),
		errStrategyLoop:        newErrStrategyState(),
		fixCascade:             newFixCascadeState(),
		errRegression:          newErrRegressionState(),
		stalledConvergence:     newStalledConvergenceState(),
		unreadEdit:             newUnreadEditState(),
		expiredRead:            newExpiredReadState(),
		searchInvalidation:     newSearchInvalidationState(),
		wtInvalidation:         newWTInvalidationState(),
		strategyExhaustion:     newStrategyExhaustionState(),
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
		turnImpact:             newTurnImpactRecorder(),
		errorBudget:            newErrorBudget(),
		shellNativeHint:        newShellNativeHintState(),
		monorepoScoper:         newMonorepoScoperState(),
		approvalMemory:         permission.NewApprovalMemory(),
		askThrottle:            permission.NewAskThrottle(),
		crossDetectorConsensus: newConsensusState(),
		taintInfluence:         newTaintInfluenceState(),
		exfilChain:             newExfilChainState(),
		perfBaseline:           newPerfBaselineState(),
		fulfillmentGate:        newFulfillmentGateState(),
		constraintAudit:        newConstraintAuditState(),
		constraintFollowup:     newConstraintFollowupState(),
		ambiguityPoint:         newAmbiguityPointState(),
		planDrift:              newPlanDriftState(),
		unverifiedClaim:        newUnverifiedClaimState(),
		companionGuard:         newCompanionGuardState(),
		specGaming:             newSpecGamingState(),
		scopeNarrow:            newScopeNarrowState(),
		complexityGate:         newComplexityGateState(),
		changeReconcile:        newChangeReconcileState(),
		docDrift:               newDocDriftState(),
		claimVerify:            newClaimVerifyState(),
		permDenyStreak:         newPermDenyStreakState(),
		diffSummary:            newDiffSummaryState(),
		goalFinish:             newGoalFinishState(),
		oversightTriage:        newOversightTriageState(),
		autonomyDial:           newAutonomyDialState(),
		guidanceStats:          guidanceRunStats{},
		guidanceHints:          map[string]guidanceHintRec{},
		commitHint:             newCommitHintState(),
		draftPRHint:            newDraftPRHintState(),
		staleReview:            newStaleReviewState(),
		verifyRegression:       newVerifyRegressionState(),
		selfCorrectionGate:     newSelfCorrectionGateState(),
		lastGoodCheckpoint:     newLastGoodCheckpoint(),
		latencyTracker:         NewLatencyTracker(),
		toolDedup:              newToolDedupLedger(),
		toolSequence:           newToolSequenceValidator(),
		obsContract:            newObservationContractTracker(),
		interventionLedger:     newInterventionLedger(""),
		refusalLedger:          newRefusalLedger(""),
		effortAdapter:          newAdaptiveEffortStateDetectOverride(p),
		sessionTimeout:         newSessionTimeoutState(0),
		fileFreshness:          newFileFreshnessSentinel(),
		readHash:               newReadHashTracker(),
		toolThermal:            newThermalState(),
		userSentiment:          newUserSentimentState(),
		transientRetryBudget:   maxTransientRetryBudgetPerRun,
		mutateLedger:           newMutatingLedger(),
		compoundingFailure:     newCompoundingFailureState(),
		failureMode:            newFailureModeState(),
		toolFallback:           newToolFallbackState(),
		cacheEffMonitor:        newCacheEffMonitor(),
		redundantRead:          newRedundantReadState(),
		patchExhaust:           newPatchExhaustState(),
		searchParamGuard:       newSearchParamGuard(),
		toolRedundancy:         newToolRedundancyAnalyzer(),
		toolEquivDetect:        newToolEquivDetectState(),
		bgOrphan:               newBgOrphanState(),
		actionAnnihil:          newActionAnnihilateState(),
		exploreFrag:            newExploreFragState(),
		batchCoupling:          newBatchCouplingState(),
		buildIdempot:           newBuildIdempotencyState(),
		orphanFile:             newOrphanFileState(),
		cfDep:                  newCFDepState(),
		serialRead:             newSerialReadState(),
		outcomeMisattrib:       newOutcomeMisattribState(),
		reasoningRedund:        newReasoningRedundancyState(),
		queryConverge:          newQueryConvergeState(),
		causalAttribution:      newCausalAttributionState(),
		attrExperiment:         newAttributionExperimentState(),
		errorCompound:          newErrorCompoundState(),
		fixAmnesia:             newFixAmnesiaState(),
		correctionSpiral:       newCorrectionSpiralState(),
		toolResultRedundancy:   newToolResultRedundancyState(),
		selfMod:                newSelfModState(),
		tunnelVision:           newTunnelVisionState(),
		prematureCommit:        newPrematureCommitState(),
		bareEditStreak:         newBareEditState(),
		editCoverage:           newEditCoverageState(),
		toolEff:                newToolEffTracker(),
		usageHints:             newToolUsageHintStore(),
		prematureSuccess:       newPrematureSuccessState(),
		bgVerifyJobs:           newBgVerifyRegistry(),
		strategyFixation:       newStrategyFixationState(),
		errorRush:              newErrorRushState(),
		attentionFragment:      newAttentionFragmentState(),
		recklessExec:           newRecklessExecState(),
		irrevGate:              newIrrevGateState(),
		mergeGate:              newMergeGateState(),
		argComplexityGate:      newArgComplexityGateState(),
		verifyDebt:             newVerifyDebtState(),
		editPropagation:        newEditPropagationState(),
		fileChurn:              newChurnState(),
		editOscillation:        newOscillationState(),
		silentError:            newSilentErrorState(),
		phantomVerify:          newPhantomVerifyState(),
		redundantReverify:      newRedundantReverifyState(),
		solutionFixation:       newSolutionFixationState(),
		pivotDecision:          newPivotDecisionTracker(),
		reproducerLifecycle:    newReproducerLifecycleState(),
		truncClaim:             newTruncClaimState(),
		outputOffload:          newOutputOffloader(),
		circularReasoning:      newCircularReasoningState(),
		contradiction:          newContradictionState(),
		reverify:               newReverifyState(),
		actionHedging:          newActionHedgingState(),
		scopeCreep:             newScopeCreepState(),
		prematureAbstr:         newPrematureAbstrState(),
		capBoundary:            newCapabilityBoundaryState(),
		planAbandon:            newPlanAbandonState(),
		compoundedUncert:       newCompoundedUncertaintyState(),
		spiralState:            newSpiralHallucinationState(),
		trajectoryHealth:       newTrajectoryHealthState(),
		tokenWasteBudget:       newTokenWasteBudgetState(),
		mindlessAction:         newMindlessActionState(),
		strategyStagnation:     newStrategyStagnationState(),
		iterPressure:           newIterPressureState(maxIter),
		termGuard:              newPrematureTerminationGuard(),
		askQualityGate:         newAskQualityGateState(),
		diminishingEdit:        newDiminishingEditState(),
		overcorrection:         newOvercorrectionState(),
		giveupRevert:           &giveupRevertState{},
		prematureRefactor:      newPrematureRefactorState(),
		subgoalTrack:           newSubgoalState(),
		infoScent:              newInfoScentState(),
		foresightCalib:         newForesightCalibrateState(),
		reversibility:          newReversibilityState(),
		errorCascade:           newErrorCascadeState(),
		errorPropagate:         newErrorPropagateState(),
		crossFileImpact:        newCrossFileImpactState(),
		diskSpace:              newDiskSpaceState(),
		envDrift:               newEnvDriftState(),
		successDeclare:         newSuccessDeclareState(),
		criteriaDrift:          newCriteriaDriftState(),
		goalDriftCtx:           newGoalDriftCtxState(),
		reasonAction:           newReasonActionState(),
		attemptBrief:           newAttemptBriefState(),
		inputUnderspec:         newInputUnderspecState(),
		qualityScorer:          NewResponseQualityScorer(100),
		futileCycle:            newFutileCycleState(),
		trajIntel:              newTrajIntelState(),
		convergenceLock:        newConvergenceLockState(),
		delegationOrch:         newDelegationState(),
		integrationMonitor:     newIntegrationState(),
		toolTargetMismatch:     newToolTargetState(),
		toolTape:               newToolTapeState(),
		auditLedger:            newAuditLedgerState(),
	}

	a.memoryTool = newMemoryToolState()

	if tools != nil {
		if rcTool, ok := tools.Get("run_command"); ok {
			if rc, ok := rcTool.(*tool.RunCommand); ok {
				rc.OmittedOutputSpiller = func(source, omitted string) string {

					if path := a.outputOffload.spill("run_command", omitted); path != "" {
						return fmt.Sprintf(
							"\n[Omitted middle section (%s) saved to: %s - head and tail are shown above; use read_file with offset/limit or grep on that path to recover the omitted lines.]",
							formatBytes(len(omitted)), path,
						)
					}
					return ""
				}
			}
		}
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

func (a *Agent) SetProbeKey(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.probeKey = key
}

func (a *Agent) SetPermissionPolicy(policy permission.PermissionPolicy) {
	a.mu.Lock()
	defer a.mu.Unlock()

	oldMode := permission.SupervisedMode
	if mp, ok := a.policy.(modeAwarePolicy); ok {
		oldMode = mp.Mode()
	}
	newMode := permission.SupervisedMode
	if mp, ok := policy.(modeAwarePolicy); ok {
		newMode = mp.Mode()
	}

	if newMode != oldMode {
		a.autonomyDial.reset()
	}

	if newMode.IsCruiseMode() && !oldMode.IsCruiseMode() {
		a.autopilotGoal = ""
		a.autopilotGoalAsked = false
		a.autopilotGoalSet = false
	}

	if oldMode.IsCruiseMode() && !newMode.IsCruiseMode() {
		a.autopilotGoal = ""
		a.autopilotGoalAsked = false
		a.autopilotGoalSet = false
	}
	a.policy = policy
}

func (a *Agent) SetUsageHandler(fn func(usage provider.TokenUsage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onUsage = fn
	a.syncContextManagerUsageHandlerLocked()
}

func (a *Agent) SetMetricHandler(fn func(metrics.MetricEvent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMetric = fn
}

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

func (a *Agent) SetRunResultWithContentHandler(fn func([]provider.ContentBlock, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onRunResult = fn
}

func (a *Agent) SetRunHealthHandler(fn func(error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onRunHealth = fn
}

func (a *Agent) SetSystemPromptInjector(fn func() string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.systemPromptInjector = fn
}

type systemPromptLayer struct {
	name string
	fn   func() string
}

func (a *Agent) AddSystemPromptLayer(name string, fn func() string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.systemPromptLayers {
		if a.systemPromptLayers[i].name == name {
			a.systemPromptLayers[i].fn = fn
			return
		}
	}
	a.systemPromptLayers = append(a.systemPromptLayers, systemPromptLayer{name: name, fn: fn})
}

func (a *Agent) SetVerifyCallbacks(progress func(string), result func(VerifyResult)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onVerifyProgress = progress
	a.onVerifyResult = result
}

func (a *Agent) SetToolProgressCallback(fn func(toolID, toolName, output string)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onToolProgress = fn
}

func (a *Agent) SetApprovalHandler(fn ApprovalFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onApproval = fn
}

func (a *Agent) ApprovalHandler() ApprovalFunc {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.onApproval
}

func (a *Agent) SetInterruptionHandler(fn func() []provider.ContentBlock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onInterrupt = fn
}

func (a *Agent) PermissionPolicy() permission.PermissionPolicy {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.policy
}

func (a *Agent) Close() {

	a.mu.Lock()
	alreadyEnded := a.sessionEndFired
	a.sessionEndFired = true
	endHookCfg := a.hookConfig
	endWorkDir := a.workingDir
	endSessionID := a.sessionID
	a.mu.Unlock()
	if !alreadyEnded && len(endHookCfg.OnSessionEnd) > 0 {
		res := hooks.RunSessionEndHooks(endHookCfg, hooks.HookEnv{
			Event:            hooks.EventOnSessionEnd,
			SessionID:        endSessionID,
			Workspace:        endWorkDir,
			WorkingDir:       endWorkDir,
			SessionEndReason: "exit",
		})
		if res.Err != nil {
			debug.Log("agent", "on_session_end hook error: %v", res.Err)
		}
	}
	a.CancelPreCompact()

	agentSessionTimeBudgets.Delete(a)
	agentSessionTokenBudgets.Delete(a)
	agentSessionCostLimits.Delete(a)
	if a.shutdownCancel != nil {
		a.shutdownCancel()
	}
}

func (a *Agent) SetContextManager(cm ctxpkg.ContextManager) {

	a.CancelPreCompact()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.contextManager = cm
	a.syncContextManagerProviderLocked()
	a.syncContextManagerUsageHandlerLocked()
	a.syncContextManagerTodoPathLocked()
}

func (a *Agent) AddMessage(msg provider.Message) {
	a.contextManager.Add(msg)
}

func (a *Agent) ReconcileToolCalls() bool {
	if a.contextManager == nil {
		return false
	}
	return a.contextManager.ReconcileToolCalls()
}

func (a *Agent) RemoveLastUserMessage() bool {
	if a.contextManager == nil {
		return false
	}
	return a.contextManager.RemoveLastUserMessage()
}

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

func (a *Agent) Messages() []provider.Message {
	return a.contextManager.Messages()
}

func (a *Agent) AddedSinceRunStart() []provider.Message {
	if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
		return cm.AddedSinceRunStart()
	}
	return nil
}

func (a *Agent) StartRunTracking() {
	if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
		cm.StartRunTracking()
	}
}

func (a *Agent) SetProvider(p provider.Provider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.provider = p

	a.serverToolSearch = false
	a.syncContextManagerProviderLocked()
}
func (a *Agent) Provider() provider.Provider {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.provider
}
func (a *Agent) SetReasoningEffort(effort string) bool {
	ok := a.setReasoningEffortInternal(effort)
	if ok {

		a.mu.Lock()
		if a.effortAdapter != nil {
			a.effortAdapter.setUserOverride(effort != "")
		}
		a.mu.Unlock()
	}
	return ok
}

func (a *Agent) setReasoningEffortInternal(effort string) bool {
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

func (a *Agent) ToolRegistry() *tool.Registry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tools
}

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

func (a *Agent) MemoryToolEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.memoryTool != nil && a.memoryTool.enabled
}

func (a *Agent) SetMemoryToolEnabled(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.memoryTool != nil {
		a.memoryTool.enabled = v
	}
}

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

func (a *Agent) UpdateSystemPrompt(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.baseSystemPrompt = text
	a.lastInjectedSystemPrompt = ""
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

func (a *Agent) SetCheckpointManager(m *checkpoint.Manager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkpoints = m
	// Keep provenance tools (why) resolvable: mirror the manager onto the
	// registry the same way sessionidx is attached.
	if a.tools != nil {
		a.tools.SetCheckpoints(m)
	}
}

func (a *Agent) CheckpointManager() *checkpoint.Manager {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.checkpoints
}

func (a *Agent) LastRunStats() *RunStats {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastRunStats
}

func (a *Agent) InvalidateToolCaches() {
	a.speculator.invalidateCache()
	a.toolMemo.invalidateTTLBased()
	a.commandCache.invalidate()
}

func (a *Agent) SetCodeIndexManager(m *tool.CodeIndexManager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.codeIndex = m
}

func (a *Agent) CodeIndexManager() *tool.CodeIndexManager {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.codeIndex
}

func (a *Agent) SetDiffConfirm(fn DiffConfirmFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.diffConfirm = fn
}

func (a *Agent) SetHookConfig(cfg hooks.HookConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hookConfig = cfg
}

func (a *Agent) SetSessionTokenBudget(budget int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	setAgentSessionTokenBudget(a, budget)
}

func (a *Agent) SetSessionTimeBudget(budget time.Duration) {
	setAgentSessionTimeBudget(a, budget)
}

func (a *Agent) SetToolCallBudget(budget int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolCallBudget.SetBudget(budget)
}

func (a *Agent) SetSessionTimeout(timeout time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionTimeout.timeout = timeout
}

func (a *Agent) GetHookConfig() hooks.HookConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hookConfig
}

func (a *Agent) SetCheckpointHandler(fn func(summaryMsgID, lastMsgID string, tokenCount int)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onCheckpoint = fn
}

func (a *Agent) SetPersistHandler(fn func(msg provider.Message)) {
	if m, ok := a.contextManager.(*ctxpkg.Manager); ok {
		m.SetPersistHandler(fn)
	}
}

// SetEarlyCompactRatio forwards an earlier auto-compact trigger ratio to the
// context manager (#5002 gap 2). Sub-agent harnesses use this to schedule
// proactive background compaction at ~70% of the window instead of waiting
// for the ~90% reactive path.
func (a *Agent) SetEarlyCompactRatio(r float64) {
	if m, ok := a.contextManager.(*ctxpkg.Manager); ok {
		m.SetEarlyCompactRatio(r)
	}
}

func (a *Agent) SetWorkingDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	dirChanged := a.workingDir != dir
	a.workingDir = dir

	if a.interventionLedger == nil || a.interventionLedger.workingDir != dir {
		a.interventionLedger = newInterventionLedger(dir)
	}
	if a.refusalLedger == nil || a.refusalLedger.workingDir != dir {
		a.refusalLedger = newRefusalLedger(dir)
	}

	// #5498: both persistent stores were previously pinned to the process
	// working directory ("."), so in ACP/daemon deployments (cwd != workspace)
	// every read/write landed beside the binary. Rebind on workspace change,
	// same pattern as the ledgers above. (The anchor-store half was removed
	// wholesale in #5514.)
	if a.selfModel == nil || a.selfModel.boundDir != dir {
		a.selfModel = LoadSelfModel(SelfModelPath(dir))
		a.selfModel.boundDir = dir
	}

	if a.unreadEdit != nil {
		a.unreadEdit.baseDir = dir
	}
	if a.expiredRead != nil {
		a.expiredRead.baseDir = dir
	}

	if a.tunnelVision != nil {
		a.tunnelVision.baseDir = dir
	}

	if a.searchInvalidation != nil {
		a.searchInvalidation.setBaseDir(dir)
	}

	if dirChanged && a.ruleStore != nil {
		a.resetRuleStoreLocked()
	}
}
func (a *Agent) WorkingDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workingDir
}

func (a *Agent) SessionID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessionID
}

func (a *Agent) SetSessionID(id string) {

	a.mu.Lock()
	a.sessionID = id
	a.syncContextManagerTodoPathLocked()
	reg := a.tools
	a.mu.Unlock()

	// Bind the session-recall index to this session (own-index ownership).
	if reg != nil {
		if m := reg.SessionIndex(); m != nil {
			safego.Go("agent.setLiveID", func() { m.SetLiveID(id) })
		}
	}

	if pcs, ok := a.provider.(provider.PromptCacheKeySetter); ok {
		pcs.SetPromptCacheKey("ggcode-" + id)
	}

	if a.tools != nil {
		if t, ok := a.tools.Get("todo_write"); ok {
			if tw, ok := t.(*tool.TodoWrite); ok {
				tw.SetSessionID(id)
			}
		}
	}
}
func (a *Agent) syncContextManagerTodoPathLocked() {
	if cm, ok := a.contextManager.(todoPathAwareContextManager); ok {
		cm.SetTodoFilePath(tool.TodoFilePath(a.sessionID))
	}
}

func (a *Agent) Clear() {
	a.CancelPreCompact()
	a.contextManager.Clear()
}

func (a *Agent) RunStream(ctx context.Context, userMsg string, onEvent func(provider.StreamEvent), origin ...string) error {
	content := []provider.ContentBlock{{Type: "text", Text: userMsg}}
	return a.RunStreamWithContent(ctx, content, onEvent, origin...)
}

func (a *Agent) SetIdleMaintainer(m *IdleMaintainer) {
	if a == nil || m == nil {
		return
	}
	a.mu.Lock()
	a.idleMaint = m
	a.mu.Unlock()
}

func (a *Agent) currentIdleMaintainer() *IdleMaintainer {
	a.mu.Lock()
	m := a.idleMaint
	a.mu.Unlock()
	return m
}

func userPromptForStatsSafe(content []provider.ContentBlock) string {
	var sb strings.Builder
	for _, b := range content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

func estimateToolDefinitionOverhead(defs []provider.ToolDefinition) int {
	total := 0
	for _, d := range defs {
		total += len(d.Name)
		total += len(d.Description)
		total += len(d.Parameters)
	}
	return total / 4
}

func (a *Agent) RunStreamWithContent(ctx context.Context, content []provider.ContentBlock, onEvent func(provider.StreamEvent), origin ...string) (err error) {
	userOrigin := ""
	if len(origin) > 0 {
		userOrigin = origin[0]
	}
	debug.Log("agent", "RunStreamWithContent START content_blocks=%d", len(content))

	a.getUserEditObserver().CheckTurnBoundary()

	if im := a.currentIdleMaintainer(); im != nil {
		im.RunBegin()
		defer im.RunEnd()
	}

	defer func() {
		if r := recover(); r != nil {
			path := WriteCrashLog("agent", r)

			if err != nil {
				err = fmt.Errorf("agent run panicked: %v (prior error: %v; stack saved to %s)", r, err, path)
			} else {
				err = fmt.Errorf("agent run panicked: %v (stack saved to %s)", r, path)
			}
		}
	}()

	a.mu.RLock()
	sid := a.sessionID
	a.mu.RUnlock()
	MarkRunning(sid, userPromptForStatsSafe(content), os.Getpid())

	defer maybePersistSpecPatterns(a.speculator)

	if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
		cm.StartRunTracking()
	}

	a.mu.RLock()
	startHookCfg := a.hookConfig
	startWorkDir := a.workingDir
	a.mu.RUnlock()
	if len(startHookCfg.OnSessionStart) > 0 {
		if err := a.fireSessionStartHooks(sid, startHookCfg, startWorkDir, onEvent); err != nil {
			return err
		}
	}

	userPromptForStats := ""
	for _, b := range content {
		if b.Type == "text" {
			userPromptForStats += b.Text
		}
	}

	if a.goalDriftCtx != nil {
		a.goalDriftCtx.reset()
	}
	a.goalDriftCtx.initFromUserMessage(userPromptForStats)
	runStats := newRunStats(userPromptForStats)

	if a.contextManager != nil {

		a.experienceFailureRecallFired = false

		a.experienceInjectedCaseIDs = nil

		a.toolflowHintFired = false
		a.runToolNames = nil
		if idx := a.recallExperience(userPromptForStats); idx != "" {
			a.contextManager.Add(provider.Message{
				Role:    "system",
				Content: []provider.ContentBlock{{Type: "text", Text: "## Relevant Experience\n" + idx}},
			})
			debug.Log("agent", "injected experience recall (%d chars)", len(idx))
		}
	}

	asyncVerifyStats := (*RunStats)(nil)

	a.resetTurnDetectors()
	// #turn-dryrun: surface the previous run's blast radius as a read-only
	// observation (inform-only, per the predictive-guardrail lesson of
	// arXiv:2611.02815 - never block).
	if a.turnImpact != nil {
		if rep := a.turnImpact.report(); rep != nil {
			if a.contextManager != nil {
				if txt := rep.render(); txt != "" {
					a.contextManager.Add(provider.Message{
						Role:    "system",
						Content: []provider.ContentBlock{{Type: "text", Text: txt}},
					})
					debug.Log("agent", "injected turn impact report (%d files, tier=%s)", len(rep.Files), rep.RiskTier)
				}
			}
			a.turnImpact.reset()
		}
	}
	// #34 env fingerprint drift: warn (once per change) that prior
	// build/test observations may be stale when manifests or toolchain
	// shifted; baseline is persisted so it survives restarts.
	if driftMsg := checkEnvDrift(a.WorkingDir()); driftMsg != "" && a.contextManager != nil {
		a.contextManager.Add(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: driftMsg}},
		})
		debug.Log("agent", "injected environment drift warning")
	}
	defer func() {
		a.runStreamEpilogue(ctx, content, err, sid, runStats, asyncVerifyStats)
	}()
	userTurn := provider.Message{
		Role:    "user",
		Content: content,
		Origin:  userOrigin,
	}
	a.contextManager.Add(userTurn)

	userText := ""
	for _, b := range content {
		if b.Type == "text" {
			userText += b.Text
		}
	}

	a.constraintAmnesia.reset()
	a.constraintAmnesia.recordConstraints(userText, 1)

	if !a.consumeRemoteRefusalInhibit() {
		a.ReleaseMatchingRefusals(userText)
		a.recordUserRefusals(userText)
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

	if a.ReconcileToolCalls() {
		debug.Log("agent", "RunStreamWithContent: reconciled unpaired tool_calls")
	}

	a.clearGoalIfNotAutopilot()
	a.maybeInjectAutopilotGoalCollection()
	a.maybeInjectCorrectionFeedback()
	a.maybeInjectSentimentFeedback(userPromptForStats)

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
	a.maybeInjectPerfRegression()
	a.maybeInjectDynamicSystemPrompt(userPromptForStats)
	a.maybeInjectRatchetRules()

	a.mu.RLock()
	uhDir := a.workingDir
	a.mu.RUnlock()
	if uhDir != "" {
		a.usageHints.attach(filepath.Join(uhDir, ".ggcode"))
		a.tools.SetDescriptionAugmenter(a.usageHints.OverlayFor)
	}
	toolDefs := a.tools.ToDefinitions()
	a.toolSearch.init(toolDefs)

	if enabler, ok := a.provider.(interface{ ServerToolSearchActive() bool }); ok && enabler.ServerToolSearchActive() {
		a.toolSearch.disable()
		a.serverToolSearch = true
		debug.Log("agent", "server-side tool search active: MCP schemas deferred via defer_loading")
	}
	if a.toolSearch.enabled {
		debug.Log("agent", "tool search: %d MCP tool schemas deferred behind %s", len(a.toolSearch.deferred), ToolSearchToolName)
	}
	if cm, ok := a.contextManager.(interface{ SetToolDefinitionOverhead(int) }); ok {
		cm.SetToolDefinitionOverhead(estimateToolDefinitionOverhead(toolDefs))
	}
	a.autopilotStrategistCount = 0
	a.strategistBudgetAnnounced = false
	a.strategistNoProgressCount = 0

	a.resetOverseer(userText)
	a.resetPlanner()

	a.researchPlan.reset()
	a.researchPlanSubs = nil

	a.plannerAnalyze(userText)

	a.maybeInjectResearchPlan(userText)
	a.resetTodoStaleness()
	a.resetTodoDrop()
	a.resetScopeDrift()
	a.bgVerifyJobs = newBgVerifyRegistry()
	a.resetDriftRecurrence()

	a.attentionFragment.reset()

	if a.spiralState != nil {
		a.spiralState.reset()
	}
	a.resetLastGoodCheckpoint()
	a.recurringError.reset()
	a.errStrategyLoop.reset()

	a.termGuard.reset()

	a.askQualityGate.reset()
	a.fixCascade.reset()
	a.errRegression.reset()
	a.stalledConvergence.reset()
	a.speculator.resetSequence()
	a.toolMemo.reset()
	a.commandCache.reset()
	a.confidence.reset()
	if a.errorBudget != nil {
		a.errorBudget.reset()
	}
	a.verifDebt.reset()
	a.undoBlind.reset()
	a.editAbandon.reset()
	a.toolCallBudget.reset()
	a.toolCallBudget.SetDefaultBudget(deriveDefaultBudget(a.maxIter))

	a.resetSessionTokenUsage()

	a.resetSessionTimeUsage()

	a.resetSessionCostUsage()

	a.unreadEdit.reset()
	a.expiredRead.reset()
	a.searchInvalidation.reset()
	a.wtInvalidation.reset()
	a.strategyExhaustion.reset()
	a.falsePremise.reset()
	a.phantomVerify.reset()

	a.editFailRecovery.reset()

	a.solutionFixation.reset()
	a.pivotDecision.reset()
	a.redundantReverify.reset()

	a.exportGuard.reset()
	a.hubPackageGuard.reset()
	a.artifactGuard.reset()
	a.branchGuard.reset()
	a.destructiveGuard.reset()
	a.fulfillmentGate.reset()
	a.oversightTriage.reset()
	a.autonomyDial.reset()

	defer func() {
		if d := a.oversightTriage.digest(); d != "" {
			onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: d})
		}

		if d := a.autonomyDial.suggest(); d != "" {
			onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: d})
		}
	}()

	defer a.flushGuidanceStats()

	defer a.routeWeaknessSignals()
	a.constraintAudit.reset()
	a.constraintFollowup.reset()
	a.ambiguityPoint.reset()
	a.planDrift.reset()
	a.unverifiedClaim.reset()
	a.companionGuard.reset()
	a.specGaming.reset()
	a.scopeNarrow.reset()
	a.verifyRegression.reset()
	a.resetSelfCorrectionGate()
	a.argSizeGuardFires = 0
	a.redundantRead.reset()
	a.patchExhaust.reset()
	a.searchParamGuard.reset()
	a.toolRedundancy.reset()
	a.toolSequence.reset()
	a.obsContract.reset()
	a.resetTransientRetryBudget()
	a.mutateLedger.reset()
	a.compoundingFailure.reset()

	a.fileChurn.reset()
	a.editOscillation.reset()
	a.silentError.reset()

	a.serialRead.reset()
	a.toolEff.reset()
	a.reasoningRedund.reset()
	a.reproducerLifecycle.reset()
	a.truncClaim.reset()
	a.circularReasoning.reset()
	a.contradiction.reset()
	a.reverify.reset()
	a.actionHedging.reset()
	a.scopeCreep.reset()
	a.prematureAbstr.reset()
	a.capBoundary.reset()
	a.planAbandon.reset()
	a.compoundedUncert.reset()
	a.trajectoryHealth.reset()
	if a.tokenWasteBudget != nil {
		a.tokenWasteBudget.reset()
	}
	a.mindlessAction.reset()

	a.strategyStagnation.reset()
	a.infoScent.reset()
	a.causalAttribution.reset()
	a.attrExperiment.reset()
	a.reversibility.reset()
	a.constraintViolation.reset()
	a.inputUnderspec.reset()
	a.tunnelVision.reset()
	a.queryConverge.reset()
	a.prematureCommit.reset()
	a.failureMode.reset()
	a.toolFallback.reset()
	a.errorCascade.reset()
	a.errorPropagate.reset()
	a.fileFreshness.reset()
	a.readHash.reset()
	a.toolThermal.reset()
	a.cacheEffMonitor.reset()

	a.changeReconcile.reset()
	a.claimVerify.reset()
	a.permDenyStreak.reset()
	a.crossFileImpact.reset()
	a.diffSummary.reset()
	a.goalFinish.reset()
	a.commitHint.reset()
	a.docDrift.reset()
	a.draftPRHint.reset()
	a.staleReview.reset()
	if workingDir := a.WorkingDir(); workingDir != "" {
		a.changeReconcile.capturePreRunState(workingDir)

		if n := a.changeReconcile.dirtyFileCount(); n > 0 {
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf(
						"[workspace] Note: %d file(s) have uncommitted changes in the working tree "+
							"(your own work before this session). When committing, stage only the files "+
							"you modified during this task - do not use 'git add -A' or 'git commit -a' "+
							"unless the user explicitly asks.",
						n,
					),
				}},
			})
		}
	}

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

	a.envDrift.reset()
	if workingDir := a.WorkingDir(); workingDir != "" {

		a.monorepoScoper.detectMonorepo(workingDir)
		if envMsg := a.envDrift.check(workingDir); envMsg != "" {
			debug.Log("env-drift", "env var drift detected, injecting advisory")
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: envMsg}},
			})
		}
	}

	if a.checkpoints != nil {
		a.checkpoints.StartRun(runStats.RunID())
	}

	a.sessionTimeout.start(a.currentMode().IsCruiseMode())

	if underspecHint := a.maybeWarnInputUnderspec(userText); underspecHint != "" {
		debug.Log("input-underspec", "underspecified user request detected, injecting advisory")
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: underspecHint}},
		})
	}

	turnCounters := &chatTurnCounters{}
	st := &loopState{detectorContinueBudget: 2}

	err = a.runConversationLoop(ctx, onEvent, sid, userPromptForStats, toolDefs, runStats, st, turnCounters)
	return err
}

// dedupKey identifies an in-turn read-only tool call for speculative
// deduplication (tool name + raw args).
type dedupKey struct {
	tool string
	args string
}

// fireSessionStartHooks runs the one-shot session-start hook volley for the
// first turn of a session (startup vs resume detection included) and
// surfaces hook output as a system message. Extracted verbatim from
// RunStreamWithContent (#5521 PR1, mechanical).
func (a *Agent) fireSessionStartHooks(sid string, hookCfg hooks.HookConfig, workDir string, onEvent func(provider.StreamEvent)) error {
	a.mu.Lock()
	firstTurn := !a.sessionStartFired
	a.sessionStartFired = true
	a.mu.Unlock()
	if !firstTurn {
		return nil
	}
	source := "startup"

	if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
		for _, msg := range cm.Messages() {
			if msg.Role != "system" {
				source = "resume"
				break
			}
		}
	}
	ssRes := hooks.RunSessionStartHooks(hookCfg, hooks.HookEnv{
		Event:         hooks.EventOnSessionStart,
		SessionID:     sid,
		Workspace:     workDir,
		WorkingDir:    workDir,
		SessionSource: source,
	})
	if !ssRes.Allowed {
		onEvent(provider.StreamEvent{
			Type:  provider.StreamEventError,
			Error: fmt.Errorf("%s", ssRes.Output),
		})

		MarkCompleted(sid, false, 0, 0)
		return fmt.Errorf("session start blocked by hook: %s", ssRes.Output)
	}
	if out := strings.TrimSpace(ssRes.Output); out != "" && a.contextManager != nil {
		a.contextManager.Add(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: "[Session start hooks]\n" + out}},
		})
		debug.Log("agent", "injected session-start hook output (%d chars)", len(out))
	}
	return nil
}

// runStreamEpilogue is the deferred end-of-run bookkeeping: stats
// persistence, cancellation classification, reflection/trajectory
// intelligence, perf baseline, run-result callbacks, todo cleanup, async
// verification kick-off and the fallback checkpoint. Extracted verbatim
// from RunStreamWithContent's defer body (#5521 PR1, mechanical).
func (a *Agent) runStreamEpilogue(ctx context.Context, content []provider.ContentBlock, err error, sid string, runStats *RunStats, asyncVerifyStats *RunStats) {
	MarkCompleted(sid, err == nil, runStats.Iterations, len(runStats.FilesEdited))
	runStats.finalize(err)
	a.mu.Lock()
	a.lastRunStats = runStats
	a.mu.Unlock()

	isCancelled := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrSessionTimeout) || errors.Is(err, ErrSessionBudgetExhausted) ||
		(err == nil && ctx.Err() != nil && (errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded)))
	if !isCancelled {
		a.maybeReflect(runStats)

		a.recordFailureAttribution(runStats)
	} else {
		debug.Log("agent", "skipping reflection/ratchet on cancellation")

		MarkInterrupted(sid, InterruptSnapshot{
			Timestamp:    time.Now(),
			LastTool:     a.lastTool,
			Iterations:   runStats.Iterations,
			FilesTouched: len(runStats.FilesEdited),
		})
	}

	if a.trajIntel != nil {

		a.trajIntel.ingestTeammateExperience(a.WorkingDir())

		if !isCancelled {
			a.trajIntel.recordInjectionOutcome(a.WorkingDir(), runStats.Success)

			a.trajIntel.recordHoldoutOutcome(a.WorkingDir(), runStats.Success)
		} else {

			a.trajIntel.clearInjectedRun()
		}
		a.trajIntel.maybeExtractAndPersist(a.WorkingDir(), runStats)

		a.maybeDistillPolicies(a.WorkingDir())

		a.maybeExportTrainingSample(a.WorkingDir(), runStats)
	}

	recordPerfBaseline(a.WorkingDir(), runStats)
	a.mu.RLock()
	fn := a.onRunResult
	a.mu.RUnlock()
	if fn != nil {
		fn(content, err)
	}

	a.mu.RLock()
	healthFn := a.onRunHealth
	a.mu.RUnlock()
	if healthFn != nil {
		healthFn(err)
	}

	if t, ok := a.tools.Get("todo_write"); ok {
		if tw, ok := t.(*tool.TodoWrite); ok {
			tw.ClearTodos()
		}
	}

	if asyncVerifyStats != nil && err == nil && !isCancelled {
		statsCopy := *asyncVerifyStats
		safego.Go("asyncVerify", func() {
			a.asyncVerify(a.shutdownCtx, &statsCopy)
		})
	}

	a.maybeFallbackCheckpoint()
}

func (a *Agent) injectPendingInterruptions(iter int) (bool, InterruptionKind) {
	a.mu.RLock()
	fn := a.onInterrupt
	a.mu.RUnlock()
	if fn == nil {
		return false, InterruptionAddition
	}
	blocks := fn()

	nonBlank := false
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) == "" {
			continue
		}
		nonBlank = true
		break
	}
	if !nonBlank {
		return false, InterruptionAddition
	}
	kind := classifyInterruption(firstTextBlock(blocks))
	debug.Log("agent", "injecting mid-run user guidance (kind=%s)", kind)

	content := []provider.ContentBlock{{
		Type: "text",
		Text: interruptionPreamble(kind),
	}}
	content = append(content, blocks...)
	a.contextManager.Add(provider.Message{
		Role:    "user",
		Content: content,
	})

	a.constraintAmnesia.recordConstraints(firstTextBlock(blocks), iter)
	return true, kind
}

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

func (a *Agent) fillCancelledToolResults(pending []provider.ToolCallDelta, results *[]provider.ContentBlock) {
	for _, tc := range pending {
		debug.Log("agent", "Filling cancelled tool_result for tool=%s id=%s", tc.Name, tc.ID)
		*results = append(*results, provider.ToolResultNamedBlock(
			tc.ID, tc.Name,
			"operation cancelled by user",
			true,
		))
	}
	if len(pending) > 0 {
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: *results,
		})
	}
}

func hasNonThinkingContent(blocks []provider.ContentBlock) bool {
	for _, b := range blocks {
		switch b.Type {
		case "thinking", "redacted_thinking":
			continue
		case "tool_use":
			return true

		case "server_tool_use", "web_search_tool_result", "web_fetch_tool_result",
			"tool_search_tool_result", "code_execution_tool_result":
			return true
		default:
			if strings.TrimSpace(b.Text) != "" {
				return true
			}
		}
	}
	return false
}
