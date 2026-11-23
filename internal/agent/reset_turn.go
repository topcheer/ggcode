package agent

func (a *Agent) resetTurnDetectors() {
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
	a.buildIdempot.reset()
	a.cfDep.reset()

	a.integrationResetForRun()
	a.resetSelfMod()
	if a.effortAdapter != nil {
		a.effortAdapter.reset()
	}

	a.giveupRevert = &giveupRevertState{}
	a.editCoverage.reset()
	a.phantomVerify.reset()
	a.successDeclare.reset()
	a.criteriaDrift.reset()
	a.reasonAction.reset()
}
