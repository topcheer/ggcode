package agent

func (a *Agent) resetTurnDetectors() {
	a.resetLoopDetector()
	a.errorClassifier.reset()
	resetSharedTreeBranchGuard()
	a.resetPostEditVerify()
	a.resetRepetitionTracker()
	a.fulfillmentGate.reset()
	a.ambiguityPoint.reset()
	a.planDrift.reset()
	a.unverifiedClaim.reset()
	a.companionGuard.reset()
	a.specGaming.reset()
	a.scopeNarrow.reset()
	a.crossDetectorConsensus.reset()
	a.taintInfluence.reset()
	a.exfilChain.reset()
	a.perfBaseline.reset()
	a.argSizeGuardFires = 0
	a.redundantRead.reset()
	a.patchExhaust.reset()
	a.searchParamGuard.reset()
	a.toolRedundancy.reset()
	a.toolEquivDetect.reset()
	a.toolSequence.reset()
	a.obsContract.reset()
	a.shellNativeHint.reset()
	a.monorepoScoper.reset()
	a.resetBgOrphan()
	a.actionAnnihil.reset()
	a.exploreFrag.reset()
	a.batchCoupling.reset()
	a.buildIdempot.reset()
	a.orphanFile.reset()
	a.cfDep.reset()

	a.foresightCalib.reset()
	a.expiredRead.reset()

	a.resetConvergenceLock()
	a.integrationResetForRun()
	a.resetSelfMod()
	a.resetOvercorrection()
	if a.delegationOrch != nil {
		a.delegationOrch.resetForNewTurn()
	}
	if a.effortAdapter != nil {
		a.effortAdapter.reset()
	}
	if a.iterPressure != nil {
		a.iterPressure.reset(a.maxIter)
	}
	a.diminishingEdit.reset()
	a.overcorrection.reset()

	a.giveupRevert = &giveupRevertState{}
	a.prematureRefactor.reset()
	a.errorCompound.reset()
	a.correctionSpiral.reset()
	a.bareEditStreak.reset()
	a.editCoverage.reset()
	a.prematureSuccess.reset()
	a.strategyFixation.reset()
	a.errorRush.reset()
	a.phantomVerify.reset()
	if a.recklessExec != nil {
		a.recklessExec.reset()
	}
	if a.irrevGate != nil {
		a.irrevGate.reset()
	}
	if a.argComplexityGate != nil {
		a.argComplexityGate.reset()
	}
	a.subgoalTrack.reset()
	a.futileCycle.reset()
	a.toolResultRedundancy.reset()
	a.verifyDebt.reset()
	a.editPropagation.reset()
	a.successDeclare.reset()
	a.criteriaDrift.reset()
	a.reasonAction.reset()
	a.attemptBrief.reset()
}

// resetTurnStateForStream resets the per-run planner/research/detector
// state after user-message hooks and before the stream loop (#5543
// extraction; verbatim order).
func (a *Agent) resetTurnStateForStream(userText string) {
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
}

// resetDetectorBatteryB resets the remaining per-run detector set after
// the pre-run environment checks arm and before workspace state capture
// (#5543 extraction; verbatim order).
func (a *Agent) resetDetectorBatteryB() {
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
}
