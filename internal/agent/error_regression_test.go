package agent

import (
	"strings"
	"testing"
)

func TestErrRegressionState_Reset(t *testing.T) {
	s := newErrRegressionState()
	s.lastErrorCount = 5
	s.hadEdits = true
	s.warningCount = 2
	s.reset()
	if s.lastErrorCount != -1 || s.hadEdits || s.warningCount != 0 {
		t.Fatalf("reset did not clear state: %+v", s)
	}
}

func TestErrRegressionState_NoPreviousBaseline(t *testing.T) {
	s := newErrRegressionState()
	// First verification ever -- no baseline to compare against.
	got := s.recordVerify("error: foo", true)
	if got != "" {
		t.Fatalf("expected no guidance on first verify, got: %s", got)
	}
}

func TestErrRegressionState_NoEditsBetweenVerifies(t *testing.T) {
	s := newErrRegressionState()
	// First verify sets baseline.
	s.recordVerify(buildOutput(5), true)
	// Second verify with more errors but no edits in between.
	got := s.recordVerify(buildOutput(10), true)
	if got != "" {
		t.Fatalf("expected no guidance without edits, got: %s", got)
	}
}

func TestErrRegressionState_RegressionDetected(t *testing.T) {
	s := newErrRegressionState()
	s.recordVerify(buildOutput(2), true)
	s.recordEdit()
	got := s.recordVerify(buildOutput(8), true) // +6 > threshold of 3
	if got == "" {
		t.Fatal("expected regression guidance, got empty string")
	}
	if !strings.Contains(got, "NEGATIVE PROGRESS") {
		t.Fatalf("expected NEGATIVE PROGRESS in guidance, got: %s", got)
	}
	if !strings.Contains(got, "2 to 8") {
		t.Fatalf("expected error counts in message, got: %s", got)
	}
}

func TestErrRegressionState_SmallIncreaseNoWarning(t *testing.T) {
	s := newErrRegressionState()
	s.recordVerify(buildOutput(3), true)
	s.recordEdit()
	got := s.recordVerify(buildOutput(5), true) // +2 < threshold of 3
	if got != "" {
		t.Fatalf("expected no guidance for small increase, got: %s", got)
	}
}

func TestErrRegressionState_ErrorDecreasesNoWarning(t *testing.T) {
	s := newErrRegressionState()
	s.recordVerify(buildOutput(10), true)
	s.recordEdit()
	got := s.recordVerify(buildOutput(3), true) // -7, improvement
	if got != "" {
		t.Fatalf("expected no guidance when errors decrease, got: %s", got)
	}
}

func TestErrRegressionState_SuccessNoWarning(t *testing.T) {
	s := newErrRegressionState()
	s.recordVerify(buildOutput(5), true)
	s.recordEdit()
	got := s.recordVerify("", false) // verification succeeded
	if got != "" {
		t.Fatalf("expected no guidance on success, got: %s", got)
	}
}

func TestErrRegressionState_MaxWarnings(t *testing.T) {
	s := newErrRegressionState()
	// Fire 3 regressions; only first 2 should produce guidance.
	for i := 0; i < 3; i++ {
		s.recordVerify(buildOutput(2), true)
		s.recordEdit()
		got := s.recordVerify(buildOutput(10), true)
		if i < maxRegressionWarnings {
			if got == "" {
				t.Fatalf("expected guidance on call %d", i)
			}
		} else {
			if got != "" {
				t.Fatalf("expected no guidance after maxWarnings on call %d", i)
			}
		}
	}
}

func TestCountErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"no errors", "all good\nbuilding...\nok", 0},
		{"single error", "error: undefined variable x", 1},
		{"multiple errors", "error: foo\nerror: bar\nerror: baz", 3},
		{"mixed case", "Error: something\nERROR: other", 2},
		{"with file prefix", "main.go:10: error: syntax error", 1},
		{"filters zero errors", "0 errors found\nno error detected", 0},
		{"go compiler", "./main.go:5:2: undefined: foo\n./main.go:6:3: undefined: bar", 0}, // Go compiler uses "undefined:" not "error:"
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countVerifyErrors(tt.input)
			if got != tt.want {
				t.Errorf("countVerifyErrors(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestErrorRegressionCheckCommand(t *testing.T) {
	a := &Agent{errRegression: newErrRegressionState()}
	// Non-run_command tools should be ignored.
	if g := a.errorRegressionCheckCommand("read_file", nil, buildOutput(5), true); g != "" {
		t.Fatalf("expected empty for non-verify tool, got: %s", g)
	}
	// Non-verify commands should be ignored.
	if g := a.errorRegressionCheckCommand("run_command", []byte(`{"command":"ls -la"}`), buildOutput(5), true); g != "" {
		t.Fatalf("expected empty for non-verify command, got: %s", g)
	}
}

// buildOutput returns a string with n error-like lines.
func buildOutput(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("error: something broke\n")
	}
	return b.String()
}
