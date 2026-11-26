package agent

import "github.com/topcheer/ggcode/internal/debug"

var guidanceCounterResets = []func(*Agent){

	func(a *Agent) {
	},

	func(a *Agent) {
		if a.commitHint != nil {
			a.commitHint.fired = false
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
	},

	func(a *Agent) {
		if a.reproducerLifecycle != nil {
			a.reproducerLifecycle.mu.Lock()
			a.reproducerLifecycle.warned = false
			a.reproducerLifecycle.mu.Unlock()
		}
	},

	func(a *Agent) {
		if a.constraintAmnesia != nil {
			a.constraintAmnesia.warnings = 0
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
		if a.criteriaDrift != nil {
			a.criteriaDrift.warnCount = 0
		}
	},

	func(a *Agent) {
		if a.reasoningRedund != nil {
			a.reasoningRedund.warnCount = 0
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
		if a.actionHedging != nil {
			a.actionHedging.warnings = 0
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
		if a.outcomeMisattrib != nil {
			a.outcomeMisattrib.warnings = 0
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
	},
	func(a *Agent) {
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
	},

	func(a *Agent) {
		if a.compoundedUncert != nil {
			a.compoundedUncert.fired = false
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
		if a.diskSpace != nil {
			a.diskSpace.fired = false
		}
	},

	func(a *Agent) {
		if a.taintInfluence != nil {
			a.taintInfluence.warnedDirect = 0
			a.taintInfluence.warnedInfluence = 0

		}
	},

	func(a *Agent) {
		a.argSizeGuardFires = 0
	},

	func(a *Agent) {
		if a.scopeCreep != nil {
			a.scopeCreep.warnings = 0
		}
	},

	func(a *Agent) {
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
		if a.toolEff != nil {
			a.toolEff.mu.Lock()
			for k := range a.toolEff.firedCount {
				delete(a.toolEff.firedCount, k)
			}
			a.toolEff.mu.Unlock()
		}
	},
}

func (a *Agent) resetGuidanceCounters() {
	for _, resetQuota := range guidanceCounterResets {
		resetQuota(a)
	}
	debug.Log("guidance", "post-compaction: guidance injection counters reset (B-class detectors)")
}
