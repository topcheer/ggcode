package agentruntime

import "testing"

// zz_issue2997_test.go - regression probes for #2997: the classification
// switch tested failure substrings FIRST, so positive statements containing
// "error"/"failed" ("no error, all packages compile", "previously failed
// test now passes") were counted as failures - and the explicit
// progressSignals entry "no error" was dead code (its text always contains
// "error"). Successful rollouts flipped to "partial" and were
// double-penalized in score().

func TestIssue2997NoErrorTextIsProgress(t *testing.T) {
	s := SummarizeTrajectory([]TrajectoryEntry{
		{Kind: "action", Text: "run go build"},
		{Kind: "result", Text: "go build: no error, all packages compile"},
	})
	if s.Verdict != "succeeded" {
		t.Fatalf("#2997: 'no error, all passed' rollout must be succeeded, got %q (failures=%v)", s.Verdict, s.Failures)
	}
	if len(s.Failures) != 0 {
		t.Fatalf("#2997: no failure entries expected, got %v", s.Failures)
	}
}

func TestIssue2997PositiveFailureWordMentionIsProgress(t *testing.T) {
	s := SummarizeTrajectory([]TrajectoryEntry{
		{Kind: "result", Text: "fixed the error in parser.go; test now passes"},
	})
	if len(s.Failures) != 0 {
		t.Fatalf("#2997: 'fixed the error ... passes' must classify as progress, got failures=%v", s.Failures)
	}
	if s.Verdict != "succeeded" {
		t.Fatalf("#2997: verdict must be succeeded, got %q", s.Verdict)
	}
}

func TestIssue2997TrueFailureStillFailure(t *testing.T) {
	// kind=error dominates regardless of wording; and failure-worded result
	// text WITHOUT any progress whitelist word still classifies failure via
	// the looksLikeFailure guard inside the progress branch.
	s := SummarizeTrajectory([]TrajectoryEntry{
		{Kind: "error", Text: "build failed: cannot find package"},
		{Kind: "result", Text: "test failed: timeout after 30s"},
	})
	if s.Verdict == "succeeded" || len(s.Failures) == 0 {
		t.Fatalf("#2997: true failures must stay failures, got verdict=%q entries=%v", s.Verdict, s.Failures)
	}
}
