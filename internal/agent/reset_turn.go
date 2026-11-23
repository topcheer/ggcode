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
	a.argSizeGuardFires = 0
	a.searchParamGuard.reset()
	a.toolRedundancy.reset()
	a.toolEquivDetect.reset()
	a.toolSequence.reset()
	a.obsContract.reset()
	a.monorepoScoper.reset()
	a.actionAnnihil.reset()
	a.exploreFrag.reset()
	a.batchCoupling.reset()
	a.buildIdempot.reset()
	a.orphanFile.reset()
	a.cfDep.reset()

	a.foresightCalib.reset()
	a.expiredRead.reset()

	a.integrationResetForRun()
	a.resetSelfMod()
	a.resetOvercorrection()
	if a.delegationOrch != nil {
		a.delegationOrch.resetForNewTurn()
	}
	if a.effortAdapter != nil {
		a.effortAdapter.reset()
	}
	a.diminishingEdit.reset()
	a.overcorrection.reset()

	a.giveupRevert = &giveupRevertState{}
	a.errorCompound.reset()
	a.bareEditStreak.reset()
	a.editCoverage.reset()
	a.strategyFixation.reset()
	a.errorRush.reset()
	a.phantomVerify.reset()
	if a.recklessExec != nil {
		a.recklessExec.reset()
	}
	a.subgoalTrack.reset()
	a.futileCycle.reset()
	a.toolResultRedundancy.reset()
	a.editPropagation.reset()
	a.successDeclare.reset()
	a.criteriaDrift.reset()
	a.reasonAction.reset()
	a.attemptBrief.reset()
}
