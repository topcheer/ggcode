package agent

// #640: error_rush counted EVERY failed tool call toward the consecutive
// error streak, including permission denials, MCP/LSP unavailability and
// network timeouts — none of which are the code-level failures (build/test/
// edit errors) the detector documents. Two denied approvals followed by a
// normal edit would fire a false "blind-fixing" warning.

import (
	"strings"
	"testing"
)

func TestIssue640_PermissionDeniedDoesNotBuildStreak(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "[permission] user denied the request", true)
	s.recordToolCall("edit_file", "Permission denied by user", true)
	// A normal successful edit right after must NOT be flagged as a blind fix.
	s.recordToolCall("edit_file", "ok", false)
	if msg := s.check(); msg != "" {
		t.Fatalf("#640 regression: permission denials built the streak, got: %s", msg)
	}
}

func TestIssue640_MCPTimeoutDoesNotBuildStreak(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("mcp__gitea__list_issues", "mcp server timeout after 30s", true)
	s.recordToolCall("lsp_diagnostics", "LSP server not running", true)
	s.recordToolCall("edit_file", "ok", false)
	if msg := s.check(); msg != "" {
		t.Fatalf("#640 regression: environment errors built the streak, got: %s", msg)
	}
}

// Non-code errors must not clobber the streak state either: a code error
// followed by a permission denial still leaves a live streak so a genuine
// second code error plus blind edit still fires.
func TestIssue640_NonCodeErrorDoesNotResetCodeStreak(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "build failed: undefined: foo", true) // code error
	s.recordToolCall("run_command", "request denied by user", true)       // non-code: ignored
	s.recordToolCall("run_command", "test failed: assertion error", true) // code error (streak=2)
	s.recordToolCall("edit_file", "ok", false)                            // blind fix
	msg := s.check()
	if msg == "" {
		t.Fatal("expected genuine code-error streak to still fire after an interleaved non-code error")
	}
	if !strings.Contains(msg, "2 consecutive error(s)") {
		t.Fatalf("expected streak snapshot of 2, got: %s", msg)
	}
}

// Regression: real code-level errors keep feeding the streak.
func TestIssue640_CodeErrorsStillBuildStreak(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "go build: undefined: foo in main.go", true)
	s.recordToolCall("run_command", "FAIL: test_xyz [0.00s]", true)
	s.recordToolCall("edit_file", "ok", false)
	if msg := s.check(); msg == "" {
		t.Fatal("code-level errors must still trigger the rush warning")
	}
}

func TestIssue640_IsNonCodeErrorClassification(t *testing.T) {
	nonCode := []string{
		"Permission denied",
		"user denied the request",
		"operation not permitted",
		"MCP server unavailable",
		"mcp timeout",
		"lsp server not running",
		"rate limit exceeded",
	}
	for _, out := range nonCode {
		if !errorRushIsNonCodeError(out) {
			t.Errorf("expected %q to be classified as non-code error", out)
		}
	}
	// #653: generic infrastructure substrings were removed from the marker
	// table — they routinely appear inside REAL build/test failure output and
	// made the streak blind to flaky-test "blind fixing" loops. They now
	// count as code-level errors (feeding the streak) by design.
	codeErrors := []string{
		"",
		"build failed: undefined: foo",
		"FAIL: TestIssue (exit status 1)",
		"edit failed: old_text not found",
		"undefined variable x",
		"connection refused",
		"context deadline exceeded",
		"request timed out",
		"panic: test timed out after 10m0s",
	}
	for _, out := range codeErrors {
		if errorRushIsNonCodeError(out) {
			t.Errorf("expected %q to be classified as code-level error", out)
		}
	}
}
