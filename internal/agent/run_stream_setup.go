package agent

import (
	"context"
	"fmt"
	"path/filepath"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/provider"
)

// runStreamCtx carries per-run setup products through the #5543 knife-5 seam.
type runStreamCtx struct {
	userPromptForStats string
	runStats           *RunStats
	asyncVerifyStats   *RunStats
}

// runStreamSetup prepares per-run state. #5543 knife-5, verbatim body.
func (a *Agent) runStreamSetup(ctx context.Context, content []provider.ContentBlock, sid string, onEvent func(provider.StreamEvent)) (*runStreamCtx, error) {
	if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
		cm.StartRunTracking()
	}

	a.mu.RLock()
	startHookCfg := a.hookConfig
	startWorkDir := a.workingDir
	a.mu.RUnlock()
	if len(startHookCfg.OnSessionStart) > 0 {
		if err := a.fireSessionStartHooks(sid, startHookCfg, startWorkDir, onEvent); err != nil {
			return nil, err
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
	return &runStreamCtx{userPromptForStats: userPromptForStats, runStats: runStats, asyncVerifyStats: asyncVerifyStats}, nil
}

// buildRunTurnContextD1 stages the user turn + pre-loop injections (#5543 knife-5).
func (a *Agent) buildRunTurnContextD1(ctx context.Context, content []provider.ContentBlock, userOrigin string, rsc *runStreamCtx, onEvent func(provider.StreamEvent)) (msgs []provider.Message, userText string, toolDefs []provider.ToolDefinition, err error) {
	userTurn := provider.Message{
		Role:    "user",
		Content: content,
		Origin:  userOrigin,
	}
	a.contextManager.Add(userTurn)

	userText = ""
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
		return nil, "", nil, fmt.Errorf("user message blocked by hook: %s", userMsgResult.Output)
	}

	if a.ReconcileToolCalls() {
		debug.Log("agent", "RunStreamWithContent: reconciled unpaired tool_calls")
	}

	a.clearGoalIfNotAutopilot()
	a.maybeInjectAutopilotGoalCollection()
	a.maybeInjectCorrectionFeedback()
	a.maybeInjectSentimentFeedback(rsc.userPromptForStats)

	if ambMsg := a.checkAmbiguityPoints(rsc.userPromptForStats); ambMsg != "" {
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
	a.maybeInjectDynamicSystemPrompt(rsc.userPromptForStats)
	a.maybeInjectRatchetRules()

	a.mu.RLock()
	uhDir := a.workingDir
	a.mu.RUnlock()
	if uhDir != "" {
		a.usageHints.attach(filepath.Join(uhDir, ".ggcode"))
		a.tools.SetDescriptionAugmenter(a.usageHints.OverlayFor)
	}
	toolDefs = a.tools.ToDefinitions()
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

	return msgs, userText, toolDefs, nil
}

// buildRunTurnContextD2 resets detectors + prepares tool defs (#5543 knife-5).
func (a *Agent) buildRunTurnContextD2(ctx context.Context, content []provider.ContentBlock, sid string, msgs []provider.Message, userText string, rsc *runStreamCtx, onEvent func(provider.StreamEvent)) (outMsgs []provider.Message, toolDefs []provider.ToolDefinition, outUserText string) {
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
		a.checkpoints.StartRun(rsc.runStats.RunID())
	}

	a.sessionTimeout.start(a.currentMode().IsCruiseMode())

	if underspecHint := a.maybeWarnInputUnderspec(userText); underspecHint != "" {
		debug.Log("input-underspec", "underspecified user request detected, injecting advisory")
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: underspecHint}},
		})
	}

	return msgs, toolDefs, userText
}
