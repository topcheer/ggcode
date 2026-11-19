package context

// mutsource.go: mutation-source vocabulary for #5812 ctxstats.
//
// Every nonTailMutSeq++ call site in manager.go records WHY the history
// changed, so the per-turn sampler (internal/ctxstats) can classify a
// head-hash drift as expected or unexplained. The vocabulary is owned here
// because the Manager owns nonTailMutSeq; internal/ctxstats maps these
// source strings onto the reason enum (expected-append | compaction |
// ptl-truncate | system-rewrite | unknown).
const (
	// MutSourceReconcile: ReconcileToolCalls relocated late tool results
	// and removed stale blocks. History-only - cannot explain head drift.
	MutSourceReconcile = "reconcile"

	// MutSourceOrphanCleanup: removeOrphanToolResults dropped orphaned
	// tool results. History-only.
	MutSourceOrphanCleanup = "orphan-cleanup"

	// MutSourceSystemRewrite: UpdateFirstSystemMessage replaced the system
	// head with different content.
	MutSourceSystemRewrite = "system-rewrite"

	// MutSourceSystemInsert: UpdateFirstSystemMessage inserted a system
	// head where none existed.
	MutSourceSystemInsert = "system-insert"

	// MutSourceCompaction: ApplyCompactResult / Summarize rebuilt the
	// history around a summary.
	MutSourceCompaction = "compaction"

	// MutSourceUserReset: Clear dropped the conversation (user reset).
	// Classified into the compaction family: a deliberate history rebuild,
	// not unexplained drift.
	MutSourceUserReset = "user-reset"

	// MutSourcePTLTruncate: TruncateOldestGroupForRetry dropped the oldest
	// group on a prompt-too-long retry.
	MutSourcePTLTruncate = "ptl-truncate"

	// MutSourceRemoveLastGroup: RemoveLastAssistantGroup rolled back the
	// trailing assistant group. History-only.
	MutSourceRemoveLastGroup = "remove-last-group"

	// MutSourceMechanicalCompaction: commitMechanicalCompaction shrank
	// message content in place.
	MutSourceMechanicalCompaction = "mechanical-compaction"
)
