package agent

import "github.com/topcheer/ggcode/internal/debug"

var guidanceCounterResets = []func(*Agent){

	func(a *Agent) {
		if a.silentError != nil {
			a.silentError.fired = false
		}
	},

	func(a *Agent) {
		if a.scopeDrift != nil {
			a.scopeDrift.fired = false
		}
	},

	func(a *Agent) {
		if a.errStrategyLoop != nil {
			a.errStrategyLoop.warningCount = 0
		}
	},

	func(a *Agent) {
		if a.commitHint != nil {
			a.commitHint.fired = false
		}
	},

	func(a *Agent) {
		if a.convergenceLock != nil {
			a.convergenceLock.warned = false
		}
	},

	func(a *Agent) {
		if a.giveupRevert != nil {
			a.giveupRevert.mu.Lock()
			a.giveupRevert.fired = false
			a.giveupRevert.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.inputUnderspec != nil {
			a.inputUnderspec.warned = false
		}
	},

	func(a *Agent) {
		if a.integrationMonitor != nil {
			a.integrationMonitor.warnings = 0
		}
	},

	func(a *Agent) {
		if a.iterPressure != nil {
			a.iterPressure.warningsFired = 0
		}
	},

	func(a *Agent) {
		if a.prematureCommit != nil {
			a.prematureCommit.warned = false
		}
	},

	func(a *Agent) {
		if a.reproducerLifecycle != nil {
			a.reproducerLifecycle.mu.Lock()
			a.reproducerLifecycle.warned = false
			a.reproducerLifecycle.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.reversibility != nil {
			a.reversibility.mu.Lock()
			a.reversibility.warnCount = 0
			a.reversibility.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.toolRedundancy != nil {
			a.toolRedundancy.warnings = 0
		}
	},

	func(a *Agent) {
		if a.tunnelVision != nil {
			a.tunnelVision.warned = false
		}
	},

	func(a *Agent) {
		if a.verifyDebt != nil {
			a.verifyDebt.mu.Lock()
			a.verifyDebt.warningsIssued = 0
			a.verifyDebt.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.infoScent != nil {
			a.infoScent.mu.Lock()
			a.infoScent.injectionCount = 0
			a.infoScent.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.futileCycle != nil {
			a.futileCycle.warningsFired = 0
		}
	},

	func(a *Agent) {
		if a.editPropagation != nil {
			a.editPropagation.mu.Lock()
			a.editPropagation.warningsIssued = 0
			a.editPropagation.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.constraintAmnesia != nil {
			a.constraintAmnesia.warnings = 0
		}
	},

	func(a *Agent) {
		if a.correctionSpiral != nil {
			a.correctionSpiral.warningCount = 0
		}
	},

	func(a *Agent) {
		if a.errorRush != nil {
			a.errorRush.warnCount = 0
		}
	},

	func(a *Agent) {
		if a.bareEditStreak != nil {
			a.bareEditStreak.warnCount = 0
		}
	},

	func(a *Agent) {
		if a.attentionFragment != nil {
			a.attentionFragment.warnCount = 0
		}
	},

	func(a *Agent) {
		if a.toolThermal != nil {
			a.toolThermal.reset()
		}
	},

	func(a *Agent) {
		if a.strategyFixation != nil {
			a.strategyFixation.warnCount = 0
		}
	},

	func(a *Agent) {
		if a.queryConverge != nil {
			a.queryConverge.mu.Lock()
			a.queryConverge.warnCount = 0
			a.queryConverge.warned = false
			a.queryConverge.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.errorCompound != nil {
			a.errorCompound.mu.Lock()
			a.errorCompound.warningCount = 0
			a.errorCompound.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.successDeclare != nil {
			a.successDeclare.mu.Lock()
			a.successDeclare.warnCount = 0
			a.successDeclare.fired = false
			a.successDeclare.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.undoBlind != nil {
			a.undoBlind.warnCount = 0
		}
	},

	func(a *Agent) {
		if a.cfDep != nil {
			a.cfDep.warnCount = 0
		}
	},

	func(a *Agent) {
		if a.editCoverage != nil {
			a.editCoverage.warnCount = 0
		}
	},

	func(a *Agent) {
		if a.foresightCalib != nil {
			a.foresightCalib.warnCount = 0
		}
	},

	func(a *Agent) {
		if a.criteriaDrift != nil {
			a.criteriaDrift.warnCount = 0
		}
	},

	func(a *Agent) {
		if a.editOscillation != nil {
			a.editOscillation.fired = 0
		}
	},
	func(a *Agent) {
		if a.subgoalTrack != nil {
			a.subgoalTrack.fired = false
		}
	},
	func(a *Agent) {
		if a.reasoningRedund != nil {
			a.reasoningRedund.warnCount = 0
		}
	},
	func(a *Agent) {
		if a.strategyStagnation != nil {
			a.strategyStagnation.warnings = 0
		}
	},
	func(a *Agent) {
		if a.actionAnnihil != nil {
			a.actionAnnihil.mu.Lock()
			a.actionAnnihil.warnsIssued = 0
			a.actionAnnihil.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.verifDebt != nil {
			a.verifDebt.warningsIssued = 0
		}
	},

	func(a *Agent) {
		if a.spiralState != nil {
			a.spiralState.warnings = 0
		}
	},
	func(a *Agent) {
		if a.capBoundary != nil {
			a.capBoundary.warnings = 0
		}
	},

	func(a *Agent) {
		if a.overcorrection != nil {
			a.overcorrection.mu.Lock()
			a.overcorrection.warnCount = 0
			a.overcorrection.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.trajectoryHealth != nil {
			a.trajectoryHealth.warnings = 0
		}
	},
	func(a *Agent) {
		if a.planAbandon != nil {
			a.planAbandon.warnings = 0
		}
	},
	func(a *Agent) {
		if a.redundantReverify != nil {
			a.redundantReverify.warnings = 0
		}
	},
	func(a *Agent) {
		if a.constraintViolation != nil {
			a.constraintViolation.warnings = 0
		}
	},
	func(a *Agent) {
		if a.reasonAction != nil {
			a.reasonAction.mu.Lock()
			a.reasonAction.warnings = 0
			a.reasonAction.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.contradiction != nil {
			a.contradiction.warnings = 0
		}
	},
	func(a *Agent) {
		if a.strategyExhaustion != nil {
			a.strategyExhaustion.mu.Lock()
			a.strategyExhaustion.warningCount = 0
			a.strategyExhaustion.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.actionHedging != nil {
			a.actionHedging.warnings = 0
		}
	},
	func(a *Agent) {
		if a.delegationOrch != nil {
			a.delegationOrch.mu.Lock()
			a.delegationOrch.orphanWarnCount = 0
			a.delegationOrch.serialWarnCount = 0
			a.delegationOrch.overDelWarned = false
			a.delegationOrch.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.buildIdempot != nil {
			a.buildIdempot.mu.Lock()
			a.buildIdempot.warnsIssued = 0
			a.buildIdempot.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.toolResultRedundancy != nil {
			a.toolResultRedundancy.warningsFired = 0
		}
	},
	func(a *Agent) {
		if a.editAbandon != nil {
			a.editAbandon.mu.Lock()
			a.editAbandon.warnings = 0
			a.editAbandon.fired = false
			a.editAbandon.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.outcomeMisattrib != nil {
			a.outcomeMisattrib.warnings = 0
		}
	},
	func(a *Agent) {
		if a.causalAttribution != nil {
			a.causalAttribution.warnings = 0
		}
	},
	func(a *Agent) {
		if a.mindlessAction != nil {
			a.mindlessAction.warnings = 0
		}
	},
	func(a *Agent) {
		if a.toolTargetMismatch != nil {
			a.toolTargetMismatch.warnings = 0
		}
	},
	func(a *Agent) {
		if a.toolEquivDetect != nil {
			a.toolEquivDetect.warnings = 0
		}
	},
	func(a *Agent) {
		if a.tokenWasteBudget != nil {
			a.tokenWasteBudget.mu.Lock()
			a.tokenWasteBudget.warnings = 0
			a.tokenWasteBudget.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.truncClaim != nil {
			a.truncClaim.warnings = 0
		}
	},
	func(a *Agent) {
		if a.solutionFixation != nil {
			a.solutionFixation.warningCount = 0
		}
	},
	func(a *Agent) {
		if a.fixAmnesia != nil {

			a.fixAmnesia.mu.Lock()
			a.fixAmnesia.warned = make(map[string]bool)
			a.fixAmnesia.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.errRegression != nil {
			a.errRegression.warningCount = 0
		}
	},
	func(a *Agent) {
		if a.phantomVerify != nil {

			a.phantomVerify.warnings = 0
		}
	},
	func(a *Agent) {
		if a.selfMod != nil {
			a.selfMod.mu.Lock()
			a.selfMod.warningCount = 0
			a.selfMod.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.prematureAbstr != nil {
			a.prematureAbstr.warnings = 0
		}
	},
	func(a *Agent) {
		if a.circularReasoning != nil {
			a.circularReasoning.warnings = 0
		}
	},
	func(a *Agent) {
		if a.stalledConvergence != nil {
			a.stalledConvergence.warningCount = 0
		}
	},
	func(a *Agent) {
		if a.expiredRead != nil {

			a.expiredRead.warningCount = 0
		}
	},
	func(a *Agent) {
		if a.searchInvalidation != nil {
			a.searchInvalidation.warningCount = 0
		}
	},
	func(a *Agent) {
		if a.irrevGate != nil {

			a.irrevGate.warnings = 0
		}
	},
	func(a *Agent) {
		if a.exploreFrag != nil {
			a.exploreFrag.mu.Lock()
			a.exploreFrag.warnings = 0
			a.exploreFrag.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.goalDriftCtx != nil {
			a.goalDriftCtx.mu.Lock()
			a.goalDriftCtx.warned = false
			a.goalDriftCtx.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.planDrift != nil {
			a.planDrift.fired = false
		}
	},
	func(a *Agent) {
		if a.driftRecurrence != nil {
			a.driftRecurrence.fired = false
			a.driftRecurrence.warned = false
		}
	},
	func(a *Agent) {
		if a.fulfillmentGate != nil {
			a.fulfillmentGate.fired = false
		}
	},
	func(a *Agent) {
		if a.scopeNarrow != nil {
			a.scopeNarrow.fired = false
		}
	},
	func(a *Agent) {
		if a.specGaming != nil {
			a.specGaming.fired = false
		}
	},
	func(a *Agent) {
		if a.selfCorrectionGate != nil {
			a.selfCorrectionGate.fired = false
		}
	},
	func(a *Agent) {
		if a.crossFileImpact != nil {
			a.crossFileImpact.mu.Lock()
			a.crossFileImpact.fired = false
			a.crossFileImpact.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.fileChurn != nil {
			a.fileChurn.fired = false
		}
	},

	func(a *Agent) {
		if a.compoundedUncert != nil {
			a.compoundedUncert.fired = false
		}
	},
	func(a *Agent) {
		if a.compoundingFailure != nil {
			a.compoundingFailure.fired = false
		}
	},
	func(a *Agent) {
		if a.branchGuard != nil {
			a.branchGuard.fired = false
		}
	},
	func(a *Agent) {
		if a.changeReconcile != nil {
			a.changeReconcile.fired = false
		}
	},
	func(a *Agent) {
		if a.companionGuard != nil {
			a.companionGuard.fired = false
		}
	},
	func(a *Agent) {
		if a.ambiguityPoint != nil {
			a.ambiguityPoint.mu.Lock()
			a.ambiguityPoint.fired = false
			a.ambiguityPoint.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.serialRead != nil {
			a.serialRead.mu.Lock()
			a.serialRead.fired = false
			a.serialRead.mu.Unlock()
		}
	},
	func(a *Agent) {
		if a.diffSummary != nil {
			a.diffSummary.fired = false
		}
	},
	func(a *Agent) {
		if a.unverifiedClaim != nil {
			a.unverifiedClaim.fired = false
		}
	},

	func(a *Agent) {
		if a.envDrift != nil {
			a.envDrift.fired = false
		}
	},
	func(a *Agent) {
		if a.diskSpace != nil {
			a.diskSpace.fired = false
		}
	},

	func(a *Agent) {
		if a.batchCoupling != nil {
			a.batchCoupling.warnsIssued = 0
		}
	},

	func(a *Agent) {
		if a.taintInfluence != nil {
			a.taintInfluence.warnedDirect = 0
			a.taintInfluence.warnedInfluence = 0

		}
	},

	func(a *Agent) {
		if a.orphanFile != nil {
			a.orphanFile.warnings = 0
		}
	},

	func(a *Agent) {
		a.argSizeGuardFires = 0
	},

	func(a *Agent) {
		if a.crossDetectorConsensus != nil {
			a.crossDetectorConsensus.alertsIssued = 0
		}
	},

	func(a *Agent) {
		if a.scopeCreep != nil {
			a.scopeCreep.warnings = 0
		}
	},

	func(a *Agent) {
		if a.searchParamGuard != nil {
			a.searchParamGuard.fires = 0
		}
	},

	func(a *Agent) {
		if a.prematureSuccess != nil {
			a.prematureSuccess.guidanceFired = 0
		}
	},

	func(a *Agent) {
		if a.editCoverage != nil {
			a.editCoverage.warnCount = 0
		}
	},

	func(a *Agent) {
		if a.falsePremise != nil {
			a.falsePremise.warningCount = 0
		}
	},

	func(a *Agent) {
		if a.complexityGate != nil {
			a.complexityGate.fires = 0
		}
	},

	func(a *Agent) {
		if a.permDenyStreak != nil {
			a.permDenyStreak.fires = 0
		}
	},

	func(a *Agent) {
		if a.wtInvalidation != nil {
			a.wtInvalidation.warnedCount = 0
		}
	},

	func(a *Agent) {
		if a.toolEff != nil {
			a.toolEff.mu.Lock()
			for k := range a.toolEff.firedCount {
				delete(a.toolEff.firedCount, k)
			}
			a.toolEff.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.patchExhaust != nil {
			a.patchExhaust.fires = 0
		}
	},
}

func (a *Agent) resetGuidanceCounters() {
	for _, resetQuota := range guidanceCounterResets {
		resetQuota(a)
	}
	debug.Log("guidance", "post-compaction: guidance injection counters reset (B-class detectors)")
}
