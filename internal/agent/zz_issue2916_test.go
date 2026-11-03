package agent

import "testing"

func TestIssue2916ZeroErrorsLineEndNotFailure(t *testing.T) {
	cases := []string{

		"ruff check .\nAll checks passed!\nFound 0 errors\n",

		"Found 0 errors  \n",

		"Lint finished: zero errors\n",

		"test result: ok. 5 passed; 0 failed; 0 ignored",
	}
	for _, c := range cases {
		if got, typ := containsFailureIndicator(c); got {
			t.Fatalf("#2916: passing summary classified as failure (%q): %.60s", typ, c)
		}
	}
}

func TestIssue2916NonzeroCountsStillFailures(t *testing.T) {
	cases := []struct {
		content string
		wantTyp string
	}{

		{"test result: FAILED. 3 passed; 10 failed; 0 ignored", "test/build failure"},
		{"Found 100 errors\n", "error in output"},
		{"golangci-lint: 20 errors\n5 warnings", "error in output"},

		{"internal/agent/foo.go:12: undefined: Bar", "error in output"},
	}
	for _, c := range cases {
		got, typ := containsFailureIndicator(c.content)
		if !got {
			t.Fatalf("#2916: real failure not detected: %s", c.content)
		}
		if c.wantTyp != "" && typ != c.wantTyp {
			t.Fatalf("failure type = %q, want %q (content: %s)", typ, c.wantTyp, c.content)
		}
	}
}

func TestIssue2916ZeroErrorsDoesNotArmPendingFailure(t *testing.T) {
	s := newOutcomeMisattribState()
	s.recordResult("run_command", "ruff check .\nFound 0 errors\n", false, 1)
	if s.pendingFailureIter != -1 {
		t.Fatalf("#2916: \"Found 0 errors\" armed pendingFailureIter=%d (want -1)", s.pendingFailureIter)
	}

	if msg := s.checkMisattribution("lint passes, no errors — all set", 2); msg != "" {
		t.Fatalf("#2916: false outcome-misattribution fired: %s", msg)
	}

	s2 := newOutcomeMisattribState()
	s2.recordResult("run_command", "Found 3 errors\n", false, 1)
	if s2.pendingFailureIter != 1 {
		t.Fatalf("real \"3 errors\" must arm pendingFailureIter, got %d", s2.pendingFailureIter)
	}
	if msg := s2.checkMisattribution("all set, no errors remaining", 2); msg == "" {
		t.Fatalf("genuine failure + success claim must warn")
	}
}
