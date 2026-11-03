package agentruntime

type RolloutCandidate struct {
	Name       string
	Transcript []TrajectoryEntry
}

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

	for i, s := range summaries {
		if sameSummary(s, winner) {
			return candidates[i], s, true
		}
	}
	return RolloutCandidate{}, RolloutSummary{}, false
}

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

func ConditioningHint(lastTranscript []TrajectoryEntry) string {
	if len(lastTranscript) == 0 {
		return ""
	}
	return DistillIntoPrompt([]RolloutSummary{SummarizeTrajectory(lastTranscript)})
}
