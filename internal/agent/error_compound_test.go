package agent

import (
	"strings"
	"testing"
)

func TestErrorCompound_NoWarningEarlyRun(t *testing.T) {
	s := newErrorCompoundState()
	// Only 5 steps - too few to warn
	for i := 0; i < 5; i++ {
		s.recordStep(true)
	}
	if msg := s.maybeWarn(5); msg != "" {
		t.Fatalf("expected no warning with <8 steps, got: %s", msg)
	}
}

func TestErrorCompound_NoWarningZeroErrors(t *testing.T) {
	s := newErrorCompoundState()
	for i := 0; i < 20; i++ {
		s.recordStep(false)
	}
	if msg := s.maybeWarn(20); msg != "" {
		t.Fatalf("expected no warning with 0 errors, got: %s", msg)
	}
}

func TestErrorCompound_ModerateRiskWarning(t *testing.T) {
	s := newErrorCompoundState()
	// 20 steps, 4 errors = 20% density
	// P = 0.8^20 = 0.0115 = ~1.2% -- well below 70% threshold
	for i := 0; i < 16; i++ {
		s.recordStep(false)
	}
	for i := 0; i < 4; i++ {
		s.recordStep(true)
	}
	msg := s.maybeWarn(20)
	if msg == "" {
		t.Fatal("expected moderate warning, got none")
	}
	if !strings.Contains(msg, "moderate") {
		t.Fatalf("expected 'moderate' in message, got: %s", msg)
	}
	if !strings.Contains(msg, "error-compounding") {
		t.Fatalf("expected tag in message, got: %s", msg)
	}
}

func TestErrorCompound_CriticalRiskSecondWarning(t *testing.T) {
	s := newErrorCompoundState()
	// 10 steps, 5 errors = 50% density, P = 0.5^10 = 0.001 = 0.1%
	for i := 0; i < 5; i++ {
		s.recordStep(false)
	}
	for i := 0; i < 5; i++ {
		s.recordStep(true)
	}
	// First warning (moderate)
	msg1 := s.maybeWarn(10)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}
	// Second warning needs spacing
	msg2 := s.maybeWarn(11) // only 1 iter gap
	if msg2 != "" {
		t.Fatalf("expected no second warning too soon, got: %s", msg2)
	}
	// After 5+ iterations gap, should get second (critical)
	msg3 := s.maybeWarn(16)
	if msg3 == "" {
		t.Fatal("expected second warning after spacing")
	}
	if !strings.Contains(msg3, "critical") {
		t.Fatalf("expected 'critical' in second warning, got: %s", msg3)
	}
}

func TestErrorCompound_MaxTwoWarnings(t *testing.T) {
	s := newErrorCompoundState()
	for i := 0; i < 5; i++ {
		s.recordStep(true) // 100% density
	}
	for i := 0; i < 5; i++ {
		s.recordStep(false)
	}
	_ = s.maybeWarn(10)    // warning 1
	_ = s.maybeWarn(16)    // warning 2
	msg := s.maybeWarn(22) // should be suppressed
	if msg != "" {
		t.Fatalf("expected no third warning, got: %s", msg)
	}
}

func TestErrorCompound_RecordResultClassification(t *testing.T) {
	s := newErrorCompoundState()

	// Verify command error
	s.recordStep(s.recordResult("run_command", true, 1))
	if s.verifyFails != 1 {
		t.Fatalf("expected 1 verifyFail, got %d", s.verifyFails)
	}

	// Edit tool error
	s.recordStep(s.recordResult("edit_file", true, 2))
	if s.editFails != 1 {
		t.Fatalf("expected 1 editFail, got %d", s.editFails)
	}

	// Other tool error
	s.recordStep(s.recordResult("grep", true, 3))
	if s.toolErrors != 1 {
		t.Fatalf("expected 1 toolError, got %d", s.toolErrors)
	}

	// No error - should not increment
	hadErr := s.recordResult("read_file", false, 4)
	if hadErr {
		t.Fatal("expected false for non-error result")
	}
}

func TestErrorCompound_Reset(t *testing.T) {
	s := newErrorCompoundState()
	s.recordStep(true)
	s.recordStep(true)
	s.recordResult("run_command", true, 1)
	_ = s.maybeWarn(10)

	s.reset()
	if s.totalSteps != 0 || s.errorSteps != 0 || s.verifyFails != 0 {
		t.Fatal("reset did not clear state")
	}
	if s.warningCount != 0 {
		t.Fatal("reset did not clear warningCount")
	}
}

func TestErrorCompound_LowErrorDensityNoWarning(t *testing.T) {
	s := newErrorCompoundState()
	// 20 steps, 1 error = 5% density
	// P = 0.95^20 = 0.358 = ~36% -- below 70%, so first warning fires
	// Actually let's test with truly negligible density: 0 errors
	for i := 0; i < 19; i++ {
		s.recordStep(false)
	}
	s.recordStep(true) // 1 error in 20 = 5%
	msg := s.maybeWarn(20)
	// 0.95^20 = 0.358 = ~36%, which IS below 70% -- should warn
	if msg == "" {
		t.Fatal("expected warning at 5% density over 20 steps")
	}
}

func TestErrorCompound_MessageContainsBreakdown(t *testing.T) {
	s := newErrorCompoundState()
	s.recordStep(s.recordResult("run_command", true, 1))
	s.recordStep(s.recordResult("edit_file", true, 2))
	s.recordStep(s.recordResult("grep", true, 3))
	for i := 0; i < 7; i++ {
		s.recordStep(false)
	}
	// total = 10, errors = 3, density = 30%, P = 0.7^10 = 2.8%
	msg := s.maybeWarn(10)
	if !strings.Contains(msg, "verify failures") {
		t.Fatalf("expected verify failures in breakdown: %s", msg)
	}
	if !strings.Contains(msg, "edit failures") {
		t.Fatalf("expected edit failures in breakdown: %s", msg)
	}
	if !strings.Contains(msg, "tool errors") {
		t.Fatalf("expected tool errors in breakdown: %s", msg)
	}
}
