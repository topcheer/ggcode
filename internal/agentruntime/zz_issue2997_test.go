package agentruntime

import "testing"

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
