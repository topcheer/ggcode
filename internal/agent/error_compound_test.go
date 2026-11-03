package agent

import (
	"strings"
	"testing"
)

func TestErrorCompound_NoWarningEarlyRun(t *testing.T) {
	s := newErrorCompoundState()

	s.recordStep(true)
	s.recordStep(true)
	s.recordStep(true)
	s.recordStep(false)
	s.recordStep(false)
	if msg := s.maybeWarn(5); msg != "" {
		t.Fatalf("expected no warning with <8 steps and sub-catastrophic density, got: %s", msg)
	}
}

func TestErrorCompound_CatastrophicShortRunStillFires(t *testing.T) {

	s := newErrorCompoundState()
	for i := 0; i < 3; i++ {
		s.recordStep(true)
	}
	msg := s.maybeWarn(3)
	if msg == "" {
		t.Fatal("expected warning at 75% density with 3 steps")
	}
	if !strings.Contains(msg, "error-compounding") {
		t.Fatalf("expected tag in message, got: %s", msg)
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

	for i := 0; i < 10; i++ {
		s.recordStep(false)
	}

	s2 := newErrorCompoundState()
	for i := 0; i < 8; i++ {
		s2.recordStep(false)
	}
	for i := 0; i < 4; i++ {
		s2.recordStep(true)
	}
	msg := s2.maybeWarn(12)
	if msg == "" {
		t.Fatal("expected moderate warning (4/12 window density 33%), got none")
	}
	if !strings.Contains(msg, "moderate") {
		t.Fatalf("expected 'moderate' in message, got: %s", msg)
	}
	if !strings.Contains(msg, "error-compounding") {
		t.Fatalf("expected tag in message, got: %s", msg)
	}
	_ = s
}

func TestErrorCompound_Issue336_SingleErrorIn8StepsNoWarn(t *testing.T) {

	s := newErrorCompoundState()
	for i := 0; i < 7; i++ {
		s.recordStep(false)
	}
	s.recordStep(true)
	if msg := s.maybeWarn(8); msg != "" {
		t.Fatalf("expected no warning at 12.5%% density, got: %s", msg)
	}
}

func TestErrorCompound_Issue336_EightStepsTwoErrors(t *testing.T) {

	s := newErrorCompoundState()
	for i := 0; i < 6; i++ {
		s.recordStep(false)
	}
	s.recordStep(true)
	s.recordStep(true)
	if msg := s.maybeWarn(8); msg != "" {
		t.Fatalf("expected no warning at 25%% density (below 30%% threshold), got: %s", msg)
	}
}

func TestErrorCompound_Issue336_RecoveryMustNotEscalate(t *testing.T) {

	s := newErrorCompoundState()

	for i := 0; i < 4; i++ {
		s.recordStep(true)
	}

	for i := 0; i < 9; i++ {
		s.recordStep(false)
	}

	s2 := newErrorCompoundState()
	for i := 0; i < 6; i++ {
		s2.recordStep(false)
	}
	for i := 0; i < 5; i++ {
		s2.recordStep(true)
	}
	if msg := s2.maybeWarn(11); msg == "" || !strings.Contains(msg, "moderate") {
		t.Fatalf("expected moderate warning, got: %q", msg)
	}

	for i := 0; i < 10; i++ {
		s2.recordStep(false)
	}
	msg := s2.maybeWarn(21)
	if msg != "" {
		t.Fatalf("recovery period must not escalate; got: %s", msg)
	}

	s3 := newErrorCompoundState()
	for i := 0; i < 5; i++ {
		s3.recordStep(true)
	}
	_ = s3.maybeWarn(5)
	for i := 0; i < 8; i++ {
		s3.recordStep(false)
	}
	if msg := s3.maybeWarn(13); msg != "" {
		t.Fatalf("13-step run with last steps clean must not warn; got: %s", msg)
	}
}

func TestErrorCompound_CriticalRequiresNewErrors(t *testing.T) {

	s := newErrorCompoundState()
	for i := 0; i < 4; i++ {
		s.recordStep(false)
	}
	for i := 0; i < 6; i++ {
		s.recordStep(true)
	}
	msg1 := s.maybeWarn(10)
	if msg1 == "" || !strings.Contains(msg1, "moderate") {
		t.Fatalf("expected first moderate warning, got: %q", msg1)
	}

	if msg := s.maybeWarn(11); msg != "" {
		t.Fatalf("expected spacing suppression, got: %s", msg)
	}

	for i := 0; i < 3; i++ {
		s.recordStep(true)
	}
	msg2 := s.maybeWarn(16)
	if msg2 == "" {
		t.Fatal("expected second warning when errors continue")
	}
	if !strings.Contains(msg2, "critical") {
		t.Fatalf("expected 'critical' in second warning, got: %s", msg2)
	}
}

func TestErrorCompound_MaxTwoWarnings(t *testing.T) {
	s := newErrorCompoundState()
	for i := 0; i < 5; i++ {
		s.recordStep(true)
	}
	for i := 0; i < 5; i++ {
		s.recordStep(false)
	}
	_ = s.maybeWarn(10)
	for i := 0; i < 3; i++ {
		s.recordStep(true)
	}
	_ = s.maybeWarn(16)
	for i := 0; i < 3; i++ {
		s.recordStep(true)
	}
	msg := s.maybeWarn(22)
	if msg != "" {
		t.Fatalf("expected no third warning, got: %s", msg)
	}
}

func TestErrorCompound_WindowSlidesOutAndSubsides(t *testing.T) {

	s := newErrorCompoundState()
	for i := 0; i < 6; i++ {
		s.recordStep(true)
	}

	_ = s.maybeWarn(6)

	for i := 0; i < 12; i++ {
		s.recordStep(false)
	}
	if msg := s.maybeWarn(18); msg != "" {
		t.Fatalf("expected warnings to subside after clean window, got: %s", msg)
	}
}

func TestErrorCompound_RecordResultClassification(t *testing.T) {
	s := newErrorCompoundState()

	s.recordStep(s.recordResult("run_command", true, 1))
	if s.verifyFails != 1 {
		t.Fatalf("expected 1 verifyFail, got %d", s.verifyFails)
	}

	s.recordStep(s.recordResult("edit_file", true, 2))
	if s.editFails != 1 {
		t.Fatalf("expected 1 editFail, got %d", s.editFails)
	}

	s.recordStep(s.recordResult("grep", true, 3))
	if s.toolErrors != 1 {
		t.Fatalf("expected 1 toolError, got %d", s.toolErrors)
	}

	hadErr := s.recordResult("read_file", false, 4)
	if hadErr {
		t.Fatal("expected false for non-error result")
	}

	wSteps, wErrs, wVerify, wEdit, wTool := s.windowStats()
	if wSteps != 3 || wErrs != 3 || wVerify != 1 || wEdit != 1 || wTool != 1 {
		t.Fatalf("window stats mismatch: steps=%d errs=%d v=%d e=%d t=%d",
			wSteps, wErrs, wVerify, wEdit, wTool)
	}
}

func TestErrorCompound_Reset(t *testing.T) {
	s := newErrorCompoundState()
	s.recordStep(true)
	s.recordStep(true)
	s.recordResult("run_command", true, 1)
	_ = s.maybeWarn(10)

	s.reset()
	if s.totalSteps != 0 || s.verifyFails != 0 {
		t.Fatal("reset did not clear state")
	}
	if s.warningCount != 0 || s.newErrSinceWarn != 0 {
		t.Fatal("reset did not clear warning counters")
	}
	if len(s.window) != 0 {
		t.Fatal("reset did not clear window")
	}
}

func TestErrorCompound_LowErrorDensityNoWarning(t *testing.T) {

	s := newErrorCompoundState()
	for i := 0; i < 19; i++ {
		s.recordStep(false)
	}
	s.recordStep(true)
	if msg := s.maybeWarn(20); msg != "" {
		t.Fatalf("expected no warning at 5%% density, got: %s", msg)
	}
}

func TestErrorCompound_MessageContainsWindowBreakdown(t *testing.T) {
	s := newErrorCompoundState()
	s.recordStep(s.recordResult("run_command", true, 1))
	s.recordStep(s.recordResult("edit_file", true, 2))
	s.recordStep(s.recordResult("grep", true, 3))
	for i := 0; i < 7; i++ {
		s.recordStep(false)
	}

	s.recordStep(s.recordResult("grep", true, 11))
	msg := s.maybeWarn(11)
	if msg == "" {
		t.Fatal("expected warning at 4/11 = 36% window density")
	}
	if !strings.Contains(msg, "verify failures") {
		t.Fatalf("expected verify failures in breakdown: %s", msg)
	}
	if !strings.Contains(msg, "edit failures") {
		t.Fatalf("expected edit failures in breakdown: %s", msg)
	}
	if !strings.Contains(msg, "tool errors") {
		t.Fatalf("expected tool errors in breakdown: %s", msg)
	}

	if !strings.Contains(msg, "2 tool errors") {
		t.Fatalf("expected window-counted '2 tool errors', got: %s", msg)
	}
}
