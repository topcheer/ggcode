package agent

import (
	"testing"
)

func TestCorrectionSpiral_NoSpiral(t *testing.T) {
	s := newCorrectionSpiralState()
	// Alternating errors with decreasing severity - not a spiral
	s.errorSequence = []int{sevCompile, sevParse, sevLint}
	msg := s.maybeWarn(5)
	if msg != "" {
		t.Errorf("expected no warning for decreasing severity, got: %s", msg)
	}
}

func TestCorrectionSpiral_InsufficientData(t *testing.T) {
	s := newCorrectionSpiralState()
	s.errorSequence = []int{sevCompile, sevRuntime}
	msg := s.maybeWarn(5)
	if msg != "" {
		t.Errorf("expected no warning with <3 entries, got: %s", msg)
	}
}

func TestCorrectionSpiral_EscalationDetected(t *testing.T) {
	s := newCorrectionSpiralState()
	// Classic spiral: syntax → compile → runtime → crash
	s.errorSequence = []int{sevParse, sevCompile, sevRuntime, sevCrash}
	msg := s.maybeWarn(10)
	if msg == "" {
		t.Fatal("expected warning for escalating severity spiral")
	}
	if s.warningCount != 1 {
		t.Errorf("expected warningCount=1, got %d", s.warningCount)
	}
}

func TestCorrectionSpiral_PlateauAllowed(t *testing.T) {
	s := newCorrectionSpiralState()
	// Two escalations with one plateau - still a spiral
	s.errorSequence = []int{sevParse, sevCompile, sevCompile, sevRuntime}
	msg := s.maybeWarn(10)
	if msg == "" {
		t.Fatal("expected warning for escalation with plateau")
	}
}

func TestCorrectionSpiral_TooManyRegressions(t *testing.T) {
	s := newCorrectionSpiralState()
	// Escalation then regression - not a clean spiral
	s.errorSequence = []int{sevParse, sevRuntime, sevCompile, sevLint}
	msg := s.maybeWarn(10)
	if msg != "" {
		t.Errorf("expected no warning for regression-dominant sequence, got: %s", msg)
	}
}

func TestCorrectionSpiral_SecondWarning(t *testing.T) {
	s := newCorrectionSpiralState()
	s.errorSequence = []int{sevParse, sevCompile, sevRuntime, sevCrash}
	// First warning
	msg1 := s.maybeWarn(10)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}
	// Need to wait 5 iterations
	msg2 := s.maybeWarn(12)
	if msg2 != "" {
		t.Fatal("expected no warning too soon after first")
	}
	// Second warning after spacing
	msg3 := s.maybeWarn(16)
	if msg3 == "" {
		t.Fatal("expected second warning after spacing")
	}
	if s.warningCount != 2 {
		t.Errorf("expected warningCount=2, got %d", s.warningCount)
	}
	// Third should be capped
	msg4 := s.maybeWarn(25)
	if msg4 != "" {
		t.Fatal("expected no third warning (capped at 2)")
	}
}

func TestCorrectionSpiral_RecordEditAndVerify(t *testing.T) {
	s := newCorrectionSpiralState()
	// Simulate: edit → verify fail (syntax) → edit → verify fail (compile) → edit → verify fail (runtime)
	s.recordEdit(1)
	s.recordVerifyResult("run_command", "syntax error: unexpected token", true, 2)

	s.recordEdit(3)
	s.recordVerifyResult("run_command", "compile error: cannot find symbol", true, 4)

	s.recordEdit(5)
	s.recordVerifyResult("run_command", "panic: runtime error: nil pointer", true, 6)

	if len(s.errorSequence) != 3 {
		t.Fatalf("expected 3 error entries, got %d", len(s.errorSequence))
	}
	if s.errorSequence[0] != sevParse {
		t.Errorf("expected first severity=sevParse(%d), got %d", sevParse, s.errorSequence[0])
	}
	if s.errorSequence[1] != sevCompile {
		t.Errorf("expected second severity=sevCompile(%d), got %d", sevCompile, s.errorSequence[1])
	}
	if s.errorSequence[2] != sevRuntime {
		t.Errorf("expected third severity=sevRuntime(%d), got %d", sevRuntime, s.errorSequence[2])
	}
}

func TestCorrectionSpiral_GreenBuildResets(t *testing.T) {
	s := newCorrectionSpiralState()
	s.recordEdit(1)
	s.recordVerifyResult("run_command", "syntax error", true, 2)

	s.recordEdit(3)
	s.recordVerifyResult("run_command", "compile error", true, 4)

	// Green build - should reset pendingEdit
	s.recordEdit(5)
	s.recordVerifyResult("run_command", "build successful", false, 6)

	// This error should not be recorded (pendingEdit was reset by success)
	s.recordVerifyResult("run_command", "panic: nil pointer", true, 7)

	if s.pendingEdit {
		t.Error("expected pendingEdit=false after green build")
	}
}

func TestCorrectionSpiral_Reset(t *testing.T) {
	s := newCorrectionSpiralState()
	s.recordEdit(1)
	s.recordVerifyResult("run_command", "syntax error", true, 2)
	s.warningCount = 1
	s.lastWarnedAt = 5

	s.reset()

	if len(s.errorSequence) != 0 {
		t.Errorf("expected empty errorSequence after reset, got %d", len(s.errorSequence))
	}
	if s.totalCorrections != 0 {
		t.Errorf("expected totalCorrections=0 after reset, got %d", s.totalCorrections)
	}
	if s.warningCount != 0 {
		t.Errorf("expected warningCount=0 after reset, got %d", s.warningCount)
	}
	if s.pendingEdit {
		t.Error("expected pendingEdit=false after reset")
	}
}

func TestCSClassifySeverity(t *testing.T) {
	tests := []struct {
		output string
		want   int
	}{
		{"panic: runtime error: nil pointer dereference", sevRuntime},
		{"fatal error: signal: segmentation violation", sevCrash},
		{"--- FAIL: TestSomething", sevTest},
		{"syntax error: unexpected semicolon", sevParse},
		{"./main.go:10:2: undefined: foo", sevCompile},
		{"lint: unused variable", sevLint},
		{"some unknown error", sevCompile}, // default
	}

	for _, tt := range tests {
		got := csClassifySeverity(tt.output)
		if got != tt.want {
			t.Errorf("csClassifySeverity(%q) = %d, want %d", tt.output, got, tt.want)
		}
	}
}

func TestCSIsEditTool(t *testing.T) {
	if !csIsEditTool("edit_file") {
		t.Error("edit_file should be an edit tool")
	}
	if !csIsEditTool("write_file") {
		t.Error("write_file should be an edit tool")
	}
	if csIsEditTool("read_file") {
		t.Error("read_file should not be an edit tool")
	}
	if csIsEditTool("run_command") {
		t.Error("run_command should not be an edit tool")
	}
}

// TestCSIsVerifyTool was removed with csIsVerifyTool (#491): verify
// classification now happens on command CONTENT in the agent.go wiring
// (run_command + psIsVerifyCommand), never on tool name alone. See
// TestCorrectionSpiral_WiringGateShape in detector_shell_channel_test.go.

func TestCorrectionSpiral_SequenceCapped(t *testing.T) {
	s := newCorrectionSpiralState()
	// Record more than 12 entries
	for i := 0; i < 15; i++ {
		s.errorSequence = append(s.errorSequence, sevCompile)
	}
	// Should be capped by append logic, but we check maybeWarn still works
	// Even though all same severity (no escalation), should not warn
	msg := s.maybeWarn(20)
	if msg != "" {
		t.Error("expected no warning for flat severity sequence")
	}
}
