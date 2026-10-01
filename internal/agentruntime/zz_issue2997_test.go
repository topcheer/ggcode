package agentruntime

import "testing"

// zz_issue2997_test.go - regression probes for #2997: the failure-switch case
// matched bare substrings first, so "no error" (a progressSignal) was dead
// code  -  any text containing it also contains "error" and hit the failure
// case. Positive phrasings ("fixed the error... now passes") were likewise
// misclassified as failures, flipping succeeded→partial and double-penalizing
// the score in RecursiveTournament.

func TestIssue2997NoErrorIsProgress(t *testing.T) {
	s := SummarizeTrajectory([]TrajectoryEntry{
		{Kind: "result", Text: "go build: no error, all packages compile"},
	})
	if s.Verdict != "succeeded" {
		t.Fatalf("#2997: 'no error' result must be progress (verdict=succeeded), got %q with failures %v", s.Verdict, s.Failures)
	}
	if len(s.Failures) != 0 {
		t.Fatalf("#2997: no failures expected, got %v", s.Failures)
	}
}

func TestIssue2997FixedErrorIsProgress(t *testing.T) {
	s := SummarizeTrajectory([]TrajectoryEntry{
		{Kind: "result", Text: "fixed the error in parser.go; test now passes"},
	})
	if s.Verdict != "succeeded" {
		t.Fatalf("#2997: resolution phrasing must be progress, got %q with failures %v", s.Verdict, s.Failures)
	}
	if len(s.Failures) != 0 {
		t.Fatalf("#2997: no failures expected, got %v", s.Failures)
	}
}

func TestIssue2997KindErrorStillFailure(t *testing.T) {
	// Structural signal wins regardless of wording: kind=error entries stay
	// failures even if their text says "no error" (e.g. an error channel
	// carrying a confusing payload).
	s := SummarizeTrajectory([]TrajectoryEntry{
		{Kind: "result", Text: "tests pass"},
		{Kind: "error", Text: "stderr mentioned: no error but exit 2"},
	})
	if s.Verdict != "partial" {
		t.Fatalf("#2997: kind=error must stay structural failure (partial), got %q", s.Verdict)
	}
	if len(s.Failures) != 1 {
		t.Fatalf("#2997: exactly one failure expected, got %v", s.Failures)
	}
}

func TestIssue2997FixFailedStaysFailure(t *testing.T) {
	// The "fail" exclusion in positiveStatement: "fix failed" contains the
	// resolution word "fixed"? No - it contains "fix" and "failed"; ensure
	// failure wording still wins over any resolution hint.
	s := SummarizeTrajectory([]TrajectoryEntry{
		{Kind: "result", Text: "the previously applied patch: fix failed, tests error out"},
	})
	if s.Verdict == "succeeded" {
		t.Fatalf("#2997: live failure wording must not be rescued, got %q", s.Verdict)
	}
	if len(s.Failures) == 0 {
		t.Fatalf("#2997: expected failure classification")
	}
}

func TestIssue2997ZeroErrorsIsProgress(t *testing.T) {
	s := SummarizeTrajectory([]TrajectoryEntry{
		{Kind: "note", Text: "vet: 0 errors"},
	})
	if s.Verdict != "succeeded" {
		t.Fatalf("#2997: '0 errors' must be progress, got %q", s.Verdict)
	}
}
