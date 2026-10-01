package agentruntime

import "strings"

import "testing"

func rankedCandidates() []RolloutCandidate {
	return []RolloutCandidate{
		{
			Name: "failed-run",
			Transcript: []TrajectoryEntry{
				{Kind: "action", Text: "attempt direct edit"},
				{Kind: "error", Text: "edit failed: anchor not found"},
			},
		},
		{
			Name: "ok-run",
			Transcript: []TrajectoryEntry{
				{Kind: "action", Text: "plan: read then edit"},
				{Kind: "result", Text: "ok: edit applied, tests pass"},
			},
		},
	}
}

func TestRankSubagentResultsEmpty(t *testing.T) {
	_, _, ok := RankSubagentResults(nil)
	if ok {
		t.Fatal("empty input must return ok=false")
	}
}

func TestRankSubagentResultsSingle(t *testing.T) {
	got, _, ok := RankSubagentResults(rankedCandidates()[:1])
	if !ok || got.Name != "failed-run" {
		t.Fatalf("single candidate must pass through, got %q ok=%v", got.Name, ok)
	}
}

func TestRankSubagentResultsPicksWinner(t *testing.T) {
	best, summary, ok := RankSubagentResults(rankedCandidates())
	if !ok {
		t.Fatal("two candidates must produce a winner")
	}
	if best.Name != "ok-run" {
		t.Fatalf("expected ok-run (progress, no failure) to win, got %q", best.Name)
	}
	if summary.Verdict != "succeeded" {
		t.Fatalf("winner summary verdict = %q, want succeeded", summary.Verdict)
	}
}

func TestConditioningHintEmpty(t *testing.T) {
	if got := ConditioningHint(nil); got != "" {
		t.Fatalf("nil transcript must yield empty hint, got %q", got)
	}
	if got := ConditioningHint([]TrajectoryEntry{}); got != "" {
		t.Fatalf("empty transcript must yield empty hint, got %q", got)
	}
}

func TestConditioningHintDistillsFailure(t *testing.T) {
	got := ConditioningHint([]TrajectoryEntry{
		{Kind: "action", Text: "attempt naive fix"},
		{Kind: "error", Text: "build failed: undefined x"},
	})
	if got == "" {
		t.Fatal("failure transcript must yield a non-empty conditioning hint")
	}
	if !strings.Contains(got, "build failed") {
		t.Fatalf("hint should carry the failure mode, got: %q", got)
	}
}
