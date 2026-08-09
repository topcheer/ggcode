package agent

import (
	"testing"
)

func TestPrematureSuccess_EditWithoutVerifyThenClaim(t *testing.T) {
	s := newPrematureSuccessState()

	// Agent edits a file (no verification)
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"})

	// Agent claims success without verifying
	hint := s.checkSuccessClaim("The issue is fixed. All tests pass now.")
	if hint == "" {
		t.Fatal("expected guidance when success claim made without verification after edits")
	}
	if s.guidanceFired != 1 {
		t.Fatalf("expected guidanceFired=1, got %d", s.guidanceFired)
	}
}

func TestPrematureSuccess_VerifiedThenClaim_NoFire(t *testing.T) {
	s := newPrematureSuccessState()

	// Agent edits, then verifies
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"})
	s.recordToolCall("run_command", map[string]interface{}{"command": "go test ./..."})

	// Claims success - should NOT fire because verification was done
	hint := s.checkSuccessClaim("All tests pass. Done!")
	if hint != "" {
		t.Fatal("should not fire when verification was done before claim")
	}
}

func TestPrematureSuccess_NoEdits_NoFire(t *testing.T) {
	s := newPrematureSuccessState()

	// No edits, just claims success
	hint := s.checkSuccessClaim("All tests pass.")
	if hint != "" {
		t.Fatal("should not fire when no edits were made")
	}
}

func TestPrematureSuccess_NonVerifyCommand_NoReset(t *testing.T) {
	s := newPrematureSuccessState()

	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"})
	// Non-verify command should NOT reset editsSinceVerify
	s.recordToolCall("run_command", map[string]interface{}{"command": "ls -la"})

	hint := s.checkSuccessClaim("The issue is resolved.")
	if hint == "" {
		t.Fatal("expected fire - ls is not a verify command")
	}
}

func TestPrematureSuccess_ConditionalClaim_NoFire(t *testing.T) {
	s := newPrematureSuccessState()

	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"})

	hint := s.checkSuccessClaim("If all tests pass, the task is complete.")
	if hint != "" {
		t.Fatal("should not fire on conditional/hypothetical claims")
	}
}

func TestPrematureSuccess_MaxFires(t *testing.T) {
	s := newPrematureSuccessState()

	s.recordToolCall("edit_file", nil)
	hint1 := s.checkSuccessClaim("The issue is fixed.")
	if hint1 == "" {
		t.Fatal("first claim should fire")
	}
	// Second claim should not fire (maxFires=1)
	hint2 := s.checkSuccessClaim("All tests pass.")
	if hint2 != "" {
		t.Fatal("should not fire more than once per run")
	}
}

func TestPrematureSuccess_Reset(t *testing.T) {
	s := newPrematureSuccessState()

	s.recordToolCall("edit_file", nil)
	_ = s.checkSuccessClaim("Done!")
	s.reset()

	if s.editsSinceVerify != 0 || s.everVerified || s.guidanceFired != 0 {
		t.Fatal("reset should clear all state")
	}
}

func TestPrematureSuccess_MultiEditCounts(t *testing.T) {
	s := newPrematureSuccessState()

	s.recordToolCall("multi_edit_file", map[string]interface{}{"file_path": "/foo.go"})
	s.recordToolCall("multi_file_edit", map[string]interface{}{})

	if s.editsSinceVerify != 2 {
		t.Fatalf("expected editsSinceVerify=2, got %d", s.editsSinceVerify)
	}
}

func TestPsIsVerifyCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", true},
		{"go build ./...", true},
		{"make test", true},
		{"npm test", true},
		{"pytest -v", true},
		{"cargo test", true},
		{"make lint", true},
		{"go vet ./...", true},
		{"ls -la", false},
		{"cat file.txt", false},
		{"echo hello", false},
		{"", false},
	}

	for _, tt := range tests {
		got := psIsVerifyCommand(tt.cmd)
		if got != tt.want {
			t.Errorf("psIsVerifyCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestPrematureSuccess_NoSessionVerifyHint(t *testing.T) {
	s := newPrematureSuccessState()

	s.recordToolCall("edit_file", nil)
	hint := s.checkSuccessClaim("The implementation is complete.")

	if hint == "" {
		t.Fatal("expected hint")
	}
	if s.everVerified {
		t.Fatal("everVerified should be false when no verify ran")
	}
	// The hint should mention that no verification was run at all
	if !contains(hint, "no verification") {
		t.Fatalf("hint should mention no verification: %s", hint)
	}
}

func psContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && psContainsStr(s, substr))
}

func psContainsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
