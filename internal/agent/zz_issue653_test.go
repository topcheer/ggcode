package agent

import "testing"

// TestIssue653RealFailureOutputFeedsStreak verifies that genuine build/test
// failure output still increments the error streak even when it quotes
// generic infrastructure text (timeouts, connection refused, HTTP status
// lines). Before #653 the marker table matched those substrings anywhere, so
// the exact scenario error_rush exists to catch — the agent blindly "fixing"
// flaky/network-dependent test failures — never accumulated a streak.
func TestIssue653RealFailureOutputFeedsStreak(t *testing.T) {
	realFailures := []string{
		"panic: test timed out after 10m0s\nrunning tests:\n\tTestServer (10m0s)",
		"dial tcp 127.0.0.1:8080: connect: connection refused",
		"--- FAIL: TestAPI | expected 200, got 503 Service Unavailable",
		"exit status 1: build failed: undefined: Foo",
		"lookup server.example: no such host",
		"Get \"https://api/x\": context deadline exceeded (Client.Timeout exceeded)",
		"--- FAIL: TestFlaky | connection reset by peer",
	}
	for _, out := range realFailures {
		if errorRushIsNonCodeError(out) {
			t.Errorf("real code failure misclassified as non-code error (#653): %q", out)
		}
	}
}

// TestIssue653FrameworkErrorsStillExcluded keeps the legitimate #640 scope:
// permission denials and tool/MCP/LSP framework availability failures must
// NOT feed the streak.
func TestIssue653FrameworkErrorsStillExcluded(t *testing.T) {
	environmental := []string{
		"Permission denied by user for tool run_command",
		"mcp server unavailable: connection closed",
		"mcp timeout after 30s",
		"lsp server not running: start the language server first",
		"rate limit exceeded for provider",
		"invalid api key provided",
	}
	for _, out := range environmental {
		if !errorRushIsNonCodeError(out) {
			t.Errorf("environmental error should stay excluded from streak (#640/#653): %q", out)
		}
	}
}

// TestIssue653StreakGrowsOnFlakyTestLoop proves the end-to-end detector
// behavior: repeated failing test runs followed by blind edits now trigger
// the rush warning, where pre-#653 the timeout text suppressed the streak.
func TestIssue653StreakGrowsOnFlakyTestLoop(t *testing.T) {
	s := newErrorRushState()
	if s == nil {
		t.Fatal("state constructor unavailable")
	}
	flaky := "--- FAIL: TestServer (0.00s)\n    panic: test timed out after 10m0s"
	for i := 0; i < errorRushConsecutiveThreshold; i++ {
		s.recordToolCall("run_command", flaky, true)
	}
	if got := s.consecutiveErrors; got != errorRushConsecutiveThreshold {
		t.Fatalf("streak = %d, want %d (timeout text must not suppress code failures, #653)", got, errorRushConsecutiveThreshold)
	}
	// A blind edit right after the failures must count as a rush.
	s.recordToolCall("edit_file", "ok", false)
	if s.rushCount != 1 {
		t.Fatalf("rushCount = %d, want 1 (blind fix after failing test loop undetected)", s.rushCount)
	}
}
