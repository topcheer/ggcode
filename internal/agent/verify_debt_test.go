package agent

import (
	"strings"
	"testing"
)

func TestVerifyDebt_NoWarningInitially(t *testing.T) {
	s := newVerifyDebtState()
	if msg := s.maybeWarn(1); msg != "" {
		t.Fatalf("expected no warning initially, got: %q", msg)
	}
}

func TestVerifyDebt_NoWarningBeforeThreshold(t *testing.T) {
	s := newVerifyDebtState()
	for i := 0; i < verifyDebtWarn1-1; i++ {
		s.recordSourceEdit()
	}
	if msg := s.maybeWarn(1); msg != "" {
		t.Fatalf("expected no warning below threshold, got: %q", msg)
	}
}

func TestVerifyDebt_WarnsAtThreshold(t *testing.T) {
	s := newVerifyDebtState()
	for i := 0; i < verifyDebtWarn1; i++ {
		s.recordSourceEdit()
	}
	msg := s.maybeWarn(1)
	if msg == "" {
		t.Fatal("expected warning at threshold")
	}
	if !strings.Contains(msg, "Verification debt") {
		t.Errorf("warning should mention 'Verification debt', got: %q", msg)
	}
	if !strings.Contains(msg, "build") {
		t.Errorf("warning should mention 'build', got: %q", msg)
	}
}

func TestVerifyDebt_EscalatesAtHighThreshold(t *testing.T) {
	s := newVerifyDebtState()
	for i := 0; i < verifyDebtWarn2; i++ {
		s.recordSourceEdit()
	}
	msg := s.maybeWarn(1)
	if msg == "" {
		t.Fatal("expected high-risk warning")
	}
	if !strings.Contains(msg, "probability") {
		t.Errorf("high-risk warning should include probability, got: %q", msg)
	}
}

func TestVerifyDebt_GreenBuildClearsDebt(t *testing.T) {
	s := newVerifyDebtState()
	for i := 0; i < verifyDebtWarn1+3; i++ {
		s.recordSourceEdit()
	}
	s.recordVerifyCommand("go build ./...", false)
	if s.editsSinceGreen != 0 {
		t.Errorf("expected debt cleared after green build, got %d", s.editsSinceGreen)
	}
	if msg := s.maybeWarn(1); msg != "" {
		t.Errorf("expected no warning after green build, got: %q", msg)
	}
}

func TestVerifyDebt_FailedBuildDoesNotClearDebt(t *testing.T) {
	s := newVerifyDebtState()
	for i := 0; i < verifyDebtWarn1; i++ {
		s.recordSourceEdit()
	}
	// A failed build verifies nothing -- debt must persist.
	s.recordVerifyCommand("go build ./...", true)
	if s.editsSinceGreen != verifyDebtWarn1 {
		t.Errorf("expected debt unchanged after failed build, got %d", s.editsSinceGreen)
	}
}

func TestVerifyDebt_MaxWarnings(t *testing.T) {
	s := newVerifyDebtState()
	for i := 0; i < verifyDebtWarn1; i++ {
		s.recordSourceEdit()
	}

	// First moderate warning at debt=7
	if msg := s.maybeWarn(1); msg == "" {
		t.Fatal("expected moderate warning at debt=7")
	}
	// Repeated call at same debt level should NOT warn again
	if msg := s.maybeWarn(1); msg != "" {
		t.Fatalf("expected no repeat warning at debt=7, got: %q", msg)
	}
	// Escalation warning suppressed (1 per run, batch 2 guidance-noise
	// cleanup; debt escalation remains visible in state, not as a second
	// guidance injection).
	for i := 0; i < verifyDebtWarn2-verifyDebtWarn1; i++ {
		s.recordSourceEdit()
	}
	if msg := s.maybeWarn(2); msg != "" {
		t.Fatalf("expected high-risk escalation to be suppressed (1 per run), got: %q", msg)
	}
	// Repeated call should also not warn
	if msg := s.maybeWarn(2); msg != "" {
		t.Fatalf("expected no repeat warning at debt=12, got: %q", msg)
	}
}

func TestVerifyDebt_Reset(t *testing.T) {
	s := newVerifyDebtState()
	for i := 0; i < verifyDebtWarn2; i++ {
		s.recordSourceEdit()
	}
	s.warningsIssued = 2

	s.reset()
	if s.editsSinceGreen != 0 || s.warningsIssued != 0 || s.totalEdits != 0 {
		t.Errorf("reset did not clear state: %+v", s)
	}
}

func TestCompoundSuccessProb(t *testing.T) {
	// 0.95^20 should be approximately 0.358
	got := compoundSuccessProb(0.95, 20)
	if got < 0.35 || got > 0.37 {
		t.Errorf("expected ~0.358, got %.4f", got)
	}
	// Edge case
	if compoundSuccessProb(1.0, 100) != 1.0 {
		t.Error("1.0^anything should be 1.0")
	}
	if compoundSuccessProb(0.5, 1) != 0.5 {
		t.Error("0.5^1 should be 0.5")
	}
}

func TestVerifyDebt_TracksTotalGreenBuilds(t *testing.T) {
	s := newVerifyDebtState()
	s.recordVerifyCommand("go build", false)
	s.recordVerifyCommand("go test", false)
	s.recordVerifyCommand("go build", true) // failed, not counted

	if s.totalGreenBuilds != 2 {
		t.Errorf("expected 2 green builds, got %d", s.totalGreenBuilds)
	}
}
