// Package agentruntime: thin wiring layer over rollout_summary.
//
// First real consumers of the test-time scaling primitives (r321): these
// adapters give sub-agent result merging and error-recovery retry paths a
// one-call entry point without importing the agent core. Wiring into the
// agent loop itself is intentionally left to follow-ups (agent.go is a
// high-risk file; this layer is the dependency-free seam).
package agentruntime

// RolloutCandidate is the minimal input for ranking parallel sub-agent
// results: an identifying name and the raw transcript of one rollout.
type RolloutCandidate struct {
	Name       string
	Transcript []TrajectoryEntry
}

// RankSubagentResults selects the best of several same-task candidate
// results (parallel sub-agent runs). Each transcript is distilled via
// SummarizeTrajectory, then RecursiveTournament picks the winner
// deterministically. Returns (best, true) when a meaningful choice was
// made; (zero, false) when there is nothing to rank or no single winner
// could be distinguished.
func RankSubagentResults(candidates []RolloutCandidate) (RolloutCandidate, RolloutSummary, bool) {
	if len(candidates) == 0 {
		return RolloutCandidate{}, RolloutSummary{}, false
	}
	summaries := make([]RolloutSummary, len(candidates))
	for i, c := range candidates {
		summaries[i] = SummarizeTrajectory(c.Transcript)
	}
	if len(summaries) == 1 {
		return candidates[0], summaries[0], true
	}
	winner := RecursiveTournament(summaries, 4)
	// Map the winning summary back to its candidate: compare on the
	// verdict+sections that score() uses, via a second pass.
	for i, s := range summaries {
		if sameSummary(s, winner) {
			return candidates[i], s, true
		}
	}
	return RolloutCandidate{}, RolloutSummary{}, false
}

// sameSummary reports whether two summaries are field-identical.
func sameSummary(a, b RolloutSummary) bool {
	if a.Verdict != b.Verdict ||
		len(a.Hypotheses) != len(b.Hypotheses) ||
		len(a.Progress) != len(b.Progress) ||
		len(a.Failures) != len(b.Failures) {
		return false
	}
	for i := range a.Hypotheses {
		if a.Hypotheses[i] != b.Hypotheses[i] {
			return false
		}
	}
	for i := range a.Progress {
		if a.Progress[i] != b.Progress[i] {
			return false
		}
	}
	for i := range a.Failures {
		if a.Failures[i] != b.Failures[i] {
			return false
		}
	}
	return true
}

// ConditioningHint turns the previous (failed) attempt's transcript into a
// compact conditioning block for the retry prompt, via DistillIntoPrompt.
// Empty-safe: a nil/empty transcript or an all-failed distillation yields
// "" so callers can always append the result unconditionally.
func ConditioningHint(lastTranscript []TrajectoryEntry) string {
	if len(lastTranscript) == 0 {
		return ""
	}
	return DistillIntoPrompt([]RolloutSummary{SummarizeTrajectory(lastTranscript)})
}
