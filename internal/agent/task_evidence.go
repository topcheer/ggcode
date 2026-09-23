package agent

// Task completion evidence gate (sa-183).
//
// Research basis: LongHorizon-Harness (arXiv:2608.01964) reformulates
// long-horizon execution as a task-state management problem and shows that
// keeping completion assessment inside the growing execution context lets
// incorrect self-assessments propagate into later decisions. Its
// Manage-Execute-Audit loop updates task state "only with facts independently
// verified from the environment".
//
// ggcode already externalizes task state (internal/task) and already owns
// mature verification-evidence predicates (unverified_claim.go:
// hasVerificationCommands / hasVerificationTools), but the two never meet:
// task_update flipped tasks to completed with no environment evidence check.
// This file supplies the missing auditor predicate: a thread-safe function
// wired into tool.TaskUpdateTool.EvidenceFn by the host (cmd/ggcode/root.go
// via tui.REPL.SetTaskManager). The gate is an integration of existing
// detectors into the task state machine — not a new detector.
//
// Concurrency: task_update executes on the tool-execution path while the main
// loop mutates RunStats. The live stats pointer is published via atomic
// Store/Load; the fallback (lastRunStats) is guarded by a.mu, matching the
// existing contract in RunStreamWithContent's deferred finalize block.

// setLiveRunStats publishes the active run's stats for concurrent readers.
// Called when a run starts; cleared (nil) when the run's deferred finalize
// block stores the stats into lastRunStats.
func (a *Agent) setLiveRunStats(stats *RunStats) {
	a.liveRunStats.Store(stats)
}

// liveOrLastRunStats returns the active run's stats, or the most recent
// completed run's stats when no run is active. May return nil.
func (a *Agent) liveOrLastRunStats() *RunStats {
	if stats := a.liveRunStats.Load(); stats != nil {
		return stats
	}
	a.mu.RLock()
	stats := a.lastRunStats
	a.mu.RUnlock()
	return stats
}

// TaskVerificationEvidence returns a thread-safe predicate reporting whether
// the active (or most recent) run carries independent verification evidence:
// a successful build/test/lint shell command, or a verification-class tool
// call (lsp_diagnostics, code_health, review_changes, scan_todos).
//
// The predicate is wired as tool.TaskUpdateTool.EvidenceFn so that completed
// task flips are stamped with metadata "verification": "verified" |
// "unverified" (MEA auditor: state updates carry environment facts).
//
// Semantics:
//   - No active run and no prior run: false (no evidence).
//   - Between runs: the most recent run's evidence is used, matching the
//     reality that users may complete planning tasks outside an LLM run.
//   - A FAILED verification command does not count (unverified_claim.go
//     #1521 cross-checks collected error text).
func TaskVerificationEvidence(a *Agent) func() bool {
	return func() bool {
		if a == nil {
			return false
		}
		stats := a.liveOrLastRunStats()
		if stats == nil {
			return false
		}
		if hasVerificationCommands(stats) {
			return true
		}
		return hasVerificationTools(stats)
	}
}
