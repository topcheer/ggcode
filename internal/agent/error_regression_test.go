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
	got := s.recordVerify("go test", "error: foo", true)
	if got != "" {
		t.Fatalf("expected no guidance on first verify, got: %s", got)
	}
}

func TestErrRegressionState_NoEditsBetweenVerifies(t *testing.T) {
	s := newErrRegressionState()
	// First verify sets baseline.
	s.recordVerify("go test", buildOutput(5), true)
	// Second verify with more errors but no edits in between.
	got := s.recordVerify("go test", buildOutput(10), true)
	if got != "" {
		t.Fatalf("expected no guidance without edits, got: %s", got)
	}
}

func TestErrRegressionState_RegressionDetected(t *testing.T) {
	s := newErrRegressionState()
	s.recordVerify("go test", buildOutput(2), true)
	s.recordEdit()
	got := s.recordVerify("go test", buildOutput(8), true) // +6 > threshold of 3
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
	s.recordVerify("go test", buildOutput(3), true)
	s.recordEdit()
	got := s.recordVerify("go test", buildOutput(5), true) // +2 < threshold of 3
	if got != "" {
		t.Fatalf("expected no guidance for small increase, got: %s", got)
	}
}

func TestErrRegressionState_ErrorDecreasesNoWarning(t *testing.T) {
	s := newErrRegressionState()
	s.recordVerify("go test", buildOutput(10), true)
	s.recordEdit()
	got := s.recordVerify("go test", buildOutput(3), true) // -7, improvement
	if got != "" {
		t.Fatalf("expected no guidance when errors decrease, got: %s", got)
	}
}

func TestErrRegressionState_SuccessNoWarning(t *testing.T) {
	s := newErrRegressionState()
	s.recordVerify("go test", buildOutput(5), true)
	s.recordEdit()
	got := s.recordVerify("go test", "", false) // verification succeeded
	if got != "" {
		t.Fatalf("expected no guidance on success, got: %s", got)
	}
}

func TestErrRegressionState_MaxWarnings(t *testing.T) {
	s := newErrRegressionState()
	// Fire 3 regressions; only first 2 should produce guidance.
	for i := 0; i < 3; i++ {
		s.recordVerify("go test", buildOutput(2), true)
		s.recordEdit()
		got := s.recordVerify("go test", buildOutput(10), true)
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
		// #1498: 'undefined:' IS a Go compiler error - the old regex missed it
		// entirely, which is exactly the detector's target scenario going blind.
		{"go compiler", "./main.go:5:2: undefined: foo\n./main.go:6:3: undefined: bar", 2},
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

// Regression for #1498: the old error-lines regex required the word "error"
// plus [\s:], missing go build ('undefined:'), go test ('--- FAIL:'), cargo
// ('error[E0308]') and pytest output entirely - countVerifyErrors scored
// zero on the exact build/test stalls the detector targets.
func TestErrorLinesReMatchesToolchainOutput(t *testing.T) {
	for _, line := range []string{
		"./internal/agent/foo.go:12:5: undefined: helpers.Render",
		"--- FAIL: TestAgentLoop (0.30s)",
		"error[E0308]: mismatched types",
		"panic: runtime error: index out of range",
		"E       AssertionError: expected 3, got 4",
	} {
		if !errorLinesRe.MatchString(line) {
			t.Errorf("errorLinesRe missed toolchain error line: %q", line)
		}
	}
	for _, line := range []string{
		"--- PASS: TestSomething (0.10s)",
		"ok  \texample.com/pkg\t0.5s",
		"all checks passed, no findings",
	} {
		if errorLinesRe.MatchString(line) {
			t.Errorf("errorLinesRe false-positive on: %q", line)
		}
	}
}
