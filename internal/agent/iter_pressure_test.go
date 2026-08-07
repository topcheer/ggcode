package agent

import (
	"strings"
	"testing"
)

func TestIterPressure_NoWarningEarly(t *testing.T) {
	a := &Agent{iterPressure: newIterPressureState(20)}
	// Simulate early iterations with edits and verifies
	for i := 1; i <= 10; i++ {
		a.iterPressure.recordToolCall("edit_file", i)
		a.iterPressure.recordToolCall("run_command", i)
	}
	msg := a.maybeWarnIterPressure(10)
	if msg != "" {
		t.Errorf("expected no warning before pressure threshold, got: %s", msg)
	}
}

func TestIterPressure_WarnsOnDegradation(t *testing.T) {
	a := &Agent{iterPressure: newIterPressureState(20)}
	// Early: 4 edits, 4 verifies (ratio = 1.0)
	for i := 1; i <= 4; i++ {
		a.iterPressure.recordToolCall("edit_file", i)
		a.iterPressure.recordToolCall("run_command", i)
	}
	// Late (after 75% = iter 15): 3 edits, 0 verifies (ratio = 0.0)
	for i := 16; i <= 18; i++ {
		a.iterPressure.recordToolCall("edit_file", i)
	}
	msg := a.maybeWarnIterPressure(17)
	if msg == "" {
		t.Error("expected warning for verification degradation, got empty")
	}
	if !strings.Contains(msg, "Iteration Pressure") {
		t.Errorf("warning should contain 'Iteration Pressure', got: %s", msg)
	}
}

func TestIterPressure_NoWarnIfStillVerifying(t *testing.T) {
	a := &Agent{iterPressure: newIterPressureState(20)}
	// Early: 4 edits, 4 verifies
	for i := 1; i <= 4; i++ {
		a.iterPressure.recordToolCall("edit_file", i)
		a.iterPressure.recordToolCall("run_command", i)
	}
	// Late: 2 edits, 2 verifies (ratio stays high)
	a.iterPressure.recordToolCall("edit_file", 17)
	a.iterPressure.recordToolCall("run_command", 17)
	a.iterPressure.recordToolCall("edit_file", 18)
	a.iterPressure.recordToolCall("run_command", 18)
	msg := a.maybeWarnIterPressure(18)
	if msg != "" {
		t.Errorf("expected no warning when verification continues, got: %s", msg)
	}
}

func TestIterPressure_NoWarnInsufficientEarlyData(t *testing.T) {
	a := &Agent{iterPressure: newIterPressureState(20)}
	// Only 1 edit in early phase
	a.iterPressure.recordToolCall("edit_file", 1)
	a.iterPressure.recordToolCall("edit_file", 17)
	msg := a.maybeWarnIterPressure(17)
	if msg != "" {
		t.Errorf("expected no warning with insufficient early data, got: %s", msg)
	}
}

func TestIterPressure_FiresOnlyOnce(t *testing.T) {
	a := &Agent{iterPressure: newIterPressureState(20)}
	for i := 1; i <= 4; i++ {
		a.iterPressure.recordToolCall("edit_file", i)
		a.iterPressure.recordToolCall("run_command", i)
	}
	a.iterPressure.recordToolCall("edit_file", 17)
	msg1 := a.maybeWarnIterPressure(17)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}
	msg2 := a.maybeWarnIterPressure(18)
	if msg2 != "" {
		t.Error("expected no second warning (fires once)")
	}
}

func TestIterPressure_Reset(t *testing.T) {
	s := newIterPressureState(20)
	s.recordToolCall("edit_file", 1)
	s.recordToolCall("run_command", 1)
	s.warningsFired = 1
	s.reset(30)
	if s.earlyEdits != 0 || s.warningsFired != 0 || s.maxIter != 30 {
		t.Error("reset did not clear state properly")
	}
}

func TestIterPressure_IgnoresOtherTools(t *testing.T) {
	s := newIterPressureState(20)
	s.recordToolCall("read_file", 5)
	s.recordToolCall("grep", 5)
	if s.earlyEdits != 0 || s.earlyVerifies != 0 {
		t.Error("read/grep should not count as edit or verify")
	}
}

func TestClassifyToolForPressure(t *testing.T) {
	tests := []struct {
		tool string
		want string
	}{
		{"edit_file", "edit"},
		{"write_file", "edit"},
		{"multi_edit_file", "edit"},
		{"run_command", "verify"},
		{"lsp_diagnostics", "verify"},
		{"code_health", "verify"},
		{"read_file", "other"},
		{"grep", "other"},
	}
	for _, tt := range tests {
		got := classifyToolForPressure(tt.tool)
		if got != tt.want {
			t.Errorf("classifyToolForPressure(%q) = %q, want %q", tt.tool, got, tt.want)
		}
	}
}
