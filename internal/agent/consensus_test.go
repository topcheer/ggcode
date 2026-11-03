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
	s.recordFiring("Error Rush", 1)
	s.recordFiring("Tunnel Vision", 1)

	if msg := s.check(); msg != "" {
		t.Errorf("expected empty with only 2 detectors, got: %s", msg)
	}
}

func TestConsensusState_AtThreshold(t *testing.T) {
	s := newConsensusState()
	s.recordFiring("Error Rush", 1)
	s.recordFiring("Tunnel Vision", 1)
	s.recordFiring("Analysis Paralysis", 1)
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
	s.recordFiring("Error Rush", 1)
	s.recordFiring("Tunnel Vision", 1)
	s.recordFiring("Analysis Paralysis", 1)

	if msg := s.check(); msg == "" {
		t.Fatal("expected first alert")
	}

	for i := 1; i <= consensusCooldownSteps; i++ {
		s.recordFiring("Error Rush", 10+i)
	}
	s.recordFiring("Tunnel Vision", 10+consensusCooldownSteps)
	s.recordFiring("Analysis Paralysis", 10+consensusCooldownSteps)

	if msg := s.check(); msg == "" {
		t.Fatal("expected second alert")
	}

	for i := 1; i <= consensusCooldownSteps; i++ {
		s.recordFiring("Error Rush", 30+i)
	}
	s.recordFiring("Tunnel Vision", 1)
	s.recordFiring("Analysis Paralysis", 1)

	if msg := s.check(); msg != "" {
		t.Errorf("expected no third alert (max %d), got: %s", consensusMaxAlerts, msg)
	}
}

func TestConsensusState_Cooldown(t *testing.T) {
	s := newConsensusState()
	s.recordFiring("Error Rush", 1)
	s.recordFiring("Tunnel Vision", 1)
	s.recordFiring("Analysis Paralysis", 1)

	if msg1 := s.check(); msg1 == "" {
		t.Fatal("expected first alert")
	}

	s.recordFiring("Error Rush", 1)
	s.recordFiring("Tunnel Vision", 1)
	s.recordFiring("Analysis Paralysis", 1)
	if msg2 := s.check(); msg2 != "" {
		t.Errorf("expected no alert during cooldown, got: %s", msg2)
	}
}

func TestConsensusState_Reset(t *testing.T) {
	s := newConsensusState()
	s.recordFiring("Error Rush", 1)
	s.recordFiring("Tunnel Vision", 1)
	s.recordFiring("Analysis Paralysis", 1)
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

	s.recordFiring("Error Rush", 1)
	s.recordFiring("Tunnel Vision", 1)
	s.recordFiring("Analysis Paralysis", 1)
	msg1 := s.check()
	if msg1 == "" {
		t.Fatal("expected first alert")
	}
	if strings.Contains(msg1, "RECURRING") {
		t.Errorf("first alert should not contain RECURRING: %s", msg1)
	}

	for i := 1; i <= consensusCooldownSteps; i++ {
		s.recordFiring("Error Rush", 10+i)
	}
	s.recordFiring("Tunnel Vision", 10+consensusCooldownSteps)
	s.recordFiring("Analysis Paralysis", 10+consensusCooldownSteps)
	msg2 := s.check()
	if msg2 == "" {
		t.Fatal("expected second alert")
	}
	if !strings.Contains(msg2, "RECURRING") {
		t.Errorf("second alert should contain RECURRING: %s", msg2)
	}
}

func TestConsensusState_DeduplicatesSameDetector(t *testing.T) {
	s := newConsensusState()

	for i := 0; i < 5; i++ {
		s.recordFiring("Error Rush", 1)
	}
	if msg := s.check(); msg != "" {
		t.Errorf("expected no alert with single distinct detector, got: %s", msg)
	}
}
