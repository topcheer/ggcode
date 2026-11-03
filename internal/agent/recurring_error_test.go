package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFingerprintBuildErrorExtraction(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		wantEmpty bool
	}{
		{
			name:      "empty output",
			output:    "",
			wantEmpty: true,
		},
		{
			name:      "no errors",
			output:    "Build successful\nAll tests passed\n",
			wantEmpty: true,
		},
		{
			name:      "Go compiler error",
			output:    "# internal/agent\n./agent.go:42:5: undefined: someFunc\n./agent.go:100:2: cannot use x (type int) as type string",
			wantEmpty: false,
		},
		{
			name:      "test failure",
			output:    "--- FAIL: TestSomething (0.00s)\n    foo_test.go:42: expected 5, got 3\nFAIL",
			wantEmpty: false,
		},
		{
			name:      "Python traceback",
			output:    "Traceback (most recent call last):\n  File \"app.py\", line 10, in <module>\nNameError: name 'foo' is not defined",
			wantEmpty: false,
		},
		{
			name:      "generic error",
			output:    "Error: compilation failed\nError: module not found",
			wantEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := fingerprintBuildError(tt.output)
			if tt.wantEmpty && fp != "" {
				t.Errorf("expected empty fingerprint, got %q", fp)
			}
			if !tt.wantEmpty && fp == "" {
				t.Errorf("expected non-empty fingerprint for %q", tt.name)
			}
		})
	}
}

func TestFingerprintStability(t *testing.T) {

	out1 := "# pkg\n./foo.go:42:5: undefined: myFunc\n./foo.go:100:2: cannot use x"
	out2 := "# pkg\n./foo.go:45:5: undefined: myFunc\n./foo.go:103:2: cannot use x"

	fp1 := fingerprintBuildError(out1)
	fp2 := fingerprintBuildError(out2)

	if fp1 == "" {
		t.Fatal("expected non-empty fingerprint for out1")
	}
	if fp1 != fp2 {
		t.Errorf("fingerprints should match for same error with different line numbers:\n  fp1=%s\n  fp2=%s", fp1, fp2)
	}
}

func TestFingerprintDifferentErrors(t *testing.T) {
	out1 := "./foo.go:42:5: undefined: myFunc"
	out2 := "./foo.go:42:5: undefined: otherFunc"

	fp1 := fingerprintBuildError(out1)
	fp2 := fingerprintBuildError(out2)

	if fp1 == "" || fp2 == "" {
		t.Fatal("expected non-empty fingerprints")
	}
	if fp1 == fp2 {
		t.Errorf("different errors should produce different fingerprints: %s == %s", fp1, fp2)
	}
}

func TestPathNormalization(t *testing.T) {
	out1 := "./internal/agent/foo.go:42:5: undefined: myFunc"
	out2 := "internal/agent/foo.go:42:5: undefined: myFunc"

	fp1 := fingerprintBuildError(out1)
	fp2 := fingerprintBuildError(out2)

	if fp1 == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	if fp1 != fp2 {
		t.Errorf("same error with ./ spelling variance should match:\n  fp1=%s\n  fp2=%s", fp1, fp2)
	}

	out3 := "/home/user/project/internal/agent/foo.go:42:5: undefined: myFunc"
	if fp3 := fingerprintBuildError(out3); fp3 == fp1 {
		t.Errorf("absolute path must stay distinct from relative (#1486 sibling separation)")
	}
}

func TestRecurringErrorSoftGuidance(t *testing.T) {
	r := newRecurringErrorState()

	errOut := "# pkg\n./foo.go:42:5: undefined: myFunc\nFAIL"

	g := r.recordBuildError(errOut)
	if g != "" {
		t.Errorf("first occurrence should not produce guidance, got: %s", g)
	}

	r.recordEdit()
	r.recordEdit()

	g = r.recordBuildError(errOut)
	if g == "" {
		t.Error("second occurrence with edits should produce guidance")
	}
	if !strings.Contains(g, "Recurring error") {
		t.Errorf("soft guidance should mention recurring, got: %s", g)
	}
}

func TestRecurringErrorHardGuidance(t *testing.T) {
	r := newRecurringErrorState()

	errOut := "# pkg\n./foo.go:42:5: undefined: myFunc\nFAIL"

	r.recordBuildError(errOut)
	r.recordEdit()

	g2 := r.recordBuildError(errOut)
	if g2 == "" || !strings.Contains(g2, "Recurring error") {
		t.Fatalf("expected soft guidance at 2nd occurrence, got: %q", g2)
	}
	r.recordEdit()

	g3 := r.recordBuildError(errOut)
	if g3 == "" {
		t.Fatal("expected hard guidance at 3rd occurrence")
	}
	if !strings.Contains(g3, "CRITICAL") {
		t.Errorf("hard guidance should contain CRITICAL, got: %s", g3)
	}
}

func TestNoGuidanceWithoutEdits(t *testing.T) {
	r := newRecurringErrorState()

	errOut := "# pkg\n./foo.go:42:5: undefined: myFunc\nFAIL"

	r.recordBuildError(errOut)

	g := r.recordBuildError(errOut)
	if g != "" {
		t.Errorf("should not produce guidance without edits between occurrences, got: %s", g)
	}
}

func TestDifferentErrorsNoRecurrence(t *testing.T) {
	r := newRecurringErrorState()

	err1 := "./foo.go:42:5: undefined: funcA"
	err2 := "./foo.go:42:5: undefined: funcB"

	r.recordBuildError(err1)
	r.recordEdit()
	g := r.recordBuildError(err2)
	if g != "" {
		t.Errorf("different errors should not trigger recurrence guidance, got: %s", g)
	}
}

func TestReset(t *testing.T) {
	r := newRecurringErrorState()

	errOut := "./foo.go:42:5: undefined: myFunc"

	r.recordBuildError(errOut)
	r.recordEdit()
	r.recordBuildError(errOut)

	r.reset()

	r.recordBuildError(errOut)
	r.recordEdit()
	g := r.recordBuildError(errOut)
	if g == "" {
		t.Error("after reset, same error should fire guidance on 2nd occurrence")
	}
}

func TestDedupSameLevel(t *testing.T) {
	r := newRecurringErrorState()

	errOut := "./foo.go:42:5: undefined: myFunc"

	r.recordBuildError(errOut)
	r.recordEdit()
	g2a := r.recordBuildError(errOut)

	if g2a == "" {
		t.Fatal("expected soft guidance on 2nd occurrence")
	}

	r.recordEdit()
	g2b := r.recordBuildError(errOut)

	if g2b == "" {
		t.Fatal("expected hard guidance on 3rd occurrence")
	}
	if !strings.Contains(g2b, "CRITICAL") {
		t.Errorf("expected hard guidance on 3rd, got: %s", g2b)
	}

	r.recordEdit()
	g4 := r.recordBuildError(errOut)
	if g4 != "" {
		t.Errorf("should not fire after both levels exhausted, got: %s", g4)
	}
}

func TestNormalizeErrorLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(t *testing.T, result string)
	}{
		{
			name:  "strips line:col",
			input: "./internal/agent/foo.go:42:5: undefined: myFunc",
			check: func(t *testing.T, r string) {
				if strings.Contains(r, ":42:") || strings.Contains(r, ":5:") {
					t.Errorf("line:col not stripped: %s", r)
				}
			},
		},
		{
			name:  "strips line word",
			input: "Error at line 42: something failed",
			check: func(t *testing.T, r string) {
				if strings.Contains(r, "line 42") {
					t.Errorf("line number not stripped: %s", r)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeErrorLine(tt.input)
			tt.check(t, result)
		})
	}
}

func TestStripPathToBasename(t *testing.T) {
	if got := stripPathToBasename("./internal/agent/foo.go:42: undefined: myFunc"); got != "internal/agent/foo.go:42: undefined: myFunc" {
		t.Errorf("./-prefix must be stripped, got: %s", got)
	}
	if got := stripPathToBasename("/abs/path/foo.go:1: err"); got != "abs/path/foo.go:1: err" {
		t.Errorf("leading slash must be stripped, named segments kept, got: %s", got)
	}
	if got := stripPathToBasename("../rel/foo.go:1: err"); got != "rel/foo.go:1: err" {
		t.Errorf("../-prefix must be stripped, got: %s", got)
	}
}

func TestAgentRecurringErrorIntegration(t *testing.T) {
	a := &Agent{
		recurringError: newRecurringErrorState(),
	}

	errOut := "# pkg\n./foo.go:42:5: undefined: myFunc\nFAIL"
	args := json.RawMessage(`{"command": "go build ./..."}`)

	g := a.recurringErrorCheckCommand("run_command", args, errOut, true)
	if g != "" {
		t.Errorf("first occurrence should not produce guidance: %s", g)
	}

	a.recurringErrorRecordEdit()

	g = a.recurringErrorCheckCommand("run_command", args, errOut, true)
	if g == "" {
		t.Error("second occurrence with edits should produce guidance")
	}
}

func TestAgentRecurringErrorNonVerifyCommand(t *testing.T) {
	a := &Agent{
		recurringError: newRecurringErrorState(),
	}

	args := json.RawMessage(`{"command": "echo hello"}`)
	errOut := "Error: something failed"

	a.recurringErrorCheckCommand("run_command", args, errOut, true)
	a.recurringErrorRecordEdit()

	g := a.recurringErrorCheckCommand("run_command", args, errOut, true)
	if g != "" {
		t.Errorf("non-verify command should not trigger recurrence guidance: %s", g)
	}
}

func TestAgentRecurringErrorReset(t *testing.T) {
	a := &Agent{
		recurringError: newRecurringErrorState(),
	}

	errOut := "./foo.go:42:5: undefined: myFunc"
	a.recurringErrorCheckCommand("run_command", json.RawMessage(`{"command":"go build ./..."}`), errOut, true)
	a.recurringErrorRecordEdit()
	a.recurringErrorCheckCommand("run_command", json.RawMessage(`{"command":"go build ./..."}`), errOut, true)

	a.resetRecurringError()

	a.recurringErrorCheckCommand("run_command", json.RawMessage(`{"command":"go build ./..."}`), errOut, true)
	a.recurringErrorRecordEdit()
	g := a.recurringErrorCheckCommand("run_command", json.RawMessage(`{"command":"go build ./..."}`), errOut, true)
	if g == "" {
		t.Error("after reset, recurrence guidance should fire on 2nd occurrence")
	}
}

func TestHasErrorMarkers(t *testing.T) {
	if !hasErrorMarkers("Error: compilation failed") {
		t.Error("should detect 'Error:' marker")
	}
	if !hasErrorMarkers("--- FAIL: TestSomething") {
		t.Error("should detect 'FAIL' marker")
	}
	if !hasErrorMarkers("panic: runtime error") {
		t.Error("should detect 'panic' marker")
	}
	if hasErrorMarkers("Build successful") {
		t.Error("should not detect markers in success output")
	}
	if hasErrorMarkers("") {
		t.Error("empty string should not have markers")
	}
}

func TestMaxFingerprintLines(t *testing.T) {

	out1 := strings.Repeat("./file.go:1:1: undefined: func1\n", 3) +
		"./file.go:2:1: undefined: func2\n" +
		"./file.go:3:1: undefined: func3\n" +
		"./file.go:4:1: undefined: uniqueError4\n" +
		"./file.go:5:1: undefined: func5\n"

	out2 := strings.Repeat("./file.go:1:1: undefined: func1\n", 3) +
		"./file.go:2:1: undefined: func2\n" +
		"./file.go:3:1: undefined: func3\n" +
		"./file.go:4:1: undefined: DIFFERENT_ERROR\n" +
		"./file.go:5:1: undefined: func5\n"

	fp1 := fingerprintBuildError(out1)
	fp2 := fingerprintBuildError(out2)

	if fp1 == fp2 {

		t.Logf("fingerprints match (fp=%s) — maxFingerprintLines may exclude the 4th line", fp1)
	}
}

func TestHasErrorMarkersIgnoresPassLines(t *testing.T) {
	green := "=== RUN   TestFailover\n--- PASS: TestFailover (0.00s)\n--- PASS: TestExpectedOutput (0.01s)\nPASS\nok  \texample.com/pkg\t0.5s"
	if hasErrorMarkers(green) {
		t.Fatal("green build output with TestFail*/TestExpected* names must not be flagged as error")
	}
	if !hasErrorMarkers("--- FAIL: TestSomething (0.00s)\n    expected 3, got 4") {
		t.Fatal("real failure output must still be flagged")
	}
}

func TestFingerprintSkipsGreenLines(t *testing.T) {
	outA := "--- PASS: TestFailover (0.00s)\n--- FAIL: TestAlpha (0.01s)\n    alpha_test.go:12: want 1 got 2\nok  pkg/x 0.5s"
	outB := "--- PASS: TestFailover (0.00s)\n--- FAIL: TestBeta (0.01s)\n    beta_test.go:34: want 3 got 4\nok  pkg/y 0.6s"
	fa := fingerprintBuildError(outA)
	fb := fingerprintBuildError(outB)
	if fa == "" || fb == "" {
		t.Fatalf("fingerprints must be non-empty, got %q / %q", fa, fb)
	}
	if fa == fb {
		t.Fatal("different errors sharing green preambles must NOT fingerprint identically (#1679 case 1)")
	}
}
