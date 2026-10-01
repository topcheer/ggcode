package agent

// resetTurnDetectors resets every loop-scoped detector and tracker before a
// new user turn starts. It is called once from RunStreamWithContent; any
// state that must not leak across user turns belongs here. When adding a new
// detector field on Agent, add its reset() call in this method.
func (a *Agent) resetTurnDetectors() {
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
	a.crossDetectorConsensus.reset()
	a.taintInfluence.reset()
	a.perfBaseline.reset()
	a.argSizeGuardFires = 0
	a.redundantRead.reset()
	a.patchExhaust.reset()
	a.searchParamGuard.reset()
	a.toolRedundancy.reset()
	a.toolEquivDetect.reset()
	a.toolSequence.reset()
	a.shellNativeHint.reset()
	a.monorepoScoper.reset()
	a.resetBgOrphan()
	a.actionAnnihil.reset()
	a.exploreFrag.reset()
	a.batchCoupling.reset()
	a.buildIdempot.reset()
	a.orphanFile.reset()
	a.cfDep.reset()
	// #1843 case 1: foresightCalib.reset() was never called outside
	// compaction - "at most 2 per run" (file-header promise) was in fact
	// per-LIFETIME: mismatches and warnCount accumulated across every
	// user turn, so after two early warnings the detector stayed silent
	// for the rest of the session.
	a.foresightCalib.reset()
	a.expiredRead.reset()
	// Convergence lock must reset per run so post-verification edit drift
	// counters don't leak across runs (issue #341).
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
	// #1823 case 2: give-up + rollback re-add is per-run.
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
