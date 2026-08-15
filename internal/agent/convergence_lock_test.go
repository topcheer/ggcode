package agent

import (
	"strings"
	"testing"
)

func TestConvergenceLock_NoGuidanceBeforeVerification(t *testing.T) {
	s := newConvergenceLockState()
	s.recordEdit() // edit before verify
	s.recordEdit()
	s.recordEdit()
	s.recordEdit()

	if msg := s.check(); msg != "" {
		t.Fatalf("expected no guidance before verification, got: %s", msg)
	}
}

func TestConvergenceLock_FirstWarningAfterAllowedEdits(t *testing.T) {
	s := newConvergenceLockState()
	s.recordVerifyResult("go test ./...", false)

	// Three minor fixes found during verification review are allowed —
	// this is the normal verify -> review -> fix loop, not drift.
	s.recordEdit()
	s.recordEdit()
	s.recordEdit()
	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning at 3 minor-fix edits, got: %s", msg)
	}

	// The 4th post-verification edit is drift.
	s.recordEdit()
	msg := s.check()
	if msg == "" {
		t.Fatal("expected convergence warning at 4th edit")
	}
	if !strings.Contains(msg, "Convergence check") {
		t.Errorf("expected 'Convergence check' in message, got: %s", msg)
	}
	if !strings.Contains(msg, "go test ./...") {
		t.Errorf("expected command in message, got: %s", msg)
	}

	// Should not fire again
	if msg2 := s.check(); msg2 != "" {
		t.Errorf("expected no duplicate warning, got: %s", msg2)
	}
}

func TestConvergenceLock_EscalationAt6Edits(t *testing.T) {
	s := newConvergenceLockState()
	s.recordVerifyResult("make test", false)

	for i := 0; i < 4; i++ {
		s.recordEdit()
	}
	s.check() // consume first warning (4th edit)

	s.recordEdit() // 5th — still below escalation
	if msg := s.check(); msg != "" {
		t.Fatalf("expected no escalation below 6 edits, got: %s", msg)
	}

	s.recordEdit() // 6th edit
	msg := s.check()
	if msg == "" {
		t.Fatal("expected escalation at 6 edits")
	}
	if !strings.Contains(msg, "Convergence alert") {
		t.Errorf("expected 'Convergence alert' escalation, got: %s", msg)
	}

	// Should not fire again
	if msg2 := s.check(); msg2 != "" {
		t.Errorf("expected no duplicate escalation, got: %s", msg2)
	}
}

func TestConvergenceLock_FailedVerifyResets(t *testing.T) {
	s := newConvergenceLockState()
	s.recordVerifyResult("go test", false)
	s.recordEdit()
	s.recordEdit()
	s.recordEdit()
	s.check() // first warning

	// Failed verification resets
	s.recordVerifyResult("go test", true)
	if s.verified {
		t.Error("expected verified=false after failed verification")
	}
	if s.postVerifyEdits != 0 {
		t.Errorf("expected postVerifyEdits=0 after reset, got %d", s.postVerifyEdits)
	}
	if s.warned {
		t.Error("expected warned=false after reset")
	}
}

func TestConvergenceLock_ReverifyResetsEdits(t *testing.T) {
	s := newConvergenceLockState()
	s.recordVerifyResult("go test", false)
	s.recordEdit()
	s.recordEdit()
	s.recordEdit()
	s.check() // first warning

	// Successful re-verification resets edit counters
	s.recordVerifyResult("go test", false)
	if s.postVerifyEdits != 0 {
		t.Errorf("expected postVerifyEdits=0 after re-verify, got %d", s.postVerifyEdits)
	}
	if s.warned {
		t.Error("expected warned=false after re-verify")
	}
}

func TestConvergenceLock_PostVerifyErrorsInEscalation(t *testing.T) {
	s := newConvergenceLockState()
	s.recordVerifyResult("make test", false)

	for i := 0; i < 4; i++ {
		s.recordEdit()
	}
	s.check() // consume first warning (4th edit)

	s.recordEdit()      // 5th — still below escalation
	s.recordEdit()      // 6th
	s.recordEditError() // with an error
	msg := s.check()
	if msg == "" {
		t.Fatal("expected escalation")
	}
	if !strings.Contains(msg, "post-verification edits resulted in errors") {
		t.Errorf("expected error count in message, got: %s", msg)
	}
}

func TestConvergenceLock_ResetClearsAll(t *testing.T) {
	s := newConvergenceLockState()
	s.recordVerifyResult("go test", false)
	s.recordEdit()
	s.recordEdit()
	s.recordEdit()
	s.recordEdit()
	s.check()
	s.check()

	s.reset()
	if s.verified || s.postVerifyEdits != 0 || s.warned || s.escalated {
		t.Error("reset did not clear all state")
	}
}

func TestTruncateCmd(t *testing.T) {
	short := "go test ./..."
	if got := truncateCmd(short); got != short {
		t.Errorf("expected %q, got %q", short, got)
	}
	long := strings.Repeat("a", 100)
	got := truncateCmd(long)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected truncated string ending with ..., got %q", got)
	}
}

// convergenceGate mirrors the agent-level convergenceRecordVerify filtering:
// only commands that pass isConvergenceVerifyCommand reach the state.
func convergenceGate(s *convergenceLockState, cmd string, isErr bool) {
	if isConvergenceVerifyCommand(cmd) {
		s.recordVerifyResult(cmd, isErr)
	}
}

func TestConvergenceLock_MakeCleanDoesNotArmLock(t *testing.T) {
	s := newConvergenceLockState()
	// Non-verification make targets must NOT arm the convergence lock.
	for _, cmd := range []string{
		"make clean", "make fmt", "make help", "make tidy",
		"make fmt-check", "make clean-all", "make tidy-check",
	} {
		if isConvergenceVerifyCommand(cmd) {
			t.Errorf("%q should NOT count as convergence verification", cmd)
		}
		convergenceGate(s, cmd, false)
	}
	if s.verified {
		t.Fatal("make clean/fmt/help/tidy must not arm the convergence lock")
	}
	// Edits after housekeeping make commands are normal work, not drift.
	for i := 0; i < 6; i++ {
		s.recordEdit()
	}
	if msg := s.check(); msg != "" {
		t.Fatalf("lock was never armed: no warning expected, got: %s", msg)
	}
}

func TestConvergenceLock_VerifyMakeTargetsArmLock(t *testing.T) {
	for _, cmd := range []string{
		"make test", "make verify-ci", "make lint", "make build",
		"make check", "make test-unit", "make ci", "just test", "task build",
		"go test ./...", "go build ./...", "npm test",
	} {
		if !isConvergenceVerifyCommand(cmd) {
			t.Errorf("%q SHOULD count as convergence verification", cmd)
		}
	}
}

func TestConvergenceLock_MakeVerifyCIVsClean(t *testing.T) {
	// make verify-ci arms the lock; make clean alone must never arm it
	// (issue #345 regression).
	s := newConvergenceLockState()
	convergenceGate(s, "make clean", false)
	if s.verified {
		t.Fatal("make clean must not arm the convergence lock")
	}
	convergenceGate(s, "make verify-ci", false)
	if !s.verified {
		t.Fatal("make verify-ci should arm the convergence lock")
	}
	// The normal minor-fix loop (3 edits) stays quiet.
	for i := 0; i < 3; i++ {
		s.recordEdit()
	}
	if msg := s.check(); msg != "" {
		t.Fatalf("3 minor fixes after verify-ci should not warn, got: %s", msg)
	}
	s.recordEdit()
	if msg := s.check(); msg == "" {
		t.Fatal("4th post-verification edit should warn")
	}
}
