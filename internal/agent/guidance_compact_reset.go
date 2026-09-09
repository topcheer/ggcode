package agent

import "github.com/topcheer/ggcode/internal/debug"

// Guidance Post-Compaction Reset (batch 2 of the guidance-noise cleanup)
//
// Design contract (user decision, 2026-08-26):
//   - B-class detectors (cross-turn state guards + real static bug checks)
//     may inject AT MOST ONCE per run.
//   - After a context compaction the injected-guidance counters reset, because
//     the guidance text itself was compacted away - the model can no longer
//     see it, so the "at most once" budget should start over. Behavioral
//     windows (sliding buffers, streak counters) are NOT reset: they track
//     what actually happened and remain valid across compaction.
//
// This file implements the narrow "reset injection counters only" operation
// as resetGuidanceCounters, wired into the compaction success points
// (reactive compact in agent_compact.go, precompact consume in
// agent_precompact.go).
//
// Rationale: after compaction the model loses the prior guidance message;
// a detector that already burned its once-per-run quota would stay silent
// for the rest of the session even if the same hazard recurs - worse than
// the noise we removed in batch 1.

// resetGuidanceCounters clears ONLY the injected-warning counters of the
// B-class detectors, leaving behavioral windows intact. Called after a
// successful context compaction.
func (a *Agent) resetGuidanceCounters() {
	// verify_debt
	if a.verifyDebt != nil {
		a.verifyDebt.mu.Lock()
		a.verifyDebt.warningsIssued = 0
		a.verifyDebt.mu.Unlock()
	}
	// info_scent
	if a.infoScent != nil {
		a.infoScent.mu.Lock()
		a.infoScent.injectionCount = 0
		a.infoScent.mu.Unlock()
	}
	// futile_cycle (no mutex; agent-loop single-goroutine access)
	if a.futileCycle != nil {
		a.futileCycle.warningsFired = 0
	}
	// edit_propagation
	if a.editPropagation != nil {
		a.editPropagation.mu.Lock()
		a.editPropagation.warningsIssued = 0
		a.editPropagation.mu.Unlock()
	}
	// constraint_amnesia (no mutex; agent-loop single-goroutine access)
	if a.constraintAmnesia != nil {
		a.constraintAmnesia.warnings = 0
	}
	// correction_spiral (no mutex; agent-loop single-goroutine access)
	if a.correctionSpiral != nil {
		a.correctionSpiral.warningCount = 0
	}
	// error_rush (no mutex; agent-loop single-goroutine access)
	if a.errorRush != nil {
		a.errorRush.warnCount = 0
	}
	// bare_edit_streak (no mutex; agent-loop single-goroutine access)
	if a.bareEditStreak != nil {
		a.bareEditStreak.warnCount = 0
	}
	// attention_fragment (no mutex; agent-loop single-goroutine access)
	if a.attentionFragment != nil {
		a.attentionFragment.warnCount = 0
	}
	// tool_thermal (no mutex; agent-loop single-goroutine access)
	if a.toolThermal != nil {
		a.toolThermal.warned = false
	}
	// strategy_fixation (no mutex; agent-loop single-goroutine access)
	if a.strategyFixation != nil {
		a.strategyFixation.warnCount = 0
	}
	// query_converge
	if a.queryConverge != nil {
		a.queryConverge.mu.Lock()
		a.queryConverge.warnCount = 0
		a.queryConverge.warned = false
		a.queryConverge.mu.Unlock()
	}
	// #1465-A: per-run injection counters missing from the original list.
	// The three compaction call sites are MID-RUN overflow retry paths
	// (same run continues), so detectors whose quota burned before the
	// compaction stayed silent for the run's remainder - the opposite of
	// the file's own rationale ("worse than the noise we removed").
	// error_compound (#1572-A: quota only - the sliding window of what
	// actually failed survives compaction)
	if a.errorCompound != nil {
		a.errorCompound.mu.Lock()
		a.errorCompound.warningCount = 0
		a.errorCompound.mu.Unlock()
	}
	// success_declare (#1572-A: quota only - prior premature-success
	// declarations remain behavioral facts)
	if a.successDeclare != nil {
		a.successDeclare.mu.Lock()
		a.successDeclare.warnCount = 0
		a.successDeclare.fired = false
		a.successDeclare.mu.Unlock()
	}
	// undo_blind (#1572-A: quota only - pendingUndoFiles is DISK state;
	// wiping it right when the model loses its undo memory to compaction
	// guaranteed the exact blind-edit miss the detector exists for)
	if a.undoBlind != nil {
		a.undoBlind.warnCount = 0
	}
	// counterfactual_dep (no mutex; agent-loop single-goroutine access)
	// (#1572-A: quota only)
	if a.cfDep != nil {
		a.cfDep.warnCount = 0
	}
	// verify_coverage_gap (no mutex; agent-loop single-goroutine access)
	// (#1572-A: quota only - the unverified-edit debt ledger survives;
	// compaction happens on long runs, i.e. at PEAK debt)
	if a.editCoverage != nil {
		a.editCoverage.warnCount = 0
	}
	// foresight_calibrate (no mutex; agent-loop single-goroutine access)
	// (#1572-B: reset() cleared predictions only and never touched
	// warnCount - the quota reopen this entry exists for was a no-op)
	if a.foresightCalib != nil {
		a.foresightCalib.warnCount = 0
	}
	// #1572-C: six more per-run injection quotas #1465-A missed - each
	// burned-out quota stayed silent for the whole session after burning
	// pre-compaction.
	if a.criteriaDrift != nil {
		a.criteriaDrift.warnCount = 0
	}
	if a.subgoalTrack != nil {
		a.subgoalTrack.fired = false
	}
	if a.reasoningRedund != nil {
		a.reasoningRedund.warnCount = 0
	}
	if a.strategyStagnation != nil {
		a.strategyStagnation.warnings = 0
	}
	if a.actionAnnihil != nil {
		a.actionAnnihil.mu.Lock()
		a.actionAnnihil.warnsIssued = 0
		a.actionAnnihil.mu.Unlock()
	}
	// #1605-A: verifDebt was named by #1572-C's own standard (per-run
	// injection quota) but never listed - a burned quota (maxWarn=2) plus
	// mid-run compaction left it silent for the run's remainder at PEAK
	// debt. Quota ONLY (warningsIssued); the debt/maxDebt/green-build
	// ledger is behavioral state and wiping it would repeat #1572-A's
	// over-reset (see reset()'s nine fields).
	// #1646-1: the #1605-A block operated a.verifyDebt (max=1, already
	// reset above - byte-identical duplicate, a NO-OP for the issue's own
	// scenario). The commit message named verifDebt (maxWarn=2, the SAUP
	// debt model) - after mid-run compaction that detector stayed muted
	// for the rest of the run. Its struct has no mutex (single-goroutine
	// access, same pattern as correctionSpiral). #1646-2:
	// overcorrection_cascade (maxWarn=2, consumed in-loop) completes the
	// same-family list.
	if a.verifDebt != nil {
		a.verifDebt.warningsIssued = 0
	}
	// #1651: quota=1 detectors go PERMANENTLY silent after one mid-run
	// compaction swallows their single warning - the worst offenders of
	// the 56-item enumeration gap (mechanical fix: per-state quota reset;
	// the structural fix is a registry, tracked in the issue).
	if a.spiralState != nil {
		a.spiralState.warnings = 0 // "at most once per run" - compact REOPENS it
	}
	if a.capBoundary != nil {
		a.capBoundary.warnings = 0 // capBoundaryMaxWarnings=1
	}
	// #1646-2: quota-only (warnCount) - entries/pendingErr are behavioral.
	if a.overcorrection != nil {
		a.overcorrection.mu.Lock()
		a.overcorrection.warnCount = 0
		a.overcorrection.mu.Unlock()
	}

	// #1651: the enumerative gap closed. Same contract as above - ONLY
	// injection quotas (int counters + quota bools) reset; behavioral
	// windows (sliding buffers, maps, streaks, time caches, session-level
	// flags like phantomVerify.categoriesEverRun) stay. Locks follow each
	// struct's own convention (mutex where one exists; plain field for
	// single-goroutine agent-loop access).

	// -- A-group int counters --
	if a.trajectoryHealth != nil {
		a.trajectoryHealth.warnings = 0
	}
	if a.planAbandon != nil {
		a.planAbandon.warnings = 0
	}
	if a.redundantReverify != nil {
		a.redundantReverify.warnings = 0
	}
	if a.constraintViolation != nil {
		a.constraintViolation.warnings = 0
	}
	if a.reasonAction != nil {
		a.reasonAction.mu.Lock()
		a.reasonAction.warnings = 0
		a.reasonAction.mu.Unlock()
	}
	if a.contradiction != nil {
		a.contradiction.warnings = 0
	}
	if a.strategyExhaustion != nil {
		a.strategyExhaustion.mu.Lock()
		a.strategyExhaustion.warningCount = 0
		a.strategyExhaustion.mu.Unlock()
	}
	if a.actionHedging != nil {
		a.actionHedging.warnings = 0
	}
	if a.delegationOrch != nil {
		a.delegationOrch.mu.Lock()
		a.delegationOrch.orphanWarnCount = 0
		a.delegationOrch.serialWarnCount = 0
		a.delegationOrch.overDelWarned = false
		a.delegationOrch.mu.Unlock()
	}
	if a.buildIdempot != nil {
		a.buildIdempot.mu.Lock()
		a.buildIdempot.warnsIssued = 0
		a.buildIdempot.mu.Unlock()
	}
	if a.toolResultRedundancy != nil {
		a.toolResultRedundancy.warningsFired = 0
	}
	if a.editAbandon != nil {
		a.editAbandon.mu.Lock()
		a.editAbandon.warnings = 0
		a.editAbandon.fired = false
		a.editAbandon.mu.Unlock()
	}
	if a.outcomeMisattrib != nil {
		a.outcomeMisattrib.warnings = 0
	}
	if a.causalAttribution != nil {
		a.causalAttribution.warnings = 0
	}
	if a.mindlessAction != nil {
		a.mindlessAction.warnings = 0
	}
	if a.toolTargetMismatch != nil {
		a.toolTargetMismatch.warnings = 0
	}
	if a.toolEquivDetect != nil {
		a.toolEquivDetect.warnings = 0
	}
	if a.tokenWasteBudget != nil {
		a.tokenWasteBudget.mu.Lock()
		a.tokenWasteBudget.warnings = 0
		a.tokenWasteBudget.mu.Unlock()
	}
	if a.truncClaim != nil {
		a.truncClaim.warnings = 0
	}
	if a.solutionFixation != nil {
		a.solutionFixation.warningCount = 0
	}
	if a.fixAmnesia != nil {
		// quota = per-category warned map (maxWarnings counts TRUE
		// categories); clearing it reopens the quota. maxWarnings is a
		// config constant, not a counter.
		a.fixAmnesia.mu.Lock()
		a.fixAmnesia.warned = make(map[string]bool)
		a.fixAmnesia.mu.Unlock()
	}
	if a.errRegression != nil {
		a.errRegression.warningCount = 0
	}
	if a.phantomVerify != nil {
		// warnings only - categoriesEverRun is session-level (#1478-A)
		// and stays.
		a.phantomVerify.warnings = 0
	}
	if a.heterogeneousModel != nil {
		a.heterogeneousModel.mu.Lock()
		a.heterogeneousModel.warnsIssued = 0
		a.heterogeneousModel.mu.Unlock()
	}
	if a.selfMod != nil {
		a.selfMod.mu.Lock()
		a.selfMod.warningCount = 0
		a.selfMod.mu.Unlock()
	}
	if a.prematureAbstr != nil {
		a.prematureAbstr.warnings = 0
	}
	if a.circularReasoning != nil {
		a.circularReasoning.warnings = 0
	}
	if a.stalledConvergence != nil {
		a.stalledConvergence.warningCount = 0
	}
	if a.expiredRead != nil {
		// warningCount only - seq/maps are behavioral windows.
		a.expiredRead.warningCount = 0
	}
	if a.searchInvalidation != nil {
		a.searchInvalidation.warningCount = 0
	}
	if a.irrevGate != nil {
		// warnings only - grounding ledger is behavioral.
		a.irrevGate.warnings = 0
	}
	if a.exploreFrag != nil {
		a.exploreFrag.mu.Lock()
		a.exploreFrag.warnings = 0
		a.exploreFrag.mu.Unlock()
	}

	// -- B-group quota bools (fired / warned; quota effectively 1) --
	if a.goalDriftCtx != nil {
		a.goalDriftCtx.mu.Lock()
		a.goalDriftCtx.warned = false
		a.goalDriftCtx.mu.Unlock()
	}
	if a.planDrift != nil {
		a.planDrift.fired = false
	}
	if a.driftRecurrence != nil {
		a.driftRecurrence.fired = false
		a.driftRecurrence.warned = false
	}
	if a.fulfillmentGate != nil {
		a.fulfillmentGate.fired = false
	}
	if a.scopeNarrow != nil {
		a.scopeNarrow.fired = false
	}
	if a.specGaming != nil {
		a.specGaming.fired = false
	}
	if a.selfCorrectionGate != nil {
		a.selfCorrectionGate.fired = false
	}
	if a.crossFileImpact != nil {
		a.crossFileImpact.mu.Lock()
		a.crossFileImpact.fired = false
		a.crossFileImpact.mu.Unlock()
	}
	if a.fileChurn != nil {
		a.fileChurn.fired = false
	}
	if a.silentError != nil {
		a.silentError.fired = false
	}
	if a.compoundedUncert != nil {
		a.compoundedUncert.fired = false
	}
	if a.scopeDrift != nil {
		a.scopeDrift.fired = false
	}
	if a.compoundingFailure != nil {
		a.compoundingFailure.fired = false
	}
	if a.branchGuard != nil {
		a.branchGuard.fired = false
	}
	if a.changeReconcile != nil {
		a.changeReconcile.fired = false
	}
	if a.companionGuard != nil {
		a.companionGuard.fired = false
	}
	if a.ambiguityPoint != nil {
		a.ambiguityPoint.mu.Lock()
		a.ambiguityPoint.fired = false
		a.ambiguityPoint.mu.Unlock()
	}
	if a.serialRead != nil {
		a.serialRead.mu.Lock()
		a.serialRead.fired = false
		a.serialRead.mu.Unlock()
	}
	if a.diffSummary != nil {
		a.diffSummary.fired = false
	}
	if a.unverifiedClaim != nil {
		a.unverifiedClaim.fired = false
	}
	// Safety advisories: fired is a run-level quota (reset at run start);
	// lastCheck is a cross-run time cache and deliberately stays.
	if a.envDrift != nil {
		a.envDrift.fired = false
	}
	if a.diskSpace != nil {
		a.diskSpace.fired = false
	}

	debug.Log("guidance", "post-compaction: guidance injection counters reset (B-class detectors)")
}
