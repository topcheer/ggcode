package agent

import (
	"strings"
	"testing"
)

func TestConsensusState_NoFirings(t *testing.T) {
	s := newConsensusState()
	if msg := s.check(); msg != "" {
		t.Errorf("expected empty guidance with no firings, got: %s", msg)
	}
}

func TestConsensusState_BelowThreshold(t *testing.T) {
	s := newConsensusState()
	s.recordFiring("Error Rush")
	s.recordFiring("Tunnel Vision")
	// Only 2 detectors, threshold is 3
	if msg := s.check(); msg != "" {
		t.Errorf("expected empty with only 2 detectors, got: %s", msg)
	}
}

func TestConsensusState_AtThreshold(t *testing.T) {
	s := newConsensusState()
	s.recordFiring("Error Rush")
	s.recordFiring("Tunnel Vision")
	s.recordFiring("Analysis Paralysis")
	msg := s.check()
	if msg == "" {
		t.Fatal("expected consensus alert with 3 detectors, got empty")
	}
	if !strings.Contains(msg, "Systemic Failure Detected") {
		t.Errorf("expected 'Systemic Failure Detected' in message, got: %s", msg)
	}
	if !strings.Contains(msg, "Error Rush") {
		t.Errorf("expected detector names in message, got: %s", msg)
	}
}

func TestConsensusState_MaxAlerts(t *testing.T) {
	s := newConsensusState()
	s.recordFiring("Error Rush")
	s.recordFiring("Tunnel Vision")
	s.recordFiring("Analysis Paralysis")

	// First alert
	if msg := s.check(); msg == "" {
		t.Fatal("expected first alert")
	}

	// Reset step to bypass cooldown, fire again
	for i := 0; i < consensusCooldownSteps; i++ {
		s.recordFiring("Error Rush")
	}
	s.recordFiring("Tunnel Vision")
	s.recordFiring("Analysis Paralysis")

	// Second alert (recurring)
	if msg := s.check(); msg == "" {
		t.Fatal("expected second alert")
	}

	// Reset step again for third attempt
	for i := 0; i < consensusCooldownSteps; i++ {
		s.recordFiring("Error Rush")
	}
	s.recordFiring("Tunnel Vision")
	s.recordFiring("Analysis Paralysis")

	// Third alert should be blocked (max 2)
	if msg := s.check(); msg != "" {
		t.Errorf("expected no third alert (max %d), got: %s", consensusMaxAlerts, msg)
	}
}

func TestConsensusState_Cooldown(t *testing.T) {
	s := newConsensusState()
	s.recordFiring("Error Rush")
	s.recordFiring("Tunnel Vision")
	s.recordFiring("Analysis Paralysis")

	if msg1 := s.check(); msg1 == "" {
		t.Fatal("expected first alert")
	}

	// Immediately fire again within cooldown window
	s.recordFiring("Error Rush")
	s.recordFiring("Tunnel Vision")
	s.recordFiring("Analysis Paralysis")
	if msg2 := s.check(); msg2 != "" {
		t.Errorf("expected no alert during cooldown, got: %s", msg2)
	}
}

func TestConsensusState_Reset(t *testing.T) {
	s := newConsensusState()
	s.recordFiring("Error Rush")
	s.recordFiring("Tunnel Vision")
	s.recordFiring("Analysis Paralysis")
	s.check()

	s.reset()
	if len(s.firings) != 0 {
		t.Errorf("expected empty firings after reset, got %d", len(s.firings))
	}
	if s.alertsIssued != 0 {
		t.Errorf("expected 0 alertsIssued after reset, got %d", s.alertsIssued)
	}
}

func TestConsensusState_RepeatAlertContainsRecurring(t *testing.T) {
	s := newConsensusState()
	// First alert
	s.recordFiring("Error Rush")
	s.recordFiring("Tunnel Vision")
	s.recordFiring("Analysis Paralysis")
	msg1 := s.check()
	if msg1 == "" {
		t.Fatal("expected first alert")
	}
	if strings.Contains(msg1, "RECURRING") {
		t.Errorf("first alert should not contain RECURRING: %s", msg1)
	}

	// Wait for cooldown
	for i := 0; i < consensusCooldownSteps; i++ {
		s.recordFiring("Error Rush")
	}
	s.recordFiring("Tunnel Vision")
	s.recordFiring("Analysis Paralysis")
	msg2 := s.check()
	if msg2 == "" {
		t.Fatal("expected second alert")
	}
	if !strings.Contains(msg2, "RECURRING") {
		t.Errorf("second alert should contain RECURRING: %s", msg2)
	}
}

func TestConsensusState_ScanAndCheck(t *testing.T) {
	s := newConsensusState()
	// Simulate tool result content containing 3 detector tags
	content := "Build error occurred.\n\n" +
		"[Error Rush / Panic Coding] You are issuing edits after errors.\n\n" +
		"[Tunnel Vision] You've touched only 2 files in 15 iterations.\n\n" +
		"[Analysis Paralysis] 8 exploration calls detected."

	msg := s.scanAndCheck(content)
	if msg == "" {
		t.Fatal("expected consensus alert from scanAndCheck with 3 tags")
	}
	if !strings.Contains(msg, "Systemic Failure") {
		t.Errorf("expected systemic failure message, got: %s", msg)
	}
}

func TestConsensusState_ScanAndCheck_BelowThreshold(t *testing.T) {
	s := newConsensusState()
	content := "Build error.\n\n[Error Rush] Some warning.\n\n[Tunnel Vision] Another."
	msg := s.scanAndCheck(content)
	if msg != "" {
		t.Errorf("expected no alert with only 2 tags, got: %s", msg)
	}
}

func TestConsensusState_ScanAndCheck_NilSafe(t *testing.T) {
	var s *consensusState
	msg := s.scanAndCheck("[Error Rush] test")
	if msg != "" {
		t.Errorf("expected empty from nil state, got: %s", msg)
	}
}

func TestConsensusState_DeduplicatesSameDetector(t *testing.T) {
	s := newConsensusState()
	// Same detector fires 5 times, but only counts as 1 distinct
	for i := 0; i < 5; i++ {
		s.recordFiring("Error Rush")
	}
	if msg := s.check(); msg != "" {
		t.Errorf("expected no alert with single distinct detector, got: %s", msg)
	}
}
