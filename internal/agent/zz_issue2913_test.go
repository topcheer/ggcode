package agent

import "testing"

// zz_issue2913_test.go - regression probes for #2913: passing-state test
// summaries that contain "0 failed" (cargo, jest, vitest, mocha) must not
// be recorded as failure indicators - the old single-pattern regex hit the
// bare "failed" token and classified it as a test/build failure, which then
// combined with the agent's success narrative into a false outcome-
// misattribution warning.

func TestIssue2913PassingSummariesNotFailures(t *testing.T) {
	cases := []string{
		// cargo test passing summary
		"running 5 tests\ntest tests::a ... ok\n\ntest result: ok. 5 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out",
		// jest/vitest/mocha style
		"Tests:       12 passed, 12 total\nTime:        1.234 s",
		"Tests: 12 passed, 0 failed, 0 skipped, 24 total",
		// zero spelled out
		"SUITE PASSED: zero failures detected",
		// go test (no failed token at all, sanity)
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
		// cargo test FAILURE summary - the nonzero count must fire
		{"test result: FAILED. 3 passed; 2 failed; 0 ignored", "test/build failure"},
		// jest failure
		{"Tests: 10 passed, 2 failed, 12 total", "test/build failure"},
		// plain failure words
		{"build failed with exit code 1", ""},
		// zero-fail line present BUT a real error appears later in the same output
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
	// And the failure variant does set it.
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
