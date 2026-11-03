package agent

import (
	"testing"
)

func TestCorrectionSpiral_NoSpiral(t *testing.T) {
	s := newCorrectionSpiralState()

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

	s.errorSequence = []int{sevParse, sevCompile, sevCompile, sevRuntime}
	msg := s.maybeWarn(10)
	if msg == "" {
		t.Fatal("expected warning for escalation with plateau")
	}
}

func TestCorrectionSpiral_TooManyRegressions(t *testing.T) {
	s := newCorrectionSpiralState()

	s.errorSequence = []int{sevParse, sevRuntime, sevCompile, sevLint}
	msg := s.maybeWarn(10)
	if msg != "" {
		t.Errorf("expected no warning for regression-dominant sequence, got: %s", msg)
	}
}

func TestCorrectionSpiral_SecondWarning(t *testing.T) {
	s := newCorrectionSpiralState()
	s.errorSequence = []int{sevParse, sevCompile, sevRuntime, sevCrash}

	msg1 := s.maybeWarn(10)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	msg2 := s.maybeWarn(12)
	if msg2 != "" {
		t.Fatal("expected no warning too soon after first")
	}

	if msg3 := s.maybeWarn(16); msg3 != "" {
		t.Fatal("expected no re-warn on frozen sequence")
	}

	s.errorSequence = append(s.errorSequence, sevCrash)
	msg3 := s.maybeWarn(16)
	if msg3 == "" {
		t.Fatal("expected second warning after spacing with new evidence")
	}
	if s.warningCount != 2 {
		t.Errorf("expected warningCount=2, got %d", s.warningCount)
	}

	msg4 := s.maybeWarn(25)
	if msg4 != "" {
		t.Fatal("expected no third warning (capped at 2)")
	}
}

func TestCorrectionSpiral_RecordEditAndVerify(t *testing.T) {
	s := newCorrectionSpiralState()

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

	s.recordEdit(5)
	s.recordVerifyResult("run_command", "build successful", false, 6)

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
		{"some unknown error", sevCompile},
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

func TestCorrectionSpiral_SequenceCapped(t *testing.T) {
	s := newCorrectionSpiralState()

	for i := 0; i < 15; i++ {
		s.errorSequence = append(s.errorSequence, sevCompile)
	}

	msg := s.maybeWarn(20)
	if msg != "" {
		t.Error("expected no warning for flat severity sequence")
	}
}

func TestCorrectionSpiralGreenClearsErrorSequence(t *testing.T) {
	s := newCorrectionSpiralState()

	for _, sev := range []struct {
		iter int
		msg  string
	}{
		{1, "syntax error: unexpected token"},
		{3, "compile error: cannot find symbol"},
		{5, "panic: runtime error: nil pointer"},
	} {
		s.recordEdit(sev.iter)
		s.recordVerifyResult("run_command", sev.msg, true, sev.iter+1)
		s.recordVerifyResult("run_command", "ok all green", false, sev.iter+2)
	}
	if msg := s.maybeWarn(50); msg != "" {
		t.Fatalf("healthy progression assembled into a fake spiral: %s", msg)
	}
	if len(s.errorSequence) != 0 {
		t.Fatalf("green did not clear errorSequence: %v", s.errorSequence)
	}
}

func TestCorrectionSpiralNoNewEvidenceNoRewarn(t *testing.T) {
	s := newCorrectionSpiralState()
	s.errorSequence = []int{1, 3, 5}
	got := s.maybeWarn(10)
	if got == "" {
		t.Fatal("first warning must fire on 3-pair escalation")
	}

	if got := s.maybeWarn(20); got != "" {
		t.Fatalf("re-warn on frozen sequence must not fire: %.80s", got)
	}

	s.errorSequence = append(s.errorSequence, 6)
	if got := s.maybeWarn(20); got == "" {
		t.Fatal("warning must re-fire after new failure evidence")
	}
}

func TestCorrectionSpiralCapSnapshotNotDeadlocked(t *testing.T) {
	s := newCorrectionSpiralState()

	s.errorSequence = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if got := s.maybeWarn(5); got == "" {
		t.Fatal("first warning must fire at len==12")
	}

	if got := s.maybeWarn(20); got != "" {
		t.Fatal("frozen sequence must not re-warn")
	}

	s.pendingEdit = true
	s.recordVerifyResult("go test", "exit status 1", true, 0)
	if got := s.maybeWarn(20); got == "" {
		t.Fatal("second warning must fire - counter growth is new evidence (#1726 case 1)")
	}
}

func TestCorrectionSpiralGreenResetsSnapshot(t *testing.T) {
	s := newCorrectionSpiralState()
	s.errorSequence = []int{1, 2, 3, 4}
	if got := s.maybeWarn(5); got == "" {
		t.Fatal("first warning must fire")
	}
	s.pendingEdit = true
	s.recordVerifyResult("go test", "ok", false, 0)
	if s.lastWarnedSeqLen != 0 || s.lastWarnedTotal != 0 {
		t.Fatalf("green must reset both snapshots, got len=%d total=%d", s.lastWarnedSeqLen, s.lastWarnedTotal)
	}
}
