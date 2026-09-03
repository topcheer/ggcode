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
	// Only 2 detectors, threshold is 3
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

	// First alert
	if msg := s.check(); msg == "" {
		t.Fatal("expected first alert")
	}

	// Advance the tool-call step past the cooldown, fire again (#1446-C:
	// the step axis is caller-supplied - advancing it is the caller's job).
	for i := 1; i <= consensusCooldownSteps; i++ {
		s.recordFiring("Error Rush", 10+i)
	}
	s.recordFiring("Tunnel Vision", 10+consensusCooldownSteps)
	s.recordFiring("Analysis Paralysis", 10+consensusCooldownSteps)

	// Second alert (recurring)
	if msg := s.check(); msg == "" {
		t.Fatal("expected second alert")
	}

	// Reset step again for third attempt
	for i := 1; i <= consensusCooldownSteps; i++ {
		s.recordFiring("Error Rush", 30+i)
	}
	s.recordFiring("Tunnel Vision", 1)
	s.recordFiring("Analysis Paralysis", 1)

	// Third alert should be blocked (max 2)
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

	// Immediately fire again within cooldown window
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
	// First alert
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

	// Wait for cooldown (#1446-C: advance the caller-supplied step axis)
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
	// Same detector fires 5 times, but only counts as 1 distinct
	for i := 0; i < 5; i++ {
		s.recordFiring("Error Rush", 1)
	}
	if msg := s.check(); msg != "" {
		t.Errorf("expected no alert with single distinct detector, got: %s", msg)
	}
}

// scanAndCheck and its baseline-slicing tests (incl. the #147 raw-content
// hazard simulation) were removed with the function itself in the #952
// follow-up cleanup: firings are recorded explicitly via recordFiring at each
// detector call site, so there is no content scan left to test.
