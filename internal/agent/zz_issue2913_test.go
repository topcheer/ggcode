package agent

import "testing"

func TestIssue2913PassingSummariesNotFailures(t *testing.T) {
	cases := []string{

		"running 5 tests\ntest tests::a ... ok\n\ntest result: ok. 5 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out",

		"Tests:       12 passed, 12 total\nTime:        1.234 s",
		"Tests: 12 passed, 0 failed, 0 skipped, 24 total",

		"SUITE PASSED: zero failures detected",

		"ok  	github.com/topcheer/ggcode/internal/agent	1.5s",
	}
	for _, c := range cases {
		if got, typ := containsFailureIndicator(c); got {
			t.Fatalf("#2913: passing summary classified as failure (%q -> %q): %s", typ, c[:min(len(c), 60)], "")
		}
	}
}

func TestIssue2913RealFailuresStillDetected(t *testing.T) {
	cases := []struct {
		content string
		wantTyp string
	}{

		{"test result: FAILED. 3 passed; 2 failed; 0 ignored", "test/build failure"},

		{"Tests: 10 passed, 2 failed, 12 total", "test/build failure"},

		{"build failed with exit code 1", ""},

		{"test result: ok. 5 passed; 0 failed; 0 ignored\ninternal/agent/foo.go:12: undefined: Bar", "error in output"},
	}
	for _, c := range cases {
		got, typ := containsFailureIndicator(c.content)
		if !got {
			t.Fatalf("#2913 fix over-suppressed: real failure not detected: %s", c.content)
		}
		if c.wantTyp != "" && typ != c.wantTyp {
			t.Fatalf("failure type = %q, want %q (content: %s)", typ, c.wantTyp, c.content)
		}
	}
}

func TestIssue2913PendingFailureNotSetByPassingSummary(t *testing.T) {
	s := newOutcomeMisattribState()
	s.recordResult("run_command",
		"test result: ok. 5 passed; 0 failed; 0 ignored; 0 measured", false, 3)
	if s.pendingFailureIter != -1 {
		t.Fatalf("#2913: passing cargo summary set pendingFailureIter=%d (want -1)", s.pendingFailureIter)
	}

	s.recordResult("run_command",
		"test result: FAILED. 3 passed; 2 failed; 0 ignored", false, 4)
	if s.pendingFailureIter != 4 {
		t.Fatalf("real failure must set pendingFailureIter, got %d", s.pendingFailureIter)
	}
	if s.pendingFailureType != "test/build failure" {
		t.Fatalf("pendingFailureType = %q, want test/build failure", s.pendingFailureType)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
