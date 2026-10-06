package agentruntime

import (
	"strings"
	"testing"
)

func TestSummarizeTrajectory_Classification(t *testing.T) {
	entries := []TrajectoryEntry{
		{Kind: "note", Text: "plan: rename symbol via LSP"},
		{Kind: "action", Text: "read the file then edit"},
		{Kind: "observation", Text: "noise line that is neither"},
		{Kind: "result", Text: "build passes"},
		{Kind: "error", Text: "vet failed: unused variable x"},
		{Kind: "note", Text: "tests pass check failed"},
	}
	s := SummarizeTrajectory(entries)

	if s.Verdict != "partial" {
		t.Errorf("Verdict = %q, want partial", s.Verdict)
	}
	if len(s.Hypotheses) == 0 {
		t.Error("want hypotheses")
	}
	if !containsLine(s.Progress, "build passes") {
		t.Errorf("Progress = %v, want build passes", s.Progress)
	}
	// "tests pass check failed" contains progress signal but looks like failure.
	if !containsLineSubstr(s.Failures, "tests pass check failed") {
		t.Errorf("Failures = %v, want misclassified-progress guarded", s.Failures)
	}
}

func TestSummarizeTrajectory_CapsAndDedup(t *testing.T) {
	var entries []TrajectoryEntry
	for i := 0; i < 20; i++ {
		entries = append(entries, TrajectoryEntry{Kind: "result", Text: "same ok: line"})
	}
	s := SummarizeTrajectory(entries)
	if len(s.Progress) != 1 {
		t.Errorf("dedup failed: Progress = %v", s.Progress)
	}
	if got := len([]rune(compactLine(strings.Repeat("x", 500), 100))); got > 101 {
		t.Errorf("compactLine exceeded limit: %d runes", got)
	}
}

func TestTournament(t *testing.T) {
	good := RolloutSummary{Verdict: "succeeded", Progress: []string{"a", "b"}}
	partial := RolloutSummary{Verdict: "partial", Progress: []string{"a"}, Failures: []string{"error: boom"}}
	bad := RolloutSummary{Verdict: "failed", Failures: []string{"panic: x", "timeout"}}

	if got := Compare(good, bad); got.Verdict != "succeeded" {
		t.Errorf("Compare(good,bad) = %q", got.Verdict)
	}
	w := RecursiveTournament([]RolloutSummary{bad, partial, good, bad}, 4)
	if w.Verdict != "succeeded" {
		t.Errorf("winner = %q, want succeeded", w.Verdict)
	}
	if got := RecursiveTournament(nil, 4); got.Verdict != "failed" {
		t.Errorf("empty field winner = %q", got.Verdict)
	}
	// Determinism: same input, same winner content.
	w2 := RecursiveTournament([]RolloutSummary{bad, partial, good, bad}, 4)
	if strings.Join(w.Progress, "|") != strings.Join(w2.Progress, "|") {
		t.Error("tournament not deterministic")
	}
}

func TestDistillIntoPrompt(t *testing.T) {
	if DistillIntoPrompt(nil) != "" {
		t.Error("want empty string for no prior")
	}
	out := DistillIntoPrompt([]RolloutSummary{{
		Verdict:    "partial",
		Progress:   []string{"build passes"},
		Failures:   []string{"timeout on tests"},
		Hypotheses: []string{"plan: use LSP"},
	}})
	for _, want := range []string{"Prior attempts", "build passes", "timeout on tests", "plan: use LSP"} {
		if !strings.Contains(out, want) {
			t.Errorf("distilled prompt missing %q:\n%s", want, out)
		}
	}
}

func containsLine(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsLineSubstr(list []string, substr string) bool {
	for _, x := range list {
		if strings.Contains(x, substr) {
			return true
		}
	}
	return false
}
