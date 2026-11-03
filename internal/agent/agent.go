package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/topcheer/ggcode/internal/util"
)

type DiffConfirmFunc func(ctx context.Context, filePath, diffText string) bool

type ApprovalFunc func(ctx context.Context, toolName string, input string) permission.Decision
type interruptionHandler func() []provider.ContentBlock
type runResultHandler func([]provider.ContentBlock, error)

var errStreamInterruptedForReplan = errors.New("stream interrupted for replan")

const maxAgentLLMRetries = 3

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
	case "write_file", "edit_file", "multi_edit_file", "lsp_rename":

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
	case "batch_replace":

		if files, ok := args["files"].([]any); ok {
			for _, f := range files {
				if p, ok := f.(string); ok {
					paths = append(paths, p)
				}
			}
		}
	case "file_ops":

		if ops, ok := args["operations"].([]any); ok {
			for _, o := range ops {
				om, ok := o.(map[string]any)
				if !ok {
					continue
				}
				if p, ok := om["source"].(string); ok && p != "" {
					paths = append(paths, p)
				}
				if p, ok := om["destination"].(string); ok && p != "" {
					paths = append(paths, p)
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
	a.mu.Unlock()

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
		a.mu.Lock()
		firstTurn := !a.sessionStartFired
		a.sessionStartFired = true
		a.mu.Unlock()
		if firstTurn {
			source := "startup"

			if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
				for _, msg := range cm.Messages() {
					if msg.Role != "system" {
						source = "resume"
						break
					}
				}
			}
			ssRes := hooks.RunSessionStartHooks(startHookCfg, hooks.HookEnv{
				Event:         hooks.EventOnSessionStart,
				SessionID:     sid,
				Workspace:     startWorkDir,
				WorkingDir:    startWorkDir,
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

	syncVerifyRetries := 0

	a.resetTurnDetectors()
	defer func() {

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
	transientCompactWarned := false

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

	sessionTimedOut := false
	guidanceAtBudget := false
	sessionBudgetStop := false

	detectorContinueBudget := 2
	for i := 0; a.maxIter <= 0 || i < a.maxIter; i++ {

		if sessionBudgetStop {

			runStats.finalize(nil)
			summary := runStats.Summary()
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventText,
				Text: fmt.Sprintf("\nSession budget exhausted (token/cost/delegated/time limit reached). No further LLM calls this run. Summary: %s.\nYour task may be partially complete - review the changes above. You can continue by sending a follow-up message or raising the session budget.", summary),
			})
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ErrSessionBudgetExhausted})
			debug.Log("session-token-budget", "winding down: no further LLM calls (budget stop gate, sentinel returned)")
			return ErrSessionBudgetExhausted
		}
		runStats.Iterations = i + 1
		if err := ctx.Err(); err != nil {
			return err
		}
		a.guidanceBudget.reset()

		if msg := a.sessionTimeout.check(); msg != "" {
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventSystem,
				Text: msg,
			})
			if a.sessionTimeout.shouldStop() {
				debug.Log("session-timeout", "wall-clock timeout exceeded, stopping agent loop")
				sessionTimedOut = true
				break
			}
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: msg,
				}},
			})
		}

		if a.consumeReadyPreCompact(onEvent) {
			runStats.recordCompaction()
		}
		if injected, kind := a.injectPendingInterruptions(i + 1); injected {
			a.recordIntervention(i + 1)

			if a.maxIter > 0 && i == a.maxIter-1 {
				guidanceAtBudget = true
			}

			if kind == InterruptionRetractStop {
				debug.Log("agent", "retraction-stop: aborting run after user withdrew the task")
				return context.Canceled
			}
			continue
		}

		a.injectSubagentCompletions()
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

		if planHint := a.maybeSuggestPlan(i + 1); planHint != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: planHint}},
			})
			msgs = a.contextManager.Messages()
		}

		if staleReminder := a.maybeRemindStaleTodo(i + 1); staleReminder != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: staleReminder}},
			})
			msgs = a.contextManager.Messages()
		}

		if staleMsg := a.fileFreshness.maybeCheckStaleFiles(i + 1); staleMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: staleMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		if thermalMsg := a.toolThermal.maybeWarn(i); thermalMsg != "" {
			debug.Log("thermal-profile", "imbalanced tool usage detected at iteration %d: %s", i+1, a.toolThermal.categoryBreakdown())
			msgs = a.guidanceEmit(thermalMsg, msgs)
		}

		if ecMsg := a.errorCompound.maybeWarn(i + 1); ecMsg != "" {

			if a.injectGuidance(ecMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.errorCompound.markUndelivered()
			}
		}

		if csMsg := a.correctionSpiral.maybeWarn(i + 1); csMsg != "" {
			msgs = a.guidanceEmit(csMsg, msgs)
		}

		if vdMsg := a.verifyDebt.maybeWarn(i + 1); vdMsg != "" {
			msgs = a.guidanceEmit(vdMsg, msgs)
		}

		if epMsg := a.editPropagation.maybeWarn(i + 1); epMsg != "" {
			msgs = a.guidanceEmit(epMsg, msgs)
		}

		if a.claimsSupervision {
			if sgMsg := a.subgoalTrack.maybeWarn(i + 1); sgMsg != "" {
				debug.Log("agent", "Iteration %d: subgoal completion gap detected", i+1)

				if a.injectGuidance(sgMsg) {
					msgs = a.contextManager.Messages()
				} else {
					a.subgoalTrack.markUndelivered()
				}
			}
		}

		if a.claimsSupervision {
			if sdMsg := a.successDeclare.maybeWarn(i + 1); sdMsg != "" {
				debug.Log("agent", "Iteration %d: premature success declaration detected", i+1)

				if a.injectGuidance(sdMsg) {
					msgs = a.contextManager.Messages()
				} else {
					a.successDeclare.markUndelivered()
				}
			}
		}
		if cdMsg := a.criteriaDrift.maybeWarn(i + 1); cdMsg != "" {
			debug.Log("agent", "Iteration %d: success criteria drift detected", i+1)
			msgs = a.guidanceEmit(cdMsg, msgs)
		}

		if abMsg := a.attemptBrief.maybeBrief(i + 1); abMsg != "" {
			debug.Log("agent", "Iteration %d: injecting attempt brief", i+1)
			msgs = a.guidanceEmit(abMsg, msgs)
		}

		if scentMsg := a.infoScent.maybeWarn(i + 1); scentMsg != "" {
			msgs = a.guidanceEmit(scentMsg, msgs)
		}

		if qcMsg := a.queryConverge.maybeWarn(i + 1); qcMsg != "" {

			if a.injectGuidance(qcMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.queryConverge.markUndelivered()
			}
		}
		if bgOrphanMsg := a.maybeWarnBgOrphan(i + 1); bgOrphanMsg != "" {
			msgs = a.guidanceEmit(bgOrphanMsg, msgs)
		}

		if rrMsg := a.reasoningRedund.maybeWarn(i+1, a.maxIter); rrMsg != "" {
			debug.Log("reasoning-redund", "Iteration %d: reasoning redundancy detected -- consecutive text-only overthinking", i+1)

			if a.injectGuidance(rrMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.reasoningRedund.markUndelivered()
			}
		}

		if ipMsg := a.maybeWarnIterPressure(i + 1); ipMsg != "" {
			msgs = a.guidanceEmit(ipMsg, msgs)
		}

		if ptMsg := a.maybeWarnTermination(); ptMsg != "" {
			msgs = a.guidanceEmit(ptMsg, msgs)
		}

		if bsMsg := a.bareEditStreak.maybeWarn(i + 1); bsMsg != "" {

			if a.injectGuidance(bsMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.bareEditStreak.markUndelivered()
			}
		}

		if sfMsg := a.strategyFixation.check(); sfMsg != "" {
			msgs = a.guidanceEmit(sfMsg, msgs)
		}

		if erMsg := a.errorRush.check(); erMsg != "" {
			msgs = a.guidanceEmit(erMsg, msgs)
		}

		if afMsg := a.attentionFragment.analyze(); afMsg != "" {
			msgs = a.guidanceEmit(afMsg, msgs)
		}

		a.driftRecurrence.recordIteration(i + 1)

		if fcMsg := a.futileCycle.maybeWarn(i + 1); fcMsg != "" {

			if a.injectGuidance(fcMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.futileCycle.markUndelivered()
			}
		}

		if caMsg := a.constraintAmnesia.maybeWarn(i + 1); caMsg != "" {

			if a.injectGuidance(caMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.constraintAmnesia.markUndelivered()
			}
		}

		if a.delegationOrch != nil {

			if delegationOrphanGateEnabled {
				if delOrchMsg := a.delegationOrch.maybeWarnOrphanedDelegations(i + 1); delOrchMsg != "" {
					debug.Log("agent", "Iteration %d: delegation orphan gate injected guidance", i+1)
					msgs = a.guidanceEmit(delOrchMsg, msgs)
				}
			}
			if delegationSerialGateEnabled {
				if serialMsg := a.delegationOrch.maybeWarnSerialDelegation(); serialMsg != "" {
					debug.Log("agent", "Iteration %d: serial delegation gate injected guidance", i+1)
					msgs = a.guidanceEmit(serialMsg, msgs)
				}
			}
			if overDelMsg := a.delegationOrch.maybeWarnOverDelegation(); overDelMsg != "" {
				debug.Log("agent", "Iteration %d: over-delegation gate injected guidance", i+1)
				msgs = a.guidanceEmit(overDelMsg, msgs)
			}
		}

		if monorepoMsg := a.monorepoScoper.maybeWarnScopeSprawl(); monorepoMsg != "" {
			debug.Log("monorepo-scope", "package scope sprawl detected: %s", monorepoMsg)

			if a.injectGuidance(monorepoMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.monorepoScoper.markUndelivered()
			}
		}

		if a.maxIter >= 20 && !progressCheckInjected && i+1 >= a.maxIter*3/5 {
			progressCheckInjected = true
			debug.Log("agent", "Injecting mid-point progress checkpoint at iteration %d/%d", i+1, a.maxIter)

			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf(
					"Progress checkpoint: iteration %d/%d. Assess - on track? If not, switch strategy.",
					i+1, a.maxIter,
				)}},
			})
			msgs = a.contextManager.Messages()
		}

		effortApplied, effortPrev := a.applyAdaptiveEffort()

		cascadeSwapped := a.applyModelCascade()

		activeToolDefs := a.toolSearch.activeDefs(toolDefs)
		if a.serverToolSearch {

			markServerDeferred(activeToolDefs)
		}
		if cm, ok := a.contextManager.(interface{ SetToolDefinitionOverhead(int) }); ok {
			cm.SetToolDefinitionOverhead(estimateToolDefinitionOverhead(activeToolDefs))
		}

		msgs = a.contextManager.Messages()

		resp, textBuf, toolCalls, truncated, policyBlocked, reasoningChars, err := func() (*provider.ChatResponse, string, []provider.ToolCallDelta, bool, bool, int, error) {
			defer func() {
				if effortApplied != "" {
					a.restoreEffort(effortPrev)
				}
				if cascadeSwapped {
					a.restoreModelCascade()
				}
			}()
			return a.streamChatResponse(ctx, a.ensureMessagesSendable(msgs), activeToolDefs, onEvent)
		}()
		if err != nil {

			a.cascade.resetBatches()
			if errors.Is(err, errStreamInterruptedForReplan) {
				reactiveCompactRetries = 0
				agentLLMRetries = 0
				continue
			}
			if a.tryReactiveCompact(ctx, onEvent, err, &reactiveCompactRetries) {
				runStats.recordCompaction()
				continue
			}

			if isAgentRetryableLLMError(err) && agentLLMRetries < agentLLMRetryLimit() {
				agentLLMRetries++

				multiplier := 2
				errStr := strings.ToLower(err.Error())
				if strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "rate_limit") || strings.Contains(errStr, "too many") || strings.Contains(errStr, "overloaded") {
					multiplier = 5
				}
				delay := time.Duration(agentLLMRetries*multiplier) * time.Second
				debug.Log("agent", "transient LLM error (attempt %d/%d), retrying in %v: %v",
					agentLLMRetries, agentLLMRetryLimit(), delay, err)
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem,
					Text: fmt.Sprintf("[Retrying LLM call (%d/%d) after %v...] ",
						agentLLMRetries, agentLLMRetryLimit(), delay)})
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ctx.Err()})
					return ctx.Err()
				}
				continue
			}

			if strings.TrimSpace(textBuf) != "" {
				a.contextManager.Add(provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock(textBuf)},
				})
			}

			if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {

				cancelErr := ctx.Err()
				if cancelErr == nil {
					cancelErr = err
				}
				onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: cancelErr})
				return cancelErr
			}

			friendlyErr := provider.WrapFriendly(err)
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: friendlyErr})
			return friendlyErr
		}
		reactiveCompactRetries = 0
		agentLLMRetries = 0

		a.maybeSetAutopilotGoalFromLLMOutput(textBuf)

		runStats.recordTokens(resp.Usage.InputTokens, resp.Usage.OutputTokens)
		a.syncContextManagerUsage(resp.Usage)
		a.emitUsage(resp.Usage)

		if msg, stop := a.RecordSessionTokenUsage(int64(resp.Usage.InputTokens), int64(resp.Usage.OutputTokens)); msg != "" {
			a.crossDetectorConsensus.recordFiring("Session Token Budget", i+1)
			a.injectGuidance(msg)
			debug.Log("session-token-budget", "threshold crossed at iteration %d stop=%v", i+1, stop)
			if stop {
				sessionBudgetStop = true
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventSystem,
					Text: "[Session token budget fully consumed - winding down. Summarize the state so the user can resume with a fresh budget.] ",
				})
			}
		}

		if msg, stop := a.RecordSessionCostUsage(resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CacheRead, resp.Usage.CacheWrite); msg != "" {
			a.crossDetectorConsensus.recordFiring("Session Cost Limit", i+1)
			a.injectGuidance(msg)
			debug.Log("session-cost-limit", "threshold crossed at iteration %d stop=%v", i+1, stop)
			if stop {
				sessionBudgetStop = true
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventSystem,
					Text: "[Session spend limit fully consumed - winding down. Summarize the state so the user can resume with a fresh budget.] ",
				})
			}
		}

		if msg, stop := a.SessionTokenBudgetAlert(); msg != "" {
			a.crossDetectorConsensus.recordFiring("Session Token Budget", i+1)
			a.injectGuidance(msg)
			debug.Log("session-token-budget", "delegated threshold drained at iteration %d stop=%v", i+1, stop)
			if stop {
				sessionBudgetStop = true
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventSystem,
					Text: "[Session token budget fully consumed (including sub-agent spend) - winding down. Summarize the state so the user can resume with a fresh budget.] ",
				})
			}
		}

		if msg, stop := a.RecordSessionTimeUsage(); msg != "" {
			a.crossDetectorConsensus.recordFiring("Session Time Budget", i+1)
			a.injectGuidance(msg)
			debug.Log("session-time-budget", "threshold crossed at iteration %d stop=%v", i+1, stop)
			if stop {
				sessionBudgetStop = true
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventSystem,
					Text: "[Session time budget fully elapsed - winding down. Summarize the state so the user can resume with a fresh budget.] ",
				})
			}
		}

		if cacheGuidance := a.cacheEffMonitor.record(resp.Usage); cacheGuidance != "" {

			a.crossDetectorConsensus.recordFiring("Cache Efficiency", i+1)
			a.injectGuidance(cacheGuidance)
			debug.Log("cache-efficiency", "injecting cache bust storm guidance at iteration %d", i+1)
		}

		if resp.Usage.OutputTokens == 0 && len(toolCalls) == 0 &&
			strings.TrimSpace(textBuf) == "" && !hasNonThinkingContent(resp.Message.Content) {

			if policyBlocked {
				debug.Log("agent", "Iteration %d: response fully blocked by provider policy, not retrying", i+1)
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventSystem,
					Text: "[Response blocked by provider safety policy - not retrying. Try rephrasing the request.] ",
				})
				if len(resp.Message.Content) > 0 {
					a.contextManager.Add(resp.Message)
				}

				return fmt.Errorf("agent: response blocked by provider safety filter (task did not complete)")
			}
			consecutiveEmptyResponses++
			debug.Log("agent", "Iteration %d: empty response detected (consecutive=%d, input_tokens=%d)",
				i+1, consecutiveEmptyResponses, resp.Usage.InputTokens)
			if consecutiveEmptyResponses >= 3 {
				debug.Log("agent", "too many consecutive empty responses (%d), aborting", consecutiveEmptyResponses)

				onEvent(provider.StreamEvent{
					Type: provider.StreamEventText,
					Text: "[run aborted - the model returned 3 consecutive empty responses; the task did not complete]\n",
				})
				return fmt.Errorf("agent: aborted after %d consecutive empty responses", consecutiveEmptyResponses)
			}

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

		if a.trajectoryHealth != nil {
			eCnt, rCnt := countToolTypes(toolCalls)
			assCnt := 0
			a.trajectoryHealth.recordIteration(eCnt, 0, len(toolCalls), rCnt, assCnt)
		}

		if len(toolCalls) == 0 {

			if truncated && !policyBlocked && truncationContinues < 3 {
				truncationContinues++
				debug.Log("agent", "Iteration %d: response truncated by output limit, auto-continuing (attempt %d/3)", i+1, truncationContinues)
				a.contextManager.Add(resp.Message)
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventSystem,
					Text: "[Response was truncated by output length limit - continuing...] ",
				})

				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: "Your previous response was cut off by the output token limit. Continue from where you left off - do not repeat what you already wrote.",
					}},
				})
				continue
			}
			if truncated && policyBlocked {

				debug.Log("agent", "Iteration %d: response blocked by provider policy, skipping auto-continuation", i+1)
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventSystem,
					Text: "[Response blocked by provider safety policy - partial output kept, not retrying.] ",
				})
			} else if truncated && !policyBlocked && truncationContinues >= 3 {

				debug.Log("agent", "Iteration %d: response truncated, continuation budget exhausted (3/3), keeping partial output", i+1)
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventSystem,
					Text: "[Output truncated - auto-continuation budget exhausted (3/3); response may be incomplete.] ",
				})
			}

			assistantText := textBuf
			a.constraintViolation.recordReasoning(assistantText, i+1)
			a.reasoningRedund.recordReasoning(assistantText, false)
			a.recordGiveupText(assistantText)

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

			if fpMsg := a.falsePremise.checkFalsePremise(assistantText); fpMsg != "" {
				debug.Log("agent", "Iteration %d: false premise detected (ungrounded success claim)", i+1)
				a.recordUncertainty("false_premise", weightFalsePremise)
				a.injectGuidance(fpMsg)
			}

			if rvMsg := a.reverify.checkStaleTrap(assistantText, i+1); rvMsg != "" {
				debug.Log("agent", "Iteration %d: stale-observation reverify escalation triggered", i+1)
				a.recordUncertainty("stale_obs_trapped", weightStaleObs)
				a.injectGuidance(rvMsg)
			}

			a.integrationCheckAndWarn(assistantText)

			if a.claimsSupervision {
				if psHint := a.prematureSuccess.checkSuccessClaim(assistantText); psHint != "" {
					debug.Log("agent", "Iteration %d: premature success claim detected (edits without verification)", i+1)
					a.injectGuidance(psHint)

					a.recordUncertainty("unverified_success", weightUnverifiedSucc)
				}
			}

			if a.claimsSupervision {
				if pvHint := a.maybeWarnPhantomVerify(assistantText); pvHint != "" {
					debug.Log("agent", "Iteration %d: phantom verification detector found unverified category claims", i+1)
					a.injectGuidance(pvHint)
				}
			}

			a.successDeclare.recordAssistantText(assistantText, i)
			a.criteriaDrift.recordAssistantText(assistantText, i)
			a.subgoalTrack.recordAssistantText(assistantText, i)

			runStats.recordAssistantText(assistantText)

			detectorMsgsBefore := len(a.contextManager.Messages())
			if truncHint := a.truncClaim.maybeWarnTruncClaim(assistantText, i); truncHint != "" {
				debug.Log("agent", "Iteration %d: truncated output completeness fallacy detected", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: truncHint,
					}},
				})
			}

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

			if planHint := a.maybeWarnPlanAbandon(assistantText, runStats); planHint != "" {
				debug.Log("agent", "Iteration %d: plan abandonment detector triggered (declared %d steps)", i+1, len(a.planAbandon.declaredSteps))
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: planHint,
					}},
				})
			}

			reproToolNames, reproToolInputs := extractToolNamesAndInputs(toolCalls)

			reproRan := false
			reproRunInput := ""
			for ti, tn := range reproToolNames {
				if reproducerRunToolNames[tn] {
					reproRan = true
					if ti < len(reproToolInputs) {
						reproRunInput = reproToolInputs[ti]
					}
					break
				}
			}
			a.reproducerLifecycle.observeText(i+1, assistantText, reproRan, reproRunInput)
			a.reproducerLifecycle.observeToolCalls(i+1, reproToolNames, reproToolInputs)
			if rlHint := a.reproducerLifecycle.checkIncomplete(i + 1); rlHint != "" {
				debug.Log("agent", "Iteration %d: reproducer lifecycle detector triggered", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: rlHint,
					}},
				})
			}

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

			if raHint := a.maybeWarnReasonAction(assistantText, toolCalls); raHint != "" {
				debug.Log("agent", "Iteration %d: reasoning-action alignment verifier triggered", i+1)
				a.injectGuidance(raHint)
			}

			a.mindlessAction.recordStep(len(assistantText), false)

			if healthHint := a.maybeWarnTrajectoryHealth(); healthHint != "" {
				debug.Log("agent", "Iteration %d: trajectory health synthesizer detected composite degradation", i+1)
				a.injectGuidance(healthHint)
			}

			if wasteHint := a.maybeWarnTokenWaste(); wasteHint != "" {
				debug.Log("agent", "Iteration %d: token waste budget exceeded 40%% threshold", i+1)
				a.injectGuidance(wasteHint)
			}

			a.foresightCalib.recordPrediction(assistantText, toolCalls, i+1)

			if epHint := a.foresightCalib.expectProtocolHint(toolCalls); epHint != "" {
				a.injectGuidance(epHint)
			}

			if gdHint := a.goalDriftCtx.checkDrift(i + 1); gdHint != "" {
				debug.Log("agent", "Iteration %d: context-length goal drift detected", i+1)
				a.injectGuidance(gdHint)
			}

			if len(a.contextManager.Messages()) > detectorMsgsBefore && detectorContinueBudget > 0 {
				detectorContinueBudget--
				debug.Log("agent", "Iteration %d: detector guidance injected; continuing run to consume it (#4650)", i+1)
				continue
			}
			if injected, kind := a.injectPendingInterruptions(i + 1); injected {
				a.recordIntervention(i + 1)

				if a.maxIter > 0 && i == a.maxIter-1 {
					guidanceAtBudget = true
				}

				if kind == InterruptionRetractStop {
					debug.Log("agent", "retraction-stop: aborting run after user withdrew the task")
					return context.Canceled
				}
				continue
			}

			if !a.suppressAutopilotInjection && a.currentMode().IsCruiseMode() && a.hasAutopilotGoal() && a.autopilotStrategistCount < maxAutopilotStrategistCalls {
				a.strategistNoProgressCount++
				a.autopilotStrategistCount++

				if a.strategistNoProgressCount >= maxConsecutiveStrategistNoProgress {
					debug.Log("agent", "Iteration %d: autopilot force-terminate after %d consecutive no-progress rounds", i+1, a.strategistNoProgressCount)
					preview := textBuf
					if len(preview) > 200 {

						preview = strings.ToValidUTF8(preview[:200], "")
					}
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Autopilot: agent idle for %d consecutive rounds - terminating to avoid deadlock. Last output: %s]", a.strategistNoProgressCount, preview)})
					a.ClearAutopilotGoal()

					if codeChangedInRun(runStats) {
						asyncVerifyStats = runStats
					}
					return nil
				}
				debug.Log("agent", "Iteration %d: autopilot calling strategist (call #%d/%d, no-progress=%d/%d)", i+1, a.autopilotStrategistCount, maxAutopilotStrategistCalls, a.strategistNoProgressCount, maxConsecutiveStrategistNoProgress)
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist #%d/%d: analyzing conversation and deciding next steps...] ", a.autopilotStrategistCount, maxAutopilotStrategistCalls)})
				result, sErr := a.runAutopilotStrategist(ctx, textBuf)
				if sErr != nil {
					debug.Log("agent", "autopilot strategist failed: %v", sErr)
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist unavailable (%v) - autopilot stopping]", sErr)})

				} else if result.Complete {
					debug.Log("agent", "Iteration %d: strategist declared goal achieved", i+1)
					summary := result.Guidance

					summary = stripStrategistCompleteMarker(summary)
					msg := "[Strategist: goal achieved - autopilot complete.]"
					if summary != "" {
						msg = fmt.Sprintf("[Strategist: goal achieved - autopilot complete. %s]", summary)
					}
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: msg})
					a.ClearAutopilotGoal()

					if codeChangedInRun(runStats) {
						asyncVerifyStats = runStats
					}
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

					debug.Log("agent", "Iteration %d: strategist returned empty guidance; continuing autonomously", i+1)
					onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Strategist returned no guidance - continuing autonomously]"})
					a.contextManager.Add(provider.Message{
						Role: "user",
						Content: []provider.ContentBlock{{
							Type: "text",
							Text: "Continue the task autonomously: check remaining work, run build/tests to verify, then finish and summarize what was completed.",
						}},
					})
					continue
				}
			} else if a.currentMode().IsCruiseMode() && a.hasAutopilotGoal() && !a.strategistBudgetAnnounced {

				a.strategistBudgetAnnounced = true
				debug.Log("agent", "Iteration %d: strategist budget exhausted (%d/%d), injecting one-time continuation guidance", i+1, a.autopilotStrategistCount, maxAutopilotStrategistCalls)
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist budget at limit (%d/%d) - continuing autonomously]", a.autopilotStrategistCount, maxAutopilotStrategistCalls)})
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: "Strategist budget exhausted. Continue remaining tasks autonomously - build, test, verify, then summarize.",
					}},
				})
				continue
			}

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

			if driftMsg := a.planDrift.checkPlanDrift(runStats, textBuf); driftMsg != "" {
				debug.Log("agent", "Iteration %d: plan drift detected, injecting reminder", i+1)

				a.driftRecurrenceMarkWarn(runStats.Iterations)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: driftMsg,
					}},
				})
				continue
			}

			a.constraintAudit.observe(userPromptForStats)
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

			if wf := a.workflowEngineLazy(); wf != nil {
				if wfMsg := wf.outstandingMessageOnce(); wfMsg != "" {
					debug.Log("agent", "Iteration %d: workflow-spec audit injected", i+1)
					a.contextManager.Add(provider.Message{
						Role: "user",
						Content: []provider.ContentBlock{{
							Type: "text",
							Text: wfMsg,
						}},
					})

					continue
				}
			}

			if auditMsg := a.constraintAudit.checkAndInject(); auditMsg != "" {
				debug.Log("agent", "Iteration %d: constraint audit injected", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: auditMsg,
					}},
				})
				continue
			}

			if fuMsg := a.constraintFollowup.checkAndInject(textBuf, a.constraintAudit); fuMsg != "" {
				debug.Log("agent", "Iteration %d: constraint followup injected (round %d)", i+1, a.constraintFollowup.currentRound())
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: fuMsg,
					}},
				})
				continue
			}

			a.mu.Lock()
			gateMsg := finalTurnEvidenceGate(
				a.postEditVerify.sourceEditsThisRun,
				a.postEditVerify.lastSourceFileThisRun,
				a.postEditVerify.realBuildOrTestRunThisRun,
				a.postEditVerify.finalGateFiredThisRun,
				a.workingDir)
			if gateMsg != "" {
				a.postEditVerify.finalGateFiredThisRun = true
			}
			a.mu.Unlock()
			if gateMsg != "" {
				debug.Log("agent", "Iteration %d: final-turn evidence gate fired, demanding verification before stop", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: gateMsg,
					}},
				})
				continue
			}

			if retraceShouldCheck(a.postEditVerify, a.overseer.researchMode, a.retraceGate.firedThisRun) {
				a.retraceGate.firedThisRun = true
				if diff := uncommittedDiff(a.workingDir); strings.TrimSpace(diff) != "" {
					if msg := a.runRetraceVerification(ctx, userPromptForStats, diff); msg != "" {
						debug.Log("agent", "Iteration %d: retrace gate verdict non-aligned, injecting revision guidance", i+1)
						a.contextManager.Add(provider.Message{
							Role: "user",
							Content: []provider.ContentBlock{{
								Type: "text",
								Text: msg,
							}},
						})
						continue
					}
				}
			}

			a.mu.Lock()
			researchMsg := researchReportGate(
				a.overseer.researchMode,
				a.overseer.searchCalls+a.overseer.fetchCalls,
				researchReportGateMinCalls,
				a.overseer.reportGateFired)
			if researchMsg != "" {
				a.overseer.reportGateFired = true
			}
			a.mu.Unlock()
			if researchMsg != "" {

				if draft := SynthesizeResearchReport("", a.overseer.findingsSnapshot(), a.researchPlanSubs); draft != "" {
					researchMsg = draft + "\n\n" + researchMsg
				}
				debug.Log("agent", "Iteration %d: research-report gate fired, demanding structured synthesis before stop", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: researchMsg,
					}},
				})
				continue
			}

			if a.claimsSupervision {
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
			}

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

			syncPassed := false
			if codeChangedInRun(runStats) && a.currentMode() != permission.PlanMode && ctx.Err() == nil {

				if shouldContinue, passed := a.syncVerifyAndGate(ctx, runStats, syncVerifyRetries); shouldContinue {
					syncVerifyRetries++
					debug.Log("agent", "Iteration %d: sync verify failed, auto-repairing (retry %d/%d)", i+1, syncVerifyRetries, maxSyncVerifyRetries)
					continue
				} else if passed {
					syncPassed = true
				}
			}
			if syncPassed {
				debug.Log("agent", "sync verify passed, skipping async verify")
			} else {

				asyncVerifyStats = runStats
			}

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

			if evalMsg := a.checkAdversarialReviewGate(ctx, runStats, userPromptForStats); evalMsg != "" {
				debug.Log("agent", "Iteration %d: adversarial evaluator returned findings", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: evalMsg,
					}},
				})
				continue
			}

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

			if goalMsg := a.checkGoalFinishGate(); goalMsg != "" {
				debug.Log("agent", "Iteration %d: goal finish gate found dangling in_progress leaves", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: goalMsg,
					}},
				})
				continue
			}

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

			if docDriftMsg := a.checkDocDriftGate(runStats); docDriftMsg != "" {
				debug.Log("agent", "Iteration %d: doc-drift advisory injected", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: docDriftMsg,
					}},
				})
				continue
			}

			if draftPRMsg := a.checkDraftPRHintGate(runStats); draftPRMsg != "" {
				debug.Log("agent", "Iteration %d: draft-PR hint gate injected reminder", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: draftPRMsg,
					}},
				})
				continue
			}

			if d := a.oversightTriage.digest(); d != "" {
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: d})
			}

			if d := a.autonomyDial.suggest(); d != "" {
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: d})
			}
			debug.Log("agent", "Iteration %d: no tool calls, returning", i+1)
			return nil
		}
		debug.Log("agent", "Iteration %d: tool_calls=%d", i+1, len(toolCalls))
		a.contextManager.Add(resp.Message)

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

		a.strategistNoProgressCount = 0
		var toolResults []provider.ContentBlock
		var followUpMessages []provider.Message
		var deferredMemoryContent string
		var deferredMemoryFiles []string
		var deferredMemoryTarget string

		preExecuted := a.preExecuteReadOnlyTools(ctx, toolCalls)

		waitPreExecuted := a.preExecuteWaitTools(ctx, toolCalls)

		batchConflictWarnings := detectBatchEditConflicts(toolCalls)
		type dedupKey struct {
			tool string
			args string
		}
		seenReadOnly := make(map[dedupKey]int)

		if depWarn := a.recordToolCallBatch(toolCalls, i+1); depWarn != "" {
			debug.Log("agent", "Iteration %d: counterfactual dependency assumption detected", i+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: depWarn}},
			})
			msgs = a.contextManager.Messages()
		}

		if len(toolCalls) > 1 {
			var batchInfos []couplingToolCall
			for _, tc := range toolCalls {
				batchInfos = append(batchInfos, couplingToolCall{name: tc.Name, args: tc.Arguments})
			}
			if couplingWarn := a.batchCoupling.checkBatchCoupling(batchInfos); couplingWarn != "" {
				debug.Log("agent", "Iteration %d: batch tool call coupling detected", i+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: couplingWarn}},
				})
				msgs = a.contextManager.Messages()
			}
		}

		if a.irrevGate != nil {
			for _, tc := range toolCalls {
				if warn := a.irrevGate.recordAction(tc.Name, string(tc.Arguments)); warn != "" {
					a.contextManager.Add(provider.Message{
						Role:    "user",
						Content: []provider.ContentBlock{{Type: "text", Text: warn}},
					})
				}
			}
		}

		if hint := a.maybeToolflowSuggestion(a.runToolNames); hint != "" {
			debug.Log("agent", "injected toolflow next-step hint (%d chars)", len(hint))
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: hint}},
			})
		}

		a.cascade.beginBatch()

		for _, tc := range toolCalls {
			a.cascade.notePlanned(tc.Name)
		}

		if a.mindlessAction.recordStep(len(textBuf)+reasoningChars, true) {
			debug.Log("agent", "Iteration %d: mindless action detector triggered (streak=%d)", i+1, a.mindlessAction.streak)
			a.injectGuidance(mindlessActionWarning(a.mindlessAction.streak))
		}
		for idx, tc := range toolCalls {
			if err := ctx.Err(); err != nil {

				a.fillCancelledToolResults(toolCalls[idx:], &toolResults)
				return err
			}
			var undoBlindHint string
			if ub := a.undoBlind.recordToolCall(tc.Name, tc.Arguments); ub != "" {
				debug.Log("agent", "Iteration %d: undo-blind mutation detected (pre-execution)", i+1)
				undoBlindHint = ub
			}

			a.orphanFile.recordPreExec(tc.Name, string(tc.Arguments), a.workingDir)
			var loopGuidance string
			if guidance := a.loopDetectionInjection(tc); guidance != "" {
				loopGuidance = guidance
			}
			var searchParamHint string
			if hint := a.searchParamGuard.checkParamQuality(tc.Name, tc.Arguments); hint != "" {
				searchParamHint = hint
			}

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

			a.reversibility.recordSafetySignal(tc.Name, string(tc.Arguments))
			var redundancyHint string
			if hint := a.toolRedundancy.recordCall(tc.Name, tc.Arguments); hint != "" {
				redundancyHint = hint
			}
			var equivHint string
			rawFp := fingerprintToolCall(tc.Name, tc.Arguments)
			if hint := a.toolEquivDetect.recordCall(tc.Name, tc.Arguments, rawFp); hint != "" {
				equivHint = hint
			}

			if mc, mf, mt := a.pendingProjectMemoryForTool(tc); len(mf) > 0 && strings.TrimSpace(mc) != "" {
				if deferredMemoryContent == "" {
					deferredMemoryContent = mc
					deferredMemoryFiles = mf
					deferredMemoryTarget = mt
				}
			}

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

			askAdvisory := ""
			if tc.Name == "ask_user" {
				if blockMsg, advMsg := a.checkAskQualityGate(string(tc.Arguments)); blockMsg != "" {
					toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, blockMsg, true))
					onEvent(provider.StreamEvent{
						Type:    provider.StreamEventToolResult,
						Tool:    tc,
						Result:  blockMsg,
						IsError: true,
					})
					continue
				} else if advMsg != "" {
					askAdvisory = advMsg
				}
			}

			if redactWarn := checkRedactedInWrite(tc.Name, string(tc.Arguments)); redactWarn != "" {
				toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, redactWarn, true))
				onEvent(provider.StreamEvent{
					Type:    provider.StreamEventToolResult,
					Tool:    tc,
					Result:  redactWarn,
					IsError: true,
				})
				continue
			}

			a.runToolNames = append(a.runToolNames, tc.Name)

			runStats.recordToolCall(tc.Name)
			a.toolCallBudget.record()
			a.toolThermal.recordToolCall(tc.Name)
			a.iterPressure.recordToolCall(tc.Name, i+1)
			extractPathsFromToolCall(tc.Name, tc.Arguments, runStats)
			var result tool.Result
			memoHit := false
			if memoResult, hit := a.toolMemo.get(tc.Name, tc.Arguments); hit && a.speculativeHitAllowed(ctx, tc) {
				result = memoResult
				memoHit = true

				if result.Content != "" && !result.IsError {
					result.Content = fmt.Sprintf("[cached - %s returned identical content since your last call]\n%s", tc.Name, result.Content)
				}
				debug.Log("memoize", "memo hit for %s (saved tool execution)", tc.Name)
			} else if cachedResult, hit := a.speculator.getCached(tc.Name, tc.Arguments); hit && a.speculativeHitAllowed(ctx, tc) {

				result = cachedResult
				debug.Log("speculate", "speculative cache hit for %s (saved tool execution)", tc.Name)
			} else if pre, ok := preExecuted[idx]; ok {

				result = a.usePreExecutedWithPermission(ctx, tc, pre)
			} else if pre, ok := waitPreExecuted[idx]; ok {

				result = a.usePreExecutedWithPermission(ctx, tc, pre)
			} else if cmdCached, hit := a.checkCommandCache(tc.Name, tc.Arguments); hit && a.speculativeHitAllowed(ctx, tc) {

				result = cmdCached
			} else {
				result = a.executeToolWithPermission(ctx, tc)

				a.storeCommandResult(tc.Name, tc.Arguments, result)

				if adv := a.checkoutAdvisory(tc.Name, tc.Arguments); adv != "" {
					result.Content += adv
				}
				if hint := a.recordEffectAttempt(tc.Name, tc.Arguments, result); hint != "" {
					result.Content += hint
				}
			}

			a.exfilChain.recordSensitiveSource(tc.Name, string(tc.Arguments), result.Content)
			result.Content = redactSecrets(tc.Name, result.Content)

			a.overseer.recordFinding(tc.Name, string(tc.Arguments), result.Content)

			a.speculator.recordObservation(tc.Name)

			if tc.Name == "todo_write" && !result.IsError {
				a.plannerMarkTodoCreated()

				todoCount := parseTodoCount(tc.Arguments)
				a.recordTodoStalenessUpdate(i+1, todoCount)

				if hint := a.checkTodoDrop(tc.Arguments); hint != "" {
					a.appendGuidance(&result, hint)
				}
			}

			if tc.Name == "ask_user" && !result.IsError {
				a.markAskAskedQG(string(tc.Arguments))
			}

			a.termGuard.recordToolStep(result.IsError)
			wroteDespiteError := partialEditWroteDespiteError(tc.Name, result.Content, result.IsError)
			if mutatesSourceTree(tc.Name) && (!result.IsError || wroteDespiteError) {
				a.speculator.invalidateCache()

				if gitWholeTreeTools[tc.Name] {
					a.toolMemo.invalidateAll()
					debug.Log("agent", "whole-tree git operation %s: invalidated all caches", tc.Name)
				} else {

					a.toolMemo.invalidateTTLBased()
				}

				a.commandCache.invalidate()

				for _, p := range extractCreateFilePaths(tc.Name, tc.Arguments) {
					a.unreadEdit.recordCreated(p)
					a.tunnelVision.recordFile(p)
					a.fileFreshness.recordWrite(p)
					a.readHash.recordWriteHash(p)
				}

				a.recurringErrorRecordEdit()

				if a.codeIndex != nil {
					a.codeIndex.MarkDirty(extractEditedPaths(tc))
				}
			}

			if tc.Name == "run_command" || tc.Name == "start_command" {
				if cmd, _ := parseRunCommandArgs(tc.Arguments); shellMutatesSources(cmd) {
					a.speculator.invalidateCache()
					a.toolMemo.invalidateTTLBased()
					a.commandCache.invalidate()

					a.redundantReverify.recordShellSourceMutation()

					a.getUserEditObserver().RestampBaseline()
					debug.Log("agent", "shell source mutation %q (failed cmd included): invalidated command/speculator/memo caches", cmd)
				}
			}

			if speculativeSafeTools[tc.Name] && !result.IsError && !memoHit {
				a.toolMemo.put(tc.Name, tc.Arguments, result)
			}

			if (tc.Name == "read_file" || tc.Name == "multi_file_read") && !result.IsError {

				readPathsLen := len(extractReadFilePaths(tc.Name, tc.Arguments))

				hasWindow := readArgsHaveWindow(tc.Arguments)
				for pi, p := range extractReadFilePaths(tc.Name, tc.Arguments) {
					a.unreadEdit.recordReadWindow(p, hasWindow)
					a.tunnelVision.recordFile(p)
					a.editFailRecovery.recordRead(p)
					a.fileFreshness.recordRead(p)
					a.readHash.recordReadHash(p)
					if hint := a.redundantRead.checkRedundantRead(p, hasWindow); hint != "" {
						a.appendGuidance(&result, hint)
					}
					if pi == readPathsLen-1 {
						if hint := a.patchExhaust.recordRead(p); hint != "" {
							a.appendGuidance(&result, hint)
						}
					}
				}
			}

			if searchResultTools[tc.Name] && !result.IsError {
				for _, p := range extractSearchResultPaths(tc.Name, result.Content) {
					a.tunnelVision.recordSearched(p)
				}
			}

			if !result.IsError && fileEditingTools[tc.Name] {

				a.prematureRefactorRecordEdit(tc.Name, tc.Arguments)
				for _, p := range extractEditFilePaths(tc.Name, tc.Arguments) {
					a.editFailRecovery.recordEditSuccess(p)

					if hint := a.readHash.validateContentAtEdit(p, extractOldTextLen(tc.Name, tc.Arguments)); hint != "" {
						a.appendGuidance(&result, hint)
					}
					a.fileFreshness.recordWrite(p)
					a.readHash.recordWriteHash(p)
					a.redundantRead.recordWrite(p)

					a.unreadEdit.recordWrite(p)
					if a.tokenWasteBudget != nil {
						a.tokenWasteBudget.markFileEdited(p)
					}
					a.patchExhaust.recordEdit(p)

					a.convergenceRecordEdit(tc.Name)

					a.diminishingRecordEdit(tc.Name, tc.Arguments)

					if a.claimsSupervision {
						if ocHint := a.overcorrectionRecordEdit(tc.Name, tc.Arguments); ocHint != "" {
							a.appendGuidance(&result, ocHint)
						}
					}

					a.fixCascade.recordEdit()

					a.errRegression.recordEdit()
					if hint := a.unreadEdit.checkUnreadEdit(p); hint != "" {
						a.appendGuidance(&result, hint)
					}

					if hint := a.unreadEdit.checkStaleRead(p); hint != "" {
						a.appendGuidance(&result, hint)
					}

					if hint := a.expiredRead.recordEdit(p); hint != "" {
						a.appendGuidance(&result, hint)
					}

					if tc.Name == "undo_edit" && !result.IsError {
						a.expiredRead.recordUndo(p)

						a.getUserEditObserver().NoteAgentUndo(p)
					}

					if hint := a.checkExportGuard(p); hint != "" {
						a.appendGuidance(&result, hint)
					}

					if hint := a.checkHubPackage(p); hint != "" {
						a.appendGuidance(&result, hint)
					}

					if hint := a.artifactGuard.checkGeneratedArtifact(p); hint != "" {
						a.appendGuidance(&result, hint)
					}

					if hint := a.checkBranchGuard(); hint != "" {
						a.appendGuidance(&result, hint)
					}
				}
			}

			if result.IsError && fileEditingTools[tc.Name] {
				a.convergenceRecordEditError()
				for _, p := range extractEditFilePaths(tc.Name, tc.Arguments) {
					if hint := a.editFailRecovery.recordEditFailure(p); hint != "" {
						a.appendGuidance(&result, hint)
					}
				}
			}

			a.overcorrectionRecordError(tc.Name, result.Content, result.IsError)

			if !result.IsError && giveupRollbackTools[tc.Name] {
				a.recordGiveupRollback()
			}

			a.falsePremise.recordToolResult(tc.Name, result.Content, result.IsError)
			a.integrationRecordToolResult(tc.Name, result.Content)
			result.Content = a.injectRulesIntoResult(tc.Name, tc.Arguments, result.Content)

			if warn, ok := batchConflictWarnings[idx]; ok {
				a.appendGuidance(&result, warn)
			}
			if result.IsError {
				debug.Log("agent", "tool result ERROR: tool=%s output=%s", tc.Name, util.Truncate(result.Content, 200))
			}

			if !isEditTool(tc.Name) {
				a.overcorrection.recordNonEditStep()
			}

			a.capBoundary.recordToolResult(result.IsError)

			if argSizeHint := a.checkArgSizeGuard(tc.Name, tc.Arguments); argSizeHint != "" {
				a.appendGuidance(&result, argSizeHint)
			}

			if seqHint := a.toolSequence.record(tc, i+1); seqHint != "" {
				a.appendGuidance(&result, seqHint)
			}

			if ocHint := a.obsContract.checkArgs(tc.Name, tc.Arguments, i+1); ocHint != "" {
				a.appendGuidance(&result, ocHint)
			}
			a.obsContract.recordResult(tc.Name, result.Content, i+1)

			a.oversightTriage.record(tc)

			a.autonomyDial.record(result.IsError, result.Content)

			a.mu.Lock()
			a.lastExecutedTool = tc.Name
			a.mu.Unlock()
			if ivHint := a.interventionLedger.hint(tc.Name); ivHint != "" {
				a.appendGuidance(&result, ivHint)
			}

			a.recordBgToolCall(tc.Name, tc.Arguments, result.Content, i+1)

			if !result.IsError {
				if annihilWarn := a.actionAnnihil.recordToolCall(tc.Name, tc.Arguments, i+1); annihilWarn != "" {
					debug.Log("agent", "Iteration %d: action annihilation detected", i+1)
					a.contextManager.Add(provider.Message{
						Role:    "user",
						Content: []provider.ContentBlock{{Type: "text", Text: annihilWarn}},
					})
					msgs = a.contextManager.Messages()
				}

				if tc.Name == "git_commit" {
					if h := extractNewCommitHash(result.Content); h != "" {
						a.actionAnnihil.recordCommitHash(i+1, h)
					}
				}
			}

			fragWarn := ""
			if !result.IsError || mutatingToolNamesFrag[tc.Name] {
				fragWarn = a.exploreFrag.recordToolCall(tc.Name, tc.Arguments, i+1)
			}
			if fragWarn != "" {
				debug.Log("agent", "Iteration %d: exploration fragmentation detected", i+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: fragWarn}},
				})
				msgs = a.contextManager.Messages()
			}

			if !result.IsError {
				if orphanWarn := a.orphanFile.recordToolCall(tc.Name, string(tc.Arguments), i+1); orphanWarn != "" {
					debug.Log("agent", "Iteration %d: orphaned file detected", i+1)
					a.contextManager.Add(provider.Message{
						Role:    "user",
						Content: []provider.ContentBlock{{Type: "text", Text: orphanWarn}},
					})
					msgs = a.contextManager.Messages()
				}
			}

			if idempWarn := a.buildIdempot.recordToolCall(tc.Name, tc.Arguments, i+1); idempWarn != "" {
				debug.Log("agent", "Iteration %d: build idempotency violation detected", i+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: idempWarn}},
				})
				msgs = a.contextManager.Messages()
			}

			if taintWarn := a.taintInfluence.checkInfluence(tc.Name, string(tc.Arguments)); taintWarn != "" {
				debug.Log("agent", "Iteration %d: tainted data influence detected on %s", i+1, tc.Name)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: taintWarn}},
				})
				msgs = a.contextManager.Messages()
			}

			if pseudoWarn := a.taintInfluence.checkPseudoDirectiveArgs(tc.Name, string(tc.Arguments)); pseudoWarn != "" {
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: pseudoWarn}},
				})
				msgs = a.contextManager.Messages()
			}

			if exfilWarn := a.exfilChain.checkExfilChain(tc.Name, string(tc.Arguments)); exfilWarn != "" {
				debug.Log("agent", "Iteration %d: exfiltration chain detected on %s", i+1, tc.Name)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: exfilWarn}},
				})
				msgs = a.contextManager.Messages()
			}

			a.serialRead.recordToolCall(tc.Name)
			a.reasoningRedund.recordReasoning("", true)

			if covWarn := a.editCoverage.recordToolCall(tc.Name, string(tc.Arguments)); covWarn != "" {
				debug.Log("agent", "Iteration %d: verification coverage gap detected", i+1)

				if cmd := extractCommandFromToolCall(tc.Arguments); cmd != "" {
					a.scopeNarrow.lastCoverageWarnedCmd = cmd
				}
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: covWarn}},
				})
				msgs = a.contextManager.Messages()
			}

			if effGuidance := a.toolEff.recordCall(tc.Name, result.Content, result.IsError); effGuidance != "" {
				debug.Log("agent", "Iteration %d: tool effectiveness guidance for %s", i+1, tc.Name)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: effGuidance}},
				})
				msgs = a.contextManager.Messages()
			}

			if result.IsError {
				a.usageHints.recordFailure(tc.Name, result.Content)
			} else {
				a.usageHints.recordSuccess(tc.Name)
			}

			if rvHint := a.redundantReverify.recordToolCall(tc.Name, string(tc.Arguments), i+1, result.IsError); rvHint != "" {
				debug.Log("agent", "Iteration %d: redundant re-verification detector triggered", i+1)
				a.injectGuidance(rvHint)
			}

			a.outcomeMisattrib.recordToolCallForOM(tc.Name)

			a.outcomeMisattrib.recordResult(tc.Name, result.Content, result.IsError, i+1)

			a.reverify.recordToolResult(tc.Name, string(tc.Arguments), result.Content, result.IsError, i+1)

			a.goalDriftCtx.recordToolCall(tc.Name, string(tc.Arguments))

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

			a.errStrategyLoop.recordResult(result.Content, result.IsError)

			if seMsg := a.strategyExhaustion.recordToolCall(tc.Name, result.IsError, result.Content, i+1); seMsg != "" {
				debug.Log("agent", "Iteration %d: strategy exhaustion detector triggered", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: seMsg,
					}},
				})
			}

			a.solutionFixation.recordToolCall(tc.Name, string(tc.Arguments), result.IsError)

			a.pivotDecision.recordToolCall(tc.Name, string(tc.Arguments), result.IsError, result.Content)

			if !result.IsError {
				a.redundantReverify.recordEdit(tc.Name)
			}
			if fixationHint := a.solutionFixation.checkAndWarn(); fixationHint != "" {
				debug.Log("agent", "Iteration %d: solution fixation detector triggered", i+1)
				a.injectGuidance(fixationHint)
			}
			if pivotHint := a.pivotDecision.checkAndWarn(); pivotHint != "" {
				debug.Log("agent", "Iteration %d: pivot-decision detector triggered", i+1)
				a.injectGuidance(pivotHint)
			}

			if strategyHint := a.errStrategyLoop.checkAndWarn(); strategyHint != "" {
				debug.Log("agent", "Iteration %d: error strategy loop detector triggered", i+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: strategyHint,
					}},
				})

				if recallHint := a.maybeRecallExperienceOnFailure(userPromptForStats, result.Content); recallHint != "" {
					debug.Log("agent", "injected decision-time experience recall (%d chars)", len(recallHint))
					a.contextManager.Add(provider.Message{
						Role:    "user",
						Content: []provider.ContentBlock{{Type: "text", Text: recallHint}},
					})
				}
			}

			if selfModMsg := a.checkSelfModification(tc.Name, tc.Arguments); selfModMsg != "" {
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: selfModMsg}},
				})
				msgs = a.contextManager.Messages()
			}

			a.infoScent.recordExploration(tc.Name, string(tc.Arguments), result.Content, i+1)

			a.queryConverge.recordToolCall(tc.Name, string(tc.Arguments), i+1)

			if tc.Name == "exit_plan_mode" {
				a.planDrift.capturePlan(extractPlanFromArgs(tc.Arguments))
			}

			if a.delegationOrch != nil {
				if delegationToolNames[tc.Name] {
					taskSum := extractDelegationTaskSummary(tc.Name, tc.Arguments)
					a.delegationOrch.recordDelegationCall(tc.ID, tc.Name, taskSum, result.Content, i+1)
				} else if delegationResultTools[tc.Name] && !result.IsError {

					a.delegationOrch.recordResultCheck(tc.Name, tc.Arguments, result.Content, i+1)
				}
				a.delegationOrch.recordToolCallCount()
			}

			if result.IsError {
				runStats.recordToolError(tc.Name, result.Content)
			} else if userWriteTools[tc.Name] {

				for _, p := range extractUserEditPaths(tc.Name, tc.Arguments) {
					a.getUserEditObserver().NoteAgentWrite(p)
				}
			}

			if result.IsError {
				rKey := extractErrorResourceKey(tc.Name, tc.Arguments)
				a.silentError.recordToolError(tc.Name, rKey, result.Content, i+1)
			} else {
				rKey := extractErrorResourceKey(tc.Name, tc.Arguments)
				if silentMsg := a.silentError.recordToolAction(tc.Name, rKey); silentMsg != "" {
					a.appendGuidance(&result, silentMsg)
				}
			}

			a.planDrift.adherence.noteToolCall(a.planDrift.items, runStats)
			if tc.Name == "run_command" || tc.Name == "start_command" {
				cmd := extractCommandFromToolCall(tc.Arguments)
				if cmd != "" {

					if tc.Name == "run_command" {
						if narrowMsg := a.scopeNarrow.recordVerificationCommand(tc.Name, cmd, result.Content, result.IsError); narrowMsg != "" {
							a.contextManager.Add(provider.Message{
								Role:    "user",
								Content: []provider.ContentBlock{{Type: "text", Text: narrowMsg}},
							})
							msgs = a.contextManager.Messages()
						}
					}

					if tc.Name == "run_command" && !result.IsError {
						runStats.recordSuccessfulCommand(cmd)
					}
				}
			}

			if a.effortAdapter != nil {
				a.effortAdapter.recordToolResultErr(tc.Name, result.IsError, result.Content)
			}

			a.cascade.noteError(result.IsError)

			if a.strategyStagnation.recordAttemptDetail(tc.Name, string(tc.Arguments), !result.IsError, result.Content) {
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

			if result.IsError {
				if catGuidance := a.errorClassifier.classifyToolError(tc.Name, result.Content); catGuidance.Name != "" {
					g := fmt.Sprintf("[Error guidance: %s] %s", catGuidance.Name, catGuidance.Guidance)
					a.appendGuidance(&result, g)
				}
			}

			if tc.Name == "run_command" && a.testFails != nil {

				a.testFails.record(extractCommandFromToolCall(tc.Arguments), result.Content)
			}

			if result.IsError {
				if fallbackHint := a.toolFallbackCheck(tc.Name, result.Content); fallbackHint != "" {
					a.appendGuidance(&result, fallbackHint)
				}
			}

			if nativeHint := a.shellNativeHint.maybeShellNativeHint(tc.Name, tc.Arguments); nativeHint != "" {
				a.appendGuidance(&result, nativeHint)
			}

			if errorGuidance := a.errorStreakCheck(result.IsError, tc.Name); errorGuidance != "" {
				a.appendGuidance(&result, errorGuidance)
			}

			a.compoundingFailure.recordResult(tc.Name, result.IsError)
			if compoundingGuidance := a.compoundingFailure.check(); compoundingGuidance != "" {
				a.appendGuidance(&result, compoundingGuidance)
			}

			if claimGuidance := a.claimVerify.check(tc.Name, result.Content, result.IsError, extractCommandFromToolCall(tc.Arguments)); claimGuidance != "" {
				a.appendGuidance(&result, claimGuidance)
			}

			if modeGuard := a.permDenyStreak.record(a.currentMode(), result); modeGuard != "" {
				a.appendGuidance(&result, modeGuard)
			}

			if modeGuidance := a.failureMode.recordResult(tc.Name, result.IsError, result.Content); modeGuidance != "" {

				a.crossDetectorConsensus.recordFiring("Failure Mode", i+1)
				a.appendGuidance(&result, modeGuidance)
			}

			if result.IsError {
				if cascadeGuidance := a.errorCascade.recordError(tc.Name, result.Content); cascadeGuidance != "" {

					a.crossDetectorConsensus.recordFiring("Error Cascade", i+1)
					a.appendGuidance(&result, cascadeGuidance)
				}
			}

			if propGuidance := a.errorPropagate.recordResult(tc.Name, result.Content, result.IsError); propGuidance != "" {

				a.crossDetectorConsensus.recordFiring("Error Propagation", i+1)
				a.appendGuidance(&result, propGuidance)
			}

			if !result.IsError {
				driftPaths := extractFileHints(tc.Name, tc.Arguments)
				if written := extractWrittenPaths(result.Content); len(written) > 0 {
					driftPaths = written
				}
				for _, fh := range driftPaths {
					a.scopeDriftRecord(tc.Name, fh)
				}
			}

			driftRecPaths := extractFileHints(tc.Name, tc.Arguments)
			if written := extractWrittenPaths(result.Content); len(written) > 0 {
				driftRecPaths = written
			}
			if len(driftRecPaths) == 0 {
				driftRecPaths = []string{extractFileHint(tc.Name, tc.Arguments)}
			}
			for _, fh := range driftRecPaths {
				a.driftRecurrenceRecord(tc.Name, fh, string(tc.Arguments), !result.IsError)
			}

			if written := extractWrittenPaths(result.Content); len(written) > 0 {
				for _, p := range written {
					a.lastGoodCheckpointRecordEdit(tc.Name, p)
				}
			} else if !result.IsError {
				a.lastGoodCheckpointRecordEdit(tc.Name, extractFileHint(tc.Name, tc.Arguments))
			}

			if !result.IsError {
				monoPaths := extractFileHints(tc.Name, tc.Arguments)
				if written := extractWrittenPaths(result.Content); len(written) > 0 {
					monoPaths = written
				}
				for _, fh := range monoPaths {
					a.monorepoScoper.recordEdit(fh)
				}
			}
			if scopeGuidance := a.scopeDriftCheck(); scopeGuidance != "" {

				a.driftRecurrenceMarkWarn(runStats.Iterations)
				a.appendGuidance(&result, scopeGuidance)
			}

			if recurrenceGuidance := a.driftRecurrenceCheck(); recurrenceGuidance != "" {
				a.appendGuidance(&result, recurrenceGuidance)
			}

			if overseerGuidance := a.overseerCheck(tc.Name, result.IsError, extractFileHint(tc.Name, tc.Arguments), result.Content, runStats.Iterations); overseerGuidance != "" {
				a.appendGuidance(&result, overseerGuidance)
			}

			if llmOverseerGuidance := a.llmOverseerCheck(runStats.Iterations); llmOverseerGuidance != "" {
				a.appendGuidance(&result, llmOverseerGuidance)
			}

			if repetitionGuidance := a.repetitionCheckEdit(tc.Name, tc.Arguments, result.IsError); repetitionGuidance != "" {
				a.appendGuidance(&result, repetitionGuidance)
			}

			if tc.Name == "read_file" || tc.Name == "multi_file_read" {
				if readGuidance := a.repetitionCheckRead(extractFileHint(tc.Name, tc.Arguments)); readGuidance != "" {
					a.appendGuidance(&result, readGuidance)
				}
			}

			a.causalAttribution.recordEdit(tc.Name, extractFileHint(tc.Name, tc.Arguments), i)
			a.confidence.recordResult(tc.Name, result.IsError, extractFileHint(tc.Name, tc.Arguments))

			if tc.Name == "run_command" || tc.Name == "bash" || tc.Name == "powershell" || tc.Name == "start_command" || tc.Name == "wait_command" || tc.Name == "read_command_output" {

				if expGuidance := a.attrExperiment.observeCommand(tc.Name, causalCmdForGate(tc, result.Content), result.IsError, result.Content); expGuidance != "" {
					a.appendGuidance(&result, expGuidance)
				}
				if result.IsError || looksLikeFailure(result.Content) {

					if causalHint := a.causalAttribution.attributeFailureCmd(result.Content, causalCmdForGate(tc, result.Content), result.IsError); causalHint != "" {
						a.appendGuidance(&result, causalHint)

						if armGuidance := a.attrExperiment.arm(a.causalAttribution.lastSuspect, causalCmdForGate(tc, result.Content)); armGuidance != "" {
							a.appendGuidance(&result, armGuidance)
						}
					}
				}
			}
			if confidenceGuidance := a.confidence.maybeIntervene(); confidenceGuidance != "" {
				a.appendGuidance(&result, confidenceGuidance)
			}

			a.verifDebt.recordToolCall(tc.Name, string(tc.Arguments))

			a.prematureCommit.recordExploration(tc.Name, extractFileHints(tc.Name, tc.Arguments))
			if debtGuidance := a.verifDebt.maybeWarn(); debtGuidance != "" {
				a.appendGuidance(&result, debtGuidance)
			}

			a.editAbandon.recordToolCall(tc.Name, string(tc.Arguments))
			if abandonGuidance := a.editAbandon.maybeWarn(); abandonGuidance != "" {
				a.appendGuidance(&result, abandonGuidance)
			}

			editPaths := extractEditedPaths(tc)
			if result.IsError {
				if written := extractWrittenPaths(result.Content); len(written) > 0 {
					editPaths = written
				} else {
					editPaths = nil
				}
			}
			if isEditTool(tc.Name) && len(editPaths) > 0 {
				a.fileChurn.recordEdit(editPaths)

				a.constraintAmnesia.noteEdit(i + 1)
				for _, p := range editPaths {
					a.tunnelVision.recordFile(p)
				}

				pcMsg := a.prematureCommit.checkFirstEdit(editPaths)
				if pcMsg != "" {
					a.appendGuidance(&result, pcMsg)
				}
				if cg := func() string {
					if !shouldRunDetector(detectorTierRoutine, i+1) {
						return ""
					}
					return a.fileChurn.check()
				}(); cg != "" {
					a.appendGuidance(&result, cg)
				}

				a.editOscillation.recordEdit(tc.Name, tc.Arguments, i+1)
				if om := func() string {
					if !shouldRunDetector(detectorTierRoutine, i+1) {
						return ""
					}
					return a.editOscillation.check()
				}(); om != "" {
					a.appendGuidance(&result, om)
				}
			}

			if tv := func() string {
				if !shouldRunDetector(detectorTierRoutine, i+1) {
					return ""
				}
				return a.tunnelVision.check(runStats.Iterations)
			}(); tv != "" {
				a.appendGuidance(&result, tv)
			}

			a.maybeResetVerifyOnCommand(tc.Name, tc.Arguments, result.IsError)

			if verifyDebtSpawnClearingTool(tc.Name) && !result.IsError && isVerificationCommand(extractCommandFromArgs(tc.Arguments)) {
				a.verifyDebt.recordVerifyCommand(extractCommandFromArgs(tc.Arguments), result.IsError)
			}

			if tc.Name == "start_command" && !result.IsError {
				a.bgVerifyJobs.register(psExtractJobID(result.Content), extractCommandFromArgs(tc.Arguments))
			}
			if tc.Name == "wait_command" || tc.Name == "read_command_output" {

				if cmd, ok := a.bgVerifyJobs.peek(bgVerifyExtractJobID(tc.Arguments)); ok && isVerificationCommand(cmd) {

					if terminal, passed := psTerminalVerifyOutcome(psParseJobStatus(result.Content)); terminal {
						a.bgVerifyJobs.remove(bgVerifyExtractJobID(tc.Arguments))

						a.strategyFixation.recordVerification(tc.Name, result.Content, !passed)
						if passed {
							a.verifyDebt.recordVerifyCommand(cmd, false)
							a.editPropagation.recordGreenBuild()

							if isStrictVerifyCommand(cmd) {
								a.fileChurn.recordVerifySuccess(cmd)
							}
						}
					}
				}
			}

			a.prematureRefactorRecordVerifyForTool(tc.Arguments)

			if verifyDebtSpawnClearingTool(tc.Name) && !result.IsError {
				if cmd := extractCommandFromArgs(tc.Arguments); cmd != "" && isVerifyCommand(cmd) {
					a.editPropagation.recordGreenBuild()

					if isStrictVerifyCommand(cmd) {
						a.fileChurn.recordVerifySuccess(cmd)
					}
				}
			}

			a.convergenceRecordVerify(tc.Name, tc.Arguments, result.IsError)

			if recurringGuidance := a.recurringErrorCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); recurringGuidance != "" {
				a.appendGuidance(&result, recurringGuidance)
			}

			if stalledGuidance := a.stalledConvergenceCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); stalledGuidance != "" {
				a.appendGuidance(&result, stalledGuidance)
			}
			if regressionGuidance := a.errorRegressionCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); regressionGuidance != "" {
				a.appendGuidance(&result, regressionGuidance)
			}

			a.errorCompound.recordStep(a.errorCompound.recordResult(tc.Name, result.IsError, i+1))

			if result.IsError {
				if cat, file := classifyToolError(tc.Name, result.Content); cat != "" {
					a.fixAmnesia.recordErrorObserved(cat, file)
				}
			}
			if csIsEditTool(tc.Name) || tc.Name == "write_file" || tc.Name == "multi_edit_file" {
				fp := extractFilePathFromArgs(tc.Name, tc.Arguments)
				if result.IsError {

					if cat, file := classifyToolError(tc.Name, result.Content); cat != "" {
						if file == "" {
							file = fp
						}
						a.fixAmnesia.recordErrorObserved(cat, file)
					}
				} else {

					a.fixAmnesia.recordFileEdited(fp)
				}

				if !result.IsError && fp != "" && a.fixAmnesia.hasFixedPatternsInOtherFiles(fp) {
					if b, rerr := os.ReadFile(fp); rerr == nil {
						if faGuidance := a.fixAmnesia.checkContentAgainstFixed(extractFilePathFromError(result.Content), fp, string(b)); faGuidance != "" {
							a.appendGuidance(&result, faGuidance)
						}
					}
				}
			}
			var psArgs map[string]interface{}
			if len(tc.Arguments) > 0 {
				_ = json.Unmarshal(tc.Arguments, &psArgs)
			}
			if csIsEditTool(tc.Name) {
				a.correctionSpiral.recordEdit(i + 1)
			} else if (tc.Name == "run_command" || tc.Name == "bash" || tc.Name == "powershell") && psIsVerifyCommand(sfCommandArg(psArgs)) {

				a.correctionSpiral.recordVerifyResult(tc.Name, result.Content, result.IsError, i+1)
			} else if tc.Name == "wait_command" || tc.Name == "read_command_output" || tc.Name == "task_output" {

				jobID, _ := psArgs["job_id"].(string)
				if jobID == "" {
					jobID, _ = psArgs["task_id"].(string)
				}
				if jobID != "" && a.prematureSuccess.psJobIsVerify(jobID) {
					if terminal, _ := psTerminalVerifyOutcome(psParseJobStatus(result.Content)); terminal {
						a.correctionSpiral.recordVerifyResult(tc.Name, result.Content, result.IsError, i+1)
					}
				}

				if jobID != "" {
					if cmd, ok := a.prematureSuccess.psJobCommand(jobID); ok && cmd != "" {
						if terminal, passed := psTerminalVerifyOutcome(psParseJobStatus(result.Content)); terminal && !passed {
							a.buildIdempot.markBuildFailed(cmd)
						}
					}
				}
			}

			a.bareEditStreak.recordToolCall(tc.Name, string(tc.Arguments))

			a.prematureSuccess.recordToolCall(tc.Name, psArgs, result.IsError, result.Content)

			a.phantomVerify.recordToolCall(tc.Name, string(tc.Arguments), result.IsError)

			sfIsVerify := strategyFixationIsVerification(tc.Name)
			if sfIsVerify && (tc.Name == "run_command" || tc.Name == "bash" || tc.Name == "powershell") {
				sfIsVerify = psIsVerifyCommand(sfCommandArg(psArgs))
			}

			if strategyFixationIsMutation(tc.Name) {

				for _, fp := range sfExtractMutationPaths(tc.Arguments) {
					a.strategyFixation.recordEdit(fp)
				}
			} else if sfIsVerify {
				a.strategyFixation.recordVerification(tc.Name, result.Content, result.IsError)
			}

			a.errorRush.recordToolCall(tc.Name, result.Content, result.IsError)
			var afArgs map[string]interface{}
			if len(tc.Arguments) > 0 {
				_ = json.Unmarshal(tc.Arguments, &afArgs)
			}
			a.attentionFragment.recordToolCall(tc.Name, afArgs)

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

			if a.irrevGate != nil {
				a.irrevGate.recordOutcome(tc.Name, result.IsError)
			}

			if fileEditingTools[tc.Name] && !result.IsError {
				a.futileCycle.recordWrite()

				a.phantomVerify.invalidateEdits()
			} else if !result.IsError {

				if readPaths := extractFilePathsFromArgs(tc.Arguments, tc.Name); len(readPaths) > 0 {

					for _, p := range readPaths {
						a.futileCycle.recordRead(p)

						a.expiredRead.recordRead(p)
						a.wtInvalidation.recordRead(p)
					}

					if hint := a.expiredRead.checkPostEditReread(readPaths[0]); hint != "" {
						a.appendGuidance(&result, hint)
					}
				}

				a.searchInvalidation.recordSearchResult(tc.Name, result.Content)
			}

			if (isWTMutatingTool(tc.Name) ||
				((tc.Name == "run_command" || tc.Name == "start_command" || tc.Name == "bash" || tc.Name == "powershell") && runCommandMutatesTree(string(tc.Arguments)))) && !result.IsError {
				if wtMsg := a.wtInvalidation.checkMutation(tc.Name, string(tc.Arguments)); wtMsg != "" {
					debug.Log("agent", "Iteration %d: working-tree invalidation detector triggered", i+1)
					a.appendGuidance(&result, wtMsg)
				}
			}
			if fileEditingTools[tc.Name] && !result.IsError {
				a.verifyDebt.recordSourceEdit()
				a.stalledConvergence.recordEdit()

				if editPath := extractFilePathFromArgs(tc.Name, tc.Arguments); editPath != "" {
					if siMsg := a.searchInvalidation.checkEditInvalidation(editPath); siMsg != "" {
						a.appendGuidance(&result, siMsg)
					}
				}
				a.editPropagation.recordEdit(tc.Name, string(tc.Arguments))
			}

			if fileEditingTools[tc.Name] || tc.Name == "run_command" {
				a.successDeclare.recordToolCall()
			}
			a.subgoalTrack.recordToolCall(tc.Name, string(tc.Arguments))

			a.attemptBrief.recordOutcome(tc.Name, extractToolTarget(tc.Name, string(tc.Arguments)), !result.IsError, i, result.Content)

			if trMsg := a.toolResultRedundancy.recordToolCall(tc.Name, tc.Arguments, result.Content, i+1); trMsg != "" {
				a.appendGuidance(&result, trMsg)
			}
			if cascadeGuidance := a.fixCascadeCheckCommand(tc.Name, tc.Arguments, result.IsError, result.Content); cascadeGuidance != "" {

				a.crossDetectorConsensus.recordFiring("Fix Cascade", i+1)
				a.appendGuidance(&result, cascadeGuidance)
			}

			if !result.IsError {
				if verifyHint := a.postEditVerifyHint(tc.Name, tc.Arguments); verifyHint != "" {
					a.appendGuidance(&result, verifyHint)
				}
			}

			if convergenceGuidance := a.convergenceCheck(); convergenceGuidance != "" {
				a.crossDetectorConsensus.recordFiring("Convergence Lock", i+1)
				a.appendGuidance(&result, convergenceGuidance)
			}

			if diminishingGuidance := a.diminishingCheck(); diminishingGuidance != "" {
				a.appendGuidance(&result, diminishingGuidance)
			}

			if refactorGuidance := a.prematureRefactorCheck(); refactorGuidance != "" {
				a.appendGuidance(&result, refactorGuidance)
			}

			if consensusGuidance := a.crossDetectorConsensus.checkOnly(); consensusGuidance != "" {
				a.appendGuidance(&result, consensusGuidance)
			}

			if len(result.FollowUpMessages) > 0 {
				followUpMessages = append(followUpMessages, result.FollowUpMessages...)
			}

			if result.SuggestedWorkingDir != "" && !result.IsError {
				a.mu.Lock()
				oldDir := a.workingDir
				a.workingDir = result.SuggestedWorkingDir

				if a.ruleStore != nil {
					a.resetRuleStoreLocked()
				}
				a.mu.Unlock()
				debug.Log("agent", "working dir changed: %s -> %s (suggested by %s)", oldDir, result.SuggestedWorkingDir, tc.Name)
			}

			result.Content = guardPromptInjection(tc.Name, tc.Arguments, result.Content)

			a.taintInfluence.recordIfTainted(tc.Name, result.Content)

			if staleMsg := a.checkPushReviewStaleness(tc.Name, tc.Arguments, result.Content); staleMsg != "" {
				result.Content += "\n\n" + staleMsg
			}

			if branchMsg := a.checkSharedTreeBranchGuard(tc.Name, tc.Arguments, result.Content); branchMsg != "" {
				result.Content += "\n\n" + branchMsg
			}

			if !result.IsError {
				a.recordSpiralVerification(tc.Name, result.Content)
			}

			if compressed := compressRepetitiveLines(result.Content); len(compressed) < len(result.Content) {
				debug.Log("compress", "repetitive-line compression: tool=%s %d→%d bytes", tc.Name, len(result.Content), len(compressed))
				result.Content = compressed
			}

			if !result.IsError {
				threshold := a.contextManager.AutoCompactThreshold()
				if threshold > 0 {
					fillRatio := float64(a.contextManager.TokenCount()) / float64(threshold)
					if truncated := guardToolOutput(result.Content, fillRatio); len(truncated) < len(result.Content) {
						debug.Log("agent", "tool output guarded: tool=%s tokens=%d threshold=%d fill=%.0f%% %d→%d bytes", tc.Name, a.contextManager.TokenCount(), threshold, fillRatio*100, len(result.Content), len(truncated))
						guarded := truncated

						if spillPath := a.outputOffload.spill(tc.Name, result.Content); spillPath != "" {
							guarded += spillNotice(spillPath, len(result.Content))
							debug.Log("agent", "tool output offloaded: tool=%s path=%s originalLen=%d", tc.Name, spillPath, len(result.Content))
						}
						result.Content = withTruncationAdvisory(guarded, tc.Name, len(result.Content))
						a.truncClaim.recordTruncation(tc.Name, i)

						a.errorPropagate.recordGuardedTruncation(tc.Name, result.Content)
					}
				}
			}

			measuredLen := len(result.Content)

			if askAdvisory != "" && tc.Name == "ask_user" && !result.IsError {
				result.Content = result.Content + "\n\n" + askAdvisory
			}
			a.applyToolResultGuidance(&result, loopGuidance, searchParamHint, redundancyHint, equivHint, undoBlindHint)

			llmContent := spotlightUntrustedOutput(tc.Name, result.Content)
			if len(result.Images) > 0 && a.SupportsVision() {
				imgs := make([]provider.ContentImage, len(result.Images))
				for i, ri := range result.Images {
					imgs[i] = provider.ContentImage{MIME: ri.MIME, Base64: ri.Base64}
				}
				toolResults = append(toolResults, provider.ToolResultWithImages(tc.ID, tc.Name, llmContent, imgs, result.IsError))
			} else {
				toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, llmContent, result.IsError))
			}
			onEvent(provider.StreamEvent{
				Type:    provider.StreamEventToolResult,
				Tool:    tc,
				Result:  result.Content,
				IsError: result.IsError,
			})

			if speculativeSafeTools[tc.Name] && !result.IsError {
				seenReadOnly[dedupK] = len(toolResults) - 1
			} else if !speculativeSafeTools[tc.Name] && !result.IsError {

				if len(seenReadOnly) > 0 {
					debug.Log("agent", "in-turn dedup invalidated: %s mutated state, %d cached read-only results dropped", tc.Name, len(seenReadOnly))
					seenReadOnly = make(map[dedupKey]int)
				}
			}
			var pathsRead []string
			if tc.Name == "read_file" || tc.Name == "multi_file_read" {
				pathsRead = extractReadFilePaths(tc.Name, tc.Arguments)
			}
			isRedundant := redundancyHint != ""
			if a.tokenWasteBudget != nil {

				a.tokenWasteBudget.recordToolResultLen(tc.Name, result.Content, measuredLen, result.IsError, isRedundant, pathsRead)
			}
			if err := ctx.Err(); err != nil {

				a.fillCancelledToolResults(toolCalls[idx+1:], &toolResults)

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
			Role:    "user",
			Content: toolResults,
		})

		if serialWarn := a.serialRead.endTurn(i + 1); serialWarn != "" {
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: serialWarn,
				}},
			})
		}

		if len(toolCalls) > 0 {

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

		for _, msg := range followUpMessages {
			debug.Log("agent", "Injecting follow-up message from tool: role=%s", msg.Role)
			a.contextManager.Add(msg)
		}

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

		if tcMsg, stop := a.toolCallBudget.check(); tcMsg != "" {
			debug.Log("tool-call-budget", "threshold crossed: calls=%d budget=%d stop=%v",
				a.toolCallBudget.totalCalls, a.toolCallBudget.effectiveBudget(), stop)
			if stop {

				runStats.finalize(nil)
				summary := runStats.Summary()
				onEvent(provider.StreamEvent{
					Type: provider.StreamEventText,
					Text: fmt.Sprintf("\nTool call budget exhausted (%d calls). Summary: %s.\nYour task may be partially complete - review the changes above. You can continue by sending a follow-up message.", a.toolCallBudget.totalCalls, summary),
				})
				err := fmt.Errorf("tool call budget (%d calls) exhausted", a.toolCallBudget.totalCalls)
				onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
				return err
			}

			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: tcMsg,
				}},
			})
			msgs = a.contextManager.Messages()
		}
	}

	if sessionTimedOut {
		runStats.finalize(nil)
		summary := runStats.Summary()
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\nSession wall-clock timeout reached (limit: %s). Summary: %s.\nYour task may be partially complete - review the changes above. You can continue by sending a follow-up message.", a.sessionTimeout.runTimeout.Round(time.Second), summary),
		})
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ErrSessionTimeout})
		return ErrSessionTimeout
	}

	if sessionBudgetStop {
		runStats.finalize(nil)
		summary := runStats.Summary()
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\nSession budget exhausted (token/cost/delegated/time limit reached). No further LLM calls this run. Summary: %s.\nYour task may be partially complete - review the changes above. You can continue by sending a follow-up message or raising the session budget.", summary),
		})
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ErrSessionBudgetExhausted})
		debug.Log("session-token-budget", "budget stop armed on final iteration; sentinel surfaced post-loop (#4421)")
		return ErrSessionBudgetExhausted
	}

	if guidanceAtBudget {
		runStats.finalize(nil)
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\nMid-run guidance arrived on the final iteration and was saved to context, but this run had no budget left to act on it. It will be consumed first on the next run. Summary: %s.", runStats.Summary()),
		})
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ErrGuidanceAtBudget})
		return ErrGuidanceAtBudget
	}
	if a.maxIter > 0 {

		runStats.finalize(nil)
		summary := runStats.Summary()
		debug.Log("agent", "RunStreamWithContent END: max iterations reached (%s)", summary)
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\nReached maximum iterations (%d). Summary: %s.\nYour task may be partially complete - review the changes above. You can continue by sending a follow-up message.", a.maxIter, summary),
		})
		err := fmt.Errorf("max iterations (%d) reached", a.maxIter)
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
		return err
	}
	return nil
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

func (a *Agent) streamChatResponse(ctx context.Context, msgs []provider.Message, toolDefs []provider.ToolDefinition, onEvent func(provider.StreamEvent)) (*provider.ChatResponse, string, []provider.ToolCallDelta, bool, bool, int, error) {

	if onEvent == nil {
		onEvent = func(provider.StreamEvent) {}
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	rawStream, err := a.provider.ChatStream(streamCtx, msgs, toolDefs)
	if err != nil {
		debug.Log("agent", "ChatStream error: %v", err)
		return nil, "", nil, false, false, 0, fmt.Errorf("chat error: %w", err)
	}
	stream := streamWithStallDetection(rawStream, streamStallThreshold)
	var (
		textBuf          strings.Builder
		assistantTextBuf strings.Builder
		content          []provider.ContentBlock
		toolCalls        []provider.ToolCallDelta
		usage            provider.TokenUsage
		truncated        bool
		policyBlocked    bool
	)
	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		s := textBuf.String()

		if strings.TrimSpace(s) != "" {
			content = append(content, provider.TextBlock(s))
		}
		textBuf.Reset()
	}
	var thinkingAcc thinkingAccumulator
	turnReasoningChars := 0

	turnMetrics := newLLMTurnMetrics()
	for event := range stream {
		switch event.Type {
		case provider.StreamEventText:
			turnMetrics.markText(event.Text)
			onEvent(event)
			textBuf.WriteString(event.Text)
			assistantTextBuf.WriteString(event.Text)
		case provider.StreamEventReasoning:
			turnMetrics.markReasoning(event.Text)

			onEvent(event)
			if event.Text != "" {
				turnReasoningChars += len(event.Text)
				thinkingAcc.onText(event.Text)
			}

			if event.ThinkingSignature != "" {
				thinkingAcc.onSignature(event.ThinkingSignature)
			}
		case provider.StreamEventToolCallChunk:

			turnMetrics.markToolChunk()
			onEvent(event)
		case provider.StreamEventToolCallDone:
			turnMetrics.closeThinkWindow()
			flushText()
			onEvent(event)
			toolCalls = append(toolCalls, event.Tool)
			blk := provider.ToolUseBlock(event.Tool.ID, event.Tool.Name, event.Tool.Arguments)
			blk.ThinkingSignature = string(event.Tool.ThoughtSignature)
			blk.CallerRaw = event.Tool.Caller
			content = append(content, blk)
		case provider.StreamEventServerTool:

			flushText()
			onEvent(event)
			content = append(content, event.Block)
		case provider.StreamEventDone:
			if event.Usage != nil {
				usage = *event.Usage
			}
			truncated = event.Truncated
			policyBlocked = event.PolicyBlocked

			if event.Confidence != nil {
				conf := *event.Confidence
				debug.Log("agent", "turn confidence: avg token logprob %.3f", conf)
				if conf < -2.5 {
					onEvent(provider.StreamEvent{
						Type: provider.StreamEventSystem,
						Text: fmt.Sprintf("[confidence] low model confidence this turn (avg token logprob %.2f) - consider reviewing the output.", conf),
					})
				}
			}

			ev := turnMetrics.emit(usage)

			stampMetricModel(a.provider, &ev)
			a.emitMetric(ev)
			onEvent(event)

			if rl, ok := a.provider.(provider.RateLimitProvider); ok {
				info := rl.RateLimitInfo()
				if info.IsCritical() && !info.IsEmpty() {
					debug.Log("agent", "rate limit warning: req %d/%d (%.0f%%), tok %d/%d (%.0f%%)",
						info.RemainingRequests, info.LimitRequests, info.RequestFractionRemaining()*100,
						info.RemainingTokens, info.LimitTokens, info.TokenFractionRemaining()*100)

					a.mu.Lock()
					firstCritical := !a.rlCriticalWarned
					a.rlCriticalWarned = true
					a.mu.Unlock()
					if firstCritical {
						onEvent(provider.StreamEvent{
							Type: provider.StreamEventSystem,
							Text: fmt.Sprintf("[rate limit] Quota nearly exhausted (requests %d/%d, tokens %d/%d remaining). Expect 429s soon - consider wrapping up or switching endpoint.",
								info.RemainingRequests, info.LimitRequests, info.RemainingTokens, info.LimitTokens),
						})
					}
				} else if !info.IsEmpty() {

					a.mu.Lock()
					a.rlCriticalWarned = false
					a.mu.Unlock()
				}
			}

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

			onEvent(event)
		case provider.StreamEventError:
			debug.Log("agent", "ChatStream event error: %v", event.Error)
			return nil, assistantTextBuf.String(), nil, false, false, turnReasoningChars, fmt.Errorf("chat error: %w", event.Error)
		}
	}
	flushText()

	respMsg := provider.Message{
		Role:    "assistant",
		Content: content,
	}

	if blocks := thinkingAcc.accumulated(); len(blocks) > 0 {
		respMsg.Content = append(blocks, respMsg.Content...)
	}
	return &provider.ChatResponse{
		Message: respMsg,
		Usage:   usage,
	}, assistantTextBuf.String(), toolCalls, truncated, policyBlocked, turnReasoningChars, nil
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
