package agent

import (
	"testing"
)

func TestNewVerifyDisconnectState(t *testing.T) {
	s := newVerifyDisconnectState()
	if s == nil {
		t.Fatal("expected non-nil state")
	}
	if s.warnings != 0 || s.pendingFailure != nil {
		t.Fatalf("expected clean initial state, got warnings=%d pending=%v", s.warnings, s.pendingFailure)
	}
}

func TestVerifyDisconnectReset(t *testing.T) {
	s := newVerifyDisconnectState()
	s.warnings = 5
	s.pendingFailure = &verifyFailureInfo{toolName: "run_command", snippet: "FAIL"}
	s.reset()
	if s.warnings != 0 || s.pendingFailure != nil {
		t.Fatalf("reset did not clear state")
	}
}

func TestDetectVerificationFailure(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		result   string
		wantFail bool
	}{
		{"test failure", "run_command", "go test ./...\nFAIL\tinternal/agent [build failed]", true},
		{"build error", "run_command", "internal/foo.go:10:5: undefined: bar", true},
		{"panic", "run_command", "panic: runtime error: invalid memory address", true},
		{"success", "run_command", "ok  internal/agent 0.5s\nPASS", false},
		{"build success", "run_command", "BUILD SUCCESSFUL", false},
		{"ambiguous output", "run_command", "running tests...", false},
		{"non-verification tool", "read_file", "some content here", false},
		{"too short", "run_command", "ok", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snippet := detectVerificationFailure(tt.tool, tt.result)
			if tt.wantFail && snippet == "" {
				t.Errorf("expected failure snippet, got empty")
			}
			if !tt.wantFail && snippet != "" {
				t.Errorf("expected no failure, got: %s", snippet)
			}
		})
	}
}

func TestDetectVerificationFailureSnippetExtraction(t *testing.T) {
	result := "some preamble\n--- FAIL: TestFoo (0.00s)\n    foo_test.go:10: expected 5 got 3\nmore output"
	snippet := detectVerificationFailure("run_command", result)
	if snippet == "" {
		t.Fatal("expected non-empty snippet")
	}
	if !contains(snippet, "FAIL") {
		t.Errorf("snippet should contain FAIL indicator, got: %s", snippet)
	}
	if len(snippet) > 200 {
		t.Errorf("snippet too long: %d chars", len(snippet))
	}
}

func TestRecordVerificationResult(t *testing.T) {
	s := newVerifyDisconnectState()

	// Record a failure
	s.recordVerificationResult("run_command", "go test ./...", "FAIL\tinternal/agent", 1)
	if s.pendingFailure == nil {
		t.Fatal("expected pending failure after recording failure result")
	}
	if s.pendingFailure.toolName != "run_command" {
		t.Errorf("expected toolName=run_command, got %s", s.pendingFailure.toolName)
	}

	// Record a success that clears the failure
	s.recordVerificationResult("run_command", "go test ./...", "ok\tinternal/agent", 2)
	if s.pendingFailure != nil {
		t.Fatal("expected pending failure cleared after success")
	}
}

func TestRecordToolCallForVD(t *testing.T) {
	s := newVerifyDisconnectState()
	s.recordVerificationResult("run_command", "go build", "BUILD FAILED", 1)

	// Edit tool should mark as addressed
	s.recordToolCallForVD("edit_file")
	if s.pendingFailure == nil {
		t.Fatal("pending failure was nil after edit")
	}
	if !s.pendingFailure.addressed {
		t.Fatal("expected failure to be addressed after edit_file")
	}
}

func TestMaybeWarnVerifyDisconnectNoFailure(t *testing.T) {
	a := &Agent{verifyDisconnect: newVerifyDisconnectState()}
	hint := a.maybeWarnVerifyDisconnect("The fix is complete and works", 1)
	if hint != "" {
		t.Errorf("expected no warning without pending failure, got: %s", hint)
	}
}

func TestMaybeWarnVerifyDisconnectClaimSuccess(t *testing.T) {
	a := &Agent{verifyDisconnect: newVerifyDisconnectState()}
	a.verifyDisconnect.recordVerificationResult("run_command", "go test ./...", "FAIL\tinternal/agent", 1)

	// Agent claims success despite failure
	hint := a.maybeWarnVerifyDisconnect("The fix is complete and tests pass", 2)
	if hint == "" {
		t.Fatal("expected warning when claiming success after failure")
	}
	if !contains(hint, "Verification Gap") {
		t.Errorf("expected [Verification Gap] prefix, got: %s", hint)
	}

	// Should have been cleared after warning
	if a.verifyDisconnect.pendingFailure != nil {
		t.Fatal("expected pending failure cleared after warning")
	}
}

func TestMaybeWarnVerifyDisconnectStale(t *testing.T) {
	a := &Agent{verifyDisconnect: newVerifyDisconnectState()}
	a.verifyDisconnect.recordVerificationResult("run_command", "go build", "BUILD FAILED", 1)

	// Wait for stale threshold
	hint := a.maybeWarnVerifyDisconnect("working on something else now", 1+verifyDisconnectMaxIterations)
	if hint == "" {
		t.Fatal("expected warning for stale unresolved failure")
	}
	if !contains(hint, "unresolved") {
		t.Errorf("expected stale warning, got: %s", hint)
	}
}

func TestMaybeWarnVerifyDisconnectAddressed(t *testing.T) {
	a := &Agent{verifyDisconnect: newVerifyDisconnectState()}
	a.verifyDisconnect.recordVerificationResult("run_command", "go build", "BUILD FAILED", 1)

	// Agent edits file to address
	a.verifyDisconnect.recordToolCallForVD("edit_file")

	hint := a.maybeWarnVerifyDisconnect("updated the code", 2)
	if hint != "" {
		t.Errorf("expected no warning when failure was addressed, got: %s", hint)
	}
}

func TestMaybeWarnVerifyDisconnectRateLimit(t *testing.T) {
	a := &Agent{verifyDisconnect: newVerifyDisconnectState()}
	a.verifyDisconnect.warnings = verifyDisconnectMaxWarnings

	a.verifyDisconnect.recordVerificationResult("run_command", "go build", "BUILD FAILED", 1)
	hint := a.maybeWarnVerifyDisconnect("fix is complete", 2)
	if hint != "" {
		t.Errorf("expected no warning after rate limit reached")
	}
}

func TestTruncateVD(t *testing.T) {
	if truncateVD("", 80) != "(no input)" {
		t.Error("empty input should return placeholder")
	}
	short := "go test"
	if truncateVD(short, 80) != short {
		t.Error("short input should be unchanged")
	}
	long := "go test " + repeat('x', 100)
	trunc := truncateVD(long, 20)
	if len(trunc) > 20 {
		t.Errorf("truncated too long: %d", len(trunc))
	}
	if !contains(trunc, "...") {
		t.Error("expected ... suffix on truncation")
	}
}

func repeat(ch rune, n int) string {
	result := make([]rune, n)
	for i := range result {
		result[i] = ch
	}
	return string(result)
}
