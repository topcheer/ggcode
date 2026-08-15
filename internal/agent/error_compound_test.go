package agent

import (
	"strings"
	"testing"
)

func TestErrorCompound_NoWarningEarlyRun(t *testing.T) {
	s := newErrorCompoundState()
	// Only 5 steps - too few to warn (and density 100% is above the
	// catastrophic short-circuit, but < ecCatastrophicMinSteps is 3 so it
	// would fire... use non-catastrophic mix: 3 errors in 5 = 60% < 75%)
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
	// Issue #336 scenario 4: 3 steps, 3 errors (75% density) must fire.
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
	// 20 steps, 5 errors in window = ~25%... use 4 errors in last 12 window
	// steps to stay representative: 10 clean + 4 errors => window = 12
	// steps, 4 errors = 33% > 30% threshold.
	for i := 0; i < 10; i++ {
		s.recordStep(false)
	}
	// push the first 8 clean steps out of the window by recording 8 more
	// clean after the errors? Simpler: 8 clean + 4 errors = 12 steps total.
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
	_ = s // keep linters quiet about unused first state
}

func TestErrorCompound_Issue336_SingleErrorIn8StepsNoWarn(t *testing.T) {
	// Issue #336 scenario 3: 8 steps with 1 error = 12.5% window density,
	// a normal transient error level. Must NOT trigger under the new model.
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
	// Issue #336 scenario 1: 8 steps with 2 errors = 25% window density.
	// Under the old model this fired (P = 0.75^8 = 10% < 70%). Under the
	// new window-density model 25% <= 30% threshold, so no warning.
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
	// Issue #336 scenario 2: 13 steps total, errors early, last 5 steps all
	// clean. Old model escalated severity during recovery; new model must
	// never emit a critical warning when the window has no NEW errors.
	s := newErrorCompoundState()
	// steps 1-4: errors (4 errors)
	for i := 0; i < 4; i++ {
		s.recordStep(true)
	}
	// steps 5-13: clean recovery (9 clean steps; window of 12 holds 4 stale
	// errors + 8 clean = 33% density, but zero new errors since warn).
	for i := 0; i < 9; i++ {
		s.recordStep(false)
	}
	// First warning at this point: window = 12 steps (last clean pushes out
	// one error) -> 3/12 = 25% <= 30%, so likely no warning at all. Force a
	// moderate warning earlier, then check recovery behavior.
	s2 := newErrorCompoundState()
	for i := 0; i < 6; i++ {
		s2.recordStep(false)
	}
	for i := 0; i < 5; i++ {
		s2.recordStep(true) // 5 errors in 11 steps; window 5/11 = 45% > 30%
	}
	if msg := s2.maybeWarn(11); msg == "" || !strings.Contains(msg, "moderate") {
		t.Fatalf("expected moderate warning, got: %q", msg)
	}
	// Pure recovery: 10 more clean steps. Window slides errors out entirely.
	for i := 0; i < 10; i++ {
		s2.recordStep(false)
	}
	msg := s2.maybeWarn(21)
	if msg != "" {
		t.Fatalf("recovery period must not escalate; got: %s", msg)
	}

	// Also verify the 13-step shape directly: warn-then-recover produces no
	// second (critical) warning.
	s3 := newErrorCompoundState()
	for i := 0; i < 5; i++ {
		s3.recordStep(true) // 5 errors
	}
	_ = s3.maybeWarn(5) // moderate fires via catastrophic short-circuit
	for i := 0; i < 8; i++ {
		s3.recordStep(false) // recovery, zero new errors
	}
	if msg := s3.maybeWarn(13); msg != "" {
		t.Fatalf("13-step run with last steps clean must not warn; got: %s", msg)
	}
}

func TestErrorCompound_CriticalRequiresNewErrors(t *testing.T) {
	// Critical escalation is allowed only when new errors arrived since the
	// moderate warning.
	s := newErrorCompoundState()
	for i := 0; i < 4; i++ {
		s.recordStep(false)
	}
	for i := 0; i < 6; i++ {
		s.recordStep(true) // 6/10 = 60% density
	}
	msg1 := s.maybeWarn(10)
	if msg1 == "" || !strings.Contains(msg1, "moderate") {
		t.Fatalf("expected first moderate warning, got: %q", msg1)
	}
	// No spacing yet
	if msg := s.maybeWarn(11); msg != "" {
		t.Fatalf("expected spacing suppression, got: %s", msg)
	}
	// New errors keep arriving: escalation is justified
	for i := 0; i < 3; i++ {
		s.recordStep(true) // window: 9 errors / 12... still high density
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
		s.recordStep(true) // 100% density
	}
	for i := 0; i < 5; i++ {
		s.recordStep(false)
	}
	_ = s.maybeWarn(10) // warning 1
	for i := 0; i < 3; i++ {
		s.recordStep(true) // new errors so escalation is permitted
	}
	_ = s.maybeWarn(16) // warning 2
	for i := 0; i < 3; i++ {
		s.recordStep(true)
	}
	msg := s.maybeWarn(22) // should be suppressed (cap)
	if msg != "" {
		t.Fatalf("expected no third warning, got: %s", msg)
	}
}

func TestErrorCompound_WindowSlidesOutAndSubsides(t *testing.T) {
	// After 12 clean steps, window density is 0 and warnings subside.
	s := newErrorCompoundState()
	for i := 0; i < 6; i++ {
		s.recordStep(true)
	}
	// no warning yet (<8 steps but >=75% density fires) — consume it
	_ = s.maybeWarn(6)
	// reset warningCount path: just check that after full clean window no warn
	for i := 0; i < 12; i++ {
		s.recordStep(false)
	}
	if msg := s.maybeWarn(18); msg != "" {
		t.Fatalf("expected warnings to subside after clean window, got: %s", msg)
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

	// Window breakdown mirrors the recorded steps
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
	// 20 steps, 1 error = 5% density: normal level, must not warn (#336).
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
	// window = 10 steps, 3 errors = 30%... need strictly > 30%, add one more
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
	// Breakdown must report WINDOW counts, not lifetime totals.
	if !strings.Contains(msg, "2 tool errors") {
		t.Fatalf("expected window-counted '2 tool errors', got: %s", msg)
	}
}
