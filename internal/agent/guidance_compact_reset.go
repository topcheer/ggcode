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
// Rationale: after compaction the model loses the prior guidance message;
// a detector that already burned its once-per-run quota would stay silent
// for the rest of the session even if the same hazard recurs - worse than
// the noise we removed in batch 1.
//
// Structure: the reset is a table-driven registry (guidanceCounterResets)
// wired into the compaction success points (reactive compact in
// agent_compact.go, precompact consume in agent_precompact.go). This closes
// the "structural fix is a registry" note from #1651: each entry is a
// self-contained closure touching exactly one detector's quota fields, so
// adding a detector means appending one entry, and the pin test in
// guidance_compact_reset_1826_test.go still enforces registration by
// matching "a.<field> " literals in this file. Contract unchanged: ONLY
// injection quotas reset; behavioral windows (sliding buffers, ledgers,
// maps) survive. The two byte-identical duplicate entries that the
// historical B-group section re-listed (silentError, scopeDrift) were
// folded into their single original entries - no behavior change, the
// final state is identical either way.

// guidanceCounterResets lists the post-compaction quota reset for every
// B-class detector holding a run-scoped injection quota. Entry order is
// irrelevant: each entry is independent (one detector, no cross-detector
// state).
var guidanceCounterResets = []func(*Agent){
	// -- historical head (pre-#1826 order preserved) --

	// silent_error (fired quota-1; unresolvedErrors ledger is behavioral)
	func(a *Agent) {
		if a.silentError != nil {
			a.silentError.fired = false
		}
	},
	// scope_drift (fired quota-1; editedDirs/editFiles/productiveCount
	// are behavioral facts)
	func(a *Agent) {
		if a.scopeDrift != nil {
			a.scopeDrift.fired = false
		}
	},
	// error_strategy_loop (warningCount quota only; recentResults/cats
	// and firedFor are behavioral - firedFor re-arming would re-nag a
	// strategy already warned about)
	func(a *Agent) {
		if a.errStrategyLoop != nil {
			a.errStrategyLoop.warningCount = 0
		}
	},
	// commit_hint_gate (fired quota-1)
	func(a *Agent) {
		if a.commitHint != nil {
			a.commitHint.fired = false
		}
	},
	// convergence_lock (warned quota-1)
	func(a *Agent) {
		if a.convergenceLock != nil {
			a.convergenceLock.warned = false
		}
	},
	// give_up+revert (#1823 case 2 re-add; fired quota-1 - the pairing
	// evidence is behavioral but the warning budget reopens)
	func(a *Agent) {
		if a.giveupRevert != nil {
			a.giveupRevert.mu.Lock()
			a.giveupRevert.fired = false
			a.giveupRevert.mu.Unlock()
		}
	},
	// input_underspec (warned quota-1)
	func(a *Agent) {
		if a.inputUnderspec != nil {
			a.inputUnderspec.warned = false
		}
	},
	// tool_integration_monitor (warnings quota only; evidence ledger
	// survives)
	func(a *Agent) {
		if a.integrationMonitor != nil {
			a.integrationMonitor.warnings = 0
		}
	},
	// iter_pressure (warningsFired quota only)
	func(a *Agent) {
		if a.iterPressure != nil {
			a.iterPressure.warningsFired = 0
		}
	},
	// premature_commit (warned quota-1)
	func(a *Agent) {
		if a.prematureCommit != nil {
			a.prematureCommit.warned = false
		}
	},
	// reproducer_lifecycle (warned quota only - the lifecycle stage
	// machine is behavioral disk/test state)
	func(a *Agent) {
		if a.reproducerLifecycle != nil {
			a.reproducerLifecycle.mu.Lock()
			a.reproducerLifecycle.warned = false
			a.reproducerLifecycle.mu.Unlock()
		}
	},
	// reversibility_check (warnCount quota; observed-irreversible-actions
	// ledger is behavioral)
	func(a *Agent) {
		if a.reversibility != nil {
			a.reversibility.mu.Lock()
			a.reversibility.warnCount = 0
			a.reversibility.mu.Unlock()
		}
	},
	// tool_result_redundancy (warnings quota only)
	func(a *Agent) {
		if a.toolRedundancy != nil {
			a.toolRedundancy.warnings = 0
		}
	},
	// tunnel_vision (warned quota-1)
	func(a *Agent) {
		if a.tunnelVision != nil {
			a.tunnelVision.warned = false
		}
	},

	// #1826 whitelist - quota-bearing fields deliberately NOT reset here:
	//   monorepoScoper (no quota field; scoping decision is behavioral)
	//   diminishingEdit / errorPropagate / prematureRefactor /
	//   recklessExec (quota fields live on inner sub-states or fire paths
	//   covered by the per-run reset in resetDetectors; none hold a
	//   run-scoped injection quota burned across compaction)
	//   perfBaseline.warnedThisSession (#1180: session-once by design -
	//   injection only happens at run start, no post-compaction re-inject
	//   need)
	//   monorepoScoper.suppressions (#687: re-arm cap is deliberate
	//   anti-retry)
	//   diskSpace IS reset below (fired is a run-scoped quota; the old
	//   note claiming "environmental state, not run-scoped" was stale -
	//   #2440)
	//   overseer hard-escalation sequence (progression must survive)

	// verify_debt
	func(a *Agent) {
		if a.verifyDebt != nil {
			a.verifyDebt.mu.Lock()
			a.verifyDebt.warningsIssued = 0
			a.verifyDebt.mu.Unlock()
		}
	},
	// info_scent
	func(a *Agent) {
		if a.infoScent != nil {
			a.infoScent.mu.Lock()
			a.infoScent.injectionCount = 0
			a.infoScent.mu.Unlock()
		}
	},
	// futile_cycle (no mutex; agent-loop single-goroutine access)
	func(a *Agent) {
		if a.futileCycle != nil {
			a.futileCycle.warningsFired = 0
		}
	},
	// edit_propagation
	func(a *Agent) {
		if a.editPropagation != nil {
			a.editPropagation.mu.Lock()
			a.editPropagation.warningsIssued = 0
			a.editPropagation.mu.Unlock()
		}
	},
	// constraint_amnesia (no mutex; agent-loop single-goroutine access)
	func(a *Agent) {
		if a.constraintAmnesia != nil {
			a.constraintAmnesia.warnings = 0
		}
	},
	// goal_reminder (no mutex; agent-loop single-goroutine access):
	// post-compaction the goal is re-emitted in the system prompt, so the
	// fade-out clock restarts and the per-run quota is refunded.
	func(a *Agent) {
		if a.goalReminder != nil {
			a.goalReminder.reset()
		}
	},
	// correction_spiral (no mutex; agent-loop single-goroutine access)
	func(a *Agent) {
		if a.correctionSpiral != nil {
			a.correctionSpiral.warningCount = 0
		}
	},
	// error_rush (no mutex; agent-loop single-goroutine access)
	func(a *Agent) {
		if a.errorRush != nil {
			a.errorRush.warnCount = 0
		}
	},
	// bare_edit_streak (no mutex; agent-loop single-goroutine access)
	func(a *Agent) {
		if a.bareEditStreak != nil {
			a.bareEditStreak.warnCount = 0
		}
	},
	// attention_fragment (no mutex; agent-loop single-goroutine access)
	func(a *Agent) {
		if a.attentionFragment != nil {
			a.attentionFragment.warnCount = 0
		}
	},
	// tool_thermal (no mutex; agent-loop single-goroutine access)
	func(a *Agent) {
		if a.toolThermal != nil {
			a.toolThermal.warned = false
		}
	},
	// strategy_fixation (no mutex; agent-loop single-goroutine access)
	func(a *Agent) {
		if a.strategyFixation != nil {
			a.strategyFixation.warnCount = 0
		}
	},
	// query_converge
	func(a *Agent) {
		if a.queryConverge != nil {
			a.queryConverge.mu.Lock()
			a.queryConverge.warnCount = 0
			a.queryConverge.warned = false
			a.queryConverge.mu.Unlock()
		}
	},
	// #1465-A: per-run injection counters missing from the original list.
	// The three compaction call sites are MID-RUN overflow retry paths
	// (same run continues), so detectors whose quota burned before the
	// compaction stayed silent for the run's remainder - the opposite of
	// the file's own rationale ("worse than the noise we removed").

	// error_compound (#1572-A: quota only - the sliding window of what
	// actually failed survives compaction)
	func(a *Agent) {
		if a.errorCompound != nil {
			a.errorCompound.mu.Lock()
			a.errorCompound.warningCount = 0
			a.errorCompound.mu.Unlock()
		}
	},
	// success_declare (#1572-A: quota only - prior premature-success
	// declarations remain behavioral facts)
	func(a *Agent) {
		if a.successDeclare != nil {
			a.successDeclare.mu.Lock()
			a.successDeclare.warnCount = 0
			a.successDeclare.fired = false
			a.successDeclare.mu.Unlock()
		}
	},
	// undo_blind (#1572-A: quota only - pendingUndoFiles is DISK state;
	// wiping it right when the model loses its undo memory to compaction
	// guaranteed the exact blind-edit miss the detector exists for)
	func(a *Agent) {
		if a.undoBlind != nil {
			a.undoBlind.warnCount = 0
		}
	},
	// counterfactual_dep (no mutex; agent-loop single-goroutine access)
	// (#1572-A: quota only)
	func(a *Agent) {
		if a.cfDep != nil {
			a.cfDep.warnCount = 0
		}
	},
	// verify_coverage_gap (no mutex; agent-loop single-goroutine access)
	// (#1572-A: quota only - the unverified-edit debt ledger survives;
	// compaction happens on long runs, i.e. at PEAK debt)
	func(a *Agent) {
		if a.editCoverage != nil {
			a.editCoverage.warnCount = 0
		}
	},
	// foresight_calibrate (no mutex; agent-loop single-goroutine access)
	// (#1572-B: reset() cleared predictions only and never touched
	// warnCount - the quota reopen this entry exists for was a no-op)
	func(a *Agent) {
		if a.foresightCalib != nil {
			a.foresightCalib.warnCount = 0
		}
	},
	// #1572-C: six more per-run injection quotas #1465-A missed - each
	// burned-out quota stayed silent for the whole session after burning
	// pre-compaction.
	func(a *Agent) {
		if a.criteriaDrift != nil {
			a.criteriaDrift.warnCount = 0
		}
	},
	// #1843 case 3: editOscillation was missing from this list too -
	// max=1 burned pre-compaction left the detector silent (and the
	// model never saw the warning text) for the rest of the run.
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
	func(a *Agent) {
		if a.verifDebt != nil {
			a.verifDebt.warningsIssued = 0
		}
	},
	// #1651: quota=1 detectors go PERMANENTLY silent after one mid-run
	// compaction swallows their single warning - the worst offenders of
	// the 56-item enumeration gap (mechanical fix: per-state quota reset;
	// the structural fix is this registry).
	func(a *Agent) {
		if a.spiralState != nil {
			a.spiralState.warnings = 0 // "at most once per run" - compact REOPENS it
		}
	},
	func(a *Agent) {
		if a.capBoundary != nil {
			a.capBoundary.warnings = 0 // capBoundaryMaxWarnings=1
		}
	},
	// #1646-2: quota-only (warnCount) - entries/pendingErr are behavioral.
	func(a *Agent) {
		if a.overcorrection != nil {
			a.overcorrection.mu.Lock()
			a.overcorrection.warnCount = 0
			a.overcorrection.mu.Unlock()
		}
	},

	// #1651: the enumerative gap closed. Same contract as above - ONLY
	// injection quotas (int counters + quota bools) reset; behavioral
	// windows (sliding buffers, maps, streaks, time caches, session-level
	// flags like phantomVerify.categoriesEverRun) stay. Locks follow each
	// struct's own convention (mutex where one exists; plain field for
	// single-goroutine agent-loop access).

	// -- A-group int counters --
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
			// quota = per-category warned map (maxWarnings counts TRUE
			// categories); clearing it reopens the quota. maxWarnings is a
			// config constant, not a counter.
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
			// warnings only - categoriesEverRun is session-level (#1478-A)
			// and stays.
			a.phantomVerify.warnings = 0
		}
	},
	func(a *Agent) {
		if a.heterogeneousModel != nil {
			a.heterogeneousModel.mu.Lock()
			a.heterogeneousModel.warnsIssued = 0
			a.heterogeneousModel.mu.Unlock()
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
			// warningCount only - seq/maps are behavioral windows.
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
			// warnings only - grounding ledger is behavioral.
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

	// -- B-group quota bools (fired / warned; quota effectively 1) --
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
	// silentError and scopeDrift also appear at the top of this registry
	// (historical head); reset semantics are identical, so the duplicate
	// B-group re-listing was folded away.
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
	// Safety advisories: fired is a run-level quota (reset at run start);
	// lastCheck is a cross-run time cache and deliberately stays.
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

	// ---- #2440: the seven-plus-eight quota-bearing detectors that the
	// hand-maintained registry missed (each verified: field increments ONLY
	// after guidance injection, short-circuits at its cap, and the field is
	// per-run — the same classification as every entry above). ----

	// batch_coupling: warnsIssued (max 2/run)
	func(a *Agent) {
		if a.batchCoupling != nil {
			a.batchCoupling.warnsIssued = 0
		}
	},
	// taint_influence (SECURITY): direct-taint and influence-taint warning
	// quotas (3/2 per run). Post-compaction silence here closes the safety
	// alert window for tainted data flowing to destructive sinks.
	func(a *Agent) {
		if a.taintInfluence != nil {
			a.taintInfluence.warnedDirect = 0
			a.taintInfluence.warnedInfluence = 0
			// warnedPaths is a behavioral ledger (dedup) and stays.
		}
	},
	// orphan_file: warnings (max 2/run)
	func(a *Agent) {
		if a.orphanFile != nil {
			a.orphanFile.warnings = 0
		}
	},
	// arg_size_guard: fires once per run by design - resetting it after
	// compaction restores the guard for the post-compaction context, which
	// is exactly what a fresh long-run segment needs.
	func(a *Agent) {
		a.argSizeGuardFires = 0
	},
	// cross_detector_consensus: alertsIssued
	func(a *Agent) {
		if a.crossDetectorConsensus != nil {
			a.crossDetectorConsensus.alertsIssued = 0
		}
	},
	// scope_creep: warnings
	func(a *Agent) {
		if a.scopeCreep != nil {
			a.scopeCreep.warnings = 0
		}
	},
	// search_param_guard: fires
	func(a *Agent) {
		if a.searchParamGuard != nil {
			a.searchParamGuard.fires = 0
		}
	},
	// premature_success: guidanceFired
	func(a *Agent) {
		if a.prematureSuccess != nil {
			a.prematureSuccess.guidanceFired = 0
		}
	},
	// verify_coverage_gap (field: editCoverage): warnCount
	func(a *Agent) {
		if a.editCoverage != nil {
			a.editCoverage.warnCount = 0
		}
	},
	// false_premise: warningCount
	func(a *Agent) {
		if a.falsePremise != nil {
			a.falsePremise.warningCount = 0
		}
	},
	// complexity_gate: fires
	func(a *Agent) {
		if a.complexityGate != nil {
			a.complexityGate.fires = 0
		}
	},
	// permission_deny_streak: fires (streak itself is behavioral and stays)
	func(a *Agent) {
		if a.permDenyStreak != nil {
			a.permDenyStreak.fires = 0
		}
	},
	// wt_invalidation: warnedCount
	func(a *Agent) {
		if a.wtInvalidation != nil {
			a.wtInvalidation.warnedCount = 0
		}
	},
	// tool_effectiveness: firedCount is per-tool guidance quota (map of
	// counts); effectiveness stats themselves are behavioral and stay.
	func(a *Agent) {
		if a.toolEff != nil {
			a.toolEff.mu.Lock()
			for k := range a.toolEff.firedCount {
				delete(a.toolEff.firedCount, k)
			}
			a.toolEff.mu.Unlock()
		}
	},
	// patch_exhaust: fires
	func(a *Agent) {
		if a.patchExhaust != nil {
			a.patchExhaust.fires = 0
		}
	},
}

// resetGuidanceCounters clears ONLY the injected-warning counters of the
// B-class detectors, leaving behavioral windows intact. Called after a
// successful context compaction.
func (a *Agent) resetGuidanceCounters() {
	for _, resetQuota := range guidanceCounterResets {
		resetQuota(a)
	}
	debug.Log("guidance", "post-compaction: guidance injection counters reset (B-class detectors)")
}
