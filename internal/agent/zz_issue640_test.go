package agent

import (
	"strings"
	"testing"
)

func TestIssue640_PermissionDeniedDoesNotBuildStreak(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "[permission] user denied the request", true)
	s.recordToolCall("edit_file", "Permission denied by user", true)

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

func TestIssue640_NonCodeErrorDoesNotResetCodeStreak(t *testing.T) {
	s := newErrorRushState()
	s.recordToolCall("run_command", "build failed: undefined: foo", true)
	s.recordToolCall("run_command", "request denied by user", true)
	s.recordToolCall("run_command", "test failed: assertion error", true)
	s.recordToolCall("edit_file", "ok", false)
	msg := s.check()
	if msg == "" {
		t.Fatal("expected genuine code-error streak to still fire after an interleaved non-code error")
	}
	if !strings.Contains(msg, "2 consecutive error(s)") {
		t.Fatalf("expected streak snapshot of 2, got: %s", msg)
	}
}

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
