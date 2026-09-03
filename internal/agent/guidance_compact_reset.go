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
	// error_compound
	if a.errorCompound != nil {
		a.errorCompound.reset()
	}
	// success_declare
	if a.successDeclare != nil {
		a.successDeclare.reset()
	}
	// undo_blind
	if a.undoBlind != nil {
		a.undoBlind.reset()
	}
	// counterfactual_dep (no mutex; agent-loop single-goroutine access)
	if a.cfDep != nil {
		a.cfDep.reset()
	}
	// verify_coverage_gap (no mutex; agent-loop single-goroutine access)
	if a.editCoverage != nil {
		a.editCoverage.reset()
	}
	// foresight_calibrate (no mutex; agent-loop single-goroutine access)
	if a.foresightCalib != nil {
		a.foresightCalib.reset()
	}
	debug.Log("guidance", "post-compaction: guidance injection counters reset (B-class detectors)")
}
