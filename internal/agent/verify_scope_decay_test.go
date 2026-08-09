package agent

import (
	"testing"
)

func TestClassifyVerificationScope(t *testing.T) {
	tests := []struct {
		cmd   string
		scope int
		isVer bool
	}{
		// Full scope
		{"go test ./...", vsdScopeFull, true},
		{"go test ./...", vsdScopeFull, true},
		{"go build ./...", vsdScopeFull, true},
		{"go vet ./...", vsdScopeFull, true},
		{"make test", vsdScopeFull, true},
		{"make verify-ci", vsdScopeFull, true},
		{"npm test", vsdScopeFull, true},
		{"pytest", vsdScopeFull, true},
		{"cargo test", vsdScopeFull, true},

		// Partial scope
		{"go test ./internal/agent/...", vsdScopePartial, true},
		{"go test ./internal/...", vsdScopePartial, true},
		{"pytest tests/", vsdScopePartial, true},

		// Minimal scope
		{"go test -run TestFoo ./...", vsdScopeMinimal, true},
		{"go test -run=TestBar", vsdScopeMinimal, true},
		{"go build main.go", vsdScopeMinimal, true},
		{"go vet server.go", vsdScopeMinimal, true},
		{"go build ./cmd/foo", vsdScopeMinimal, true},

		// Not verification
		{"ls -la", 0, false},
		{"echo hello", 0, false},
		{"", 0, false},
		{"git status", 0, false},
	}

	for _, tt := range tests {
		scope, ok := classifyVerificationScope(tt.cmd)
		if ok != tt.isVer {
			t.Errorf("classifyVerificationScope(%q): got ok=%v, want %v", tt.cmd, ok, tt.isVer)
			continue
		}
		if ok && scope != tt.scope {
			t.Errorf("classifyVerificationScope(%q): got scope=%d, want %d", tt.cmd, scope, tt.scope)
		}
	}
}

func TestVerifyScopeDecay_MonotonicDecrease(t *testing.T) {
	s := newVerifyScopeDecayState()

	// FULL -> PARTIAL -> MINIMAL
	s.recordVerification("run_command", "go test ./...", 1)
	s.recordVerification("run_command", "go test ./internal/agent/...", 3)
	s.recordVerification("run_command", "go test -run TestFoo", 5)

	hint := s.maybeWarnScopeDecay()
	if hint == "" {
		t.Fatal("expected scope decay warning for monotonic decrease, got empty")
	}
	if s.warnings != 1 {
		t.Errorf("expected 1 warning, got %d", s.warnings)
	}
}

func TestVerifyScopeDecay_FullToMinimalJump(t *testing.T) {
	s := newVerifyScopeDecayState()

	// FULL -> FULL -> MINIMAL (two-level jump)
	s.recordVerification("run_command", "go test ./...", 1)
	s.recordVerification("run_command", "go build ./...", 3)
	s.recordVerification("run_command", "go test -run TestBar", 5)

	hint := s.maybeWarnScopeDecay()
	if hint == "" {
		t.Fatal("expected scope decay warning for FULL->MINIMAL jump")
	}
}

func TestVerifyScopeDecay_NoDecay_Stable(t *testing.T) {
	s := newVerifyScopeDecayState()

	// All full scope - no decay
	s.recordVerification("run_command", "go test ./...", 1)
	s.recordVerification("run_command", "go build ./...", 3)
	s.recordVerification("run_command", "go test ./...", 5)

	hint := s.maybeWarnScopeDecay()
	if hint != "" {
		t.Errorf("expected no warning for stable scope, got: %s", hint)
	}
}

func TestVerifyScopeDecay_NoDecay_Increasing(t *testing.T) {
	s := newVerifyScopeDecayState()

	// Scope increases: MINIMAL -> PARTIAL -> FULL
	s.recordVerification("run_command", "go test -run TestFoo", 1)
	s.recordVerification("run_command", "go test ./internal/...", 3)
	s.recordVerification("run_command", "go test ./...", 5)

	hint := s.maybeWarnScopeDecay()
	if hint != "" {
		t.Errorf("expected no warning for increasing scope, got: %s", hint)
	}
}

func TestVerifyScopeDecay_TooFewVerifications(t *testing.T) {
	s := newVerifyScopeDecayState()

	s.recordVerification("run_command", "go test ./...", 1)
	s.recordVerification("run_command", "go test -run TestFoo", 3)

	hint := s.maybeWarnScopeDecay()
	if hint != "" {
		t.Errorf("expected no warning with <3 verifications, got: %s", hint)
	}
}

func TestVerifyScopeDecay_NonMonotonicOverallDecay(t *testing.T) {
	s := newVerifyScopeDecayState()

	// FULL, PARTIAL, FULL, MINIMAL - overall downward with 2 narrower steps
	s.recordVerification("run_command", "go test ./...", 1)
	s.recordVerification("run_command", "go test ./internal/agent/...", 3)
	s.recordVerification("run_command", "go test ./...", 5)
	s.recordVerification("run_command", "go test -run TestFoo", 7)

	hint := s.maybeWarnScopeDecay()
	if hint == "" {
		t.Fatal("expected warning for non-monotonic overall decay (FULL->MINIMAL with 2 narrower)")
	}
}

func TestVerifyScopeDecay_MaxWarnings(t *testing.T) {
	s := newVerifyScopeDecayState()

	s.recordVerification("run_command", "go test ./...", 1)
	s.recordVerification("run_command", "go test ./internal/...", 3)
	s.recordVerification("run_command", "go test -run TestFoo", 5)

	hint1 := s.maybeWarnScopeDecay()
	if hint1 == "" {
		t.Fatal("expected first warning")
	}
	// Add more decaying verifications and check again
	s.recordVerification("run_command", "go test -run TestBar", 7)
	hint2 := s.maybeWarnScopeDecay()
	if hint2 != "" {
		t.Errorf("expected no second warning (capped at 1), got: %s", hint2)
	}
}

func TestVerifyScopeDecay_IgnoresNonVerificationTools(t *testing.T) {
	s := newVerifyScopeDecayState()

	// Non-verification tools should be ignored
	s.recordVerification("edit_file", "some edit", 1)
	s.recordVerification("read_file", "some file", 3)
	s.recordVerification("grep", "pattern", 5)

	if len(s.verifications) != 0 {
		t.Errorf("expected 0 verifications from non-verification tools, got %d", len(s.verifications))
	}

	hint := s.maybeWarnScopeDecay()
	if hint != "" {
		t.Errorf("expected no warning with no verification calls")
	}
}

func TestVerifyScopeDecay_Reset(t *testing.T) {
	s := newVerifyScopeDecayState()

	s.recordVerification("run_command", "go test ./...", 1)
	s.recordVerification("run_command", "go test ./internal/...", 3)
	s.recordVerification("run_command", "go test -run TestFoo", 5)
	s.maybeWarnScopeDecay()

	s.reset()
	if s.warnings != 0 || len(s.verifications) != 0 {
		t.Errorf("reset did not clear state: warnings=%d, verifications=%d", s.warnings, len(s.verifications))
	}
}

func TestVerifyScopeDecay_PartialToMinimal(t *testing.T) {
	s := newVerifyScopeDecayState()

	// PARTIAL -> PARTIAL -> MINIMAL (monotonic but not from FULL)
	s.recordVerification("run_command", "go test ./internal/agent/...", 1)
	s.recordVerification("run_command", "go test ./internal/...", 3)
	s.recordVerification("run_command", "go test -run TestFoo", 5)

	hint := s.maybeWarnScopeDecay()
	if hint == "" {
		t.Fatal("expected warning for PARTIAL->PARTIAL->MINIMAL monotonic decrease")
	}
}
