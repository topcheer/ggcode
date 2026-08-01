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

func TestConvergenceLock_FirstWarningAt3Edits(t *testing.T) {
	s := newConvergenceLockState()
	s.recordVerifyResult("go test ./...", false)

	s.recordEdit()
	s.recordEdit()
	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning at 2 edits, got: %s", msg)
	}

	s.recordEdit()
	msg := s.check()
	if msg == "" {
		t.Fatal("expected convergence warning at 3 edits")
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

	for i := 0; i < 5; i++ {
		s.recordEdit()
	}
	s.check() // consume first warning

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

	for i := 0; i < 5; i++ {
		s.recordEdit()
	}
	s.check()

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
