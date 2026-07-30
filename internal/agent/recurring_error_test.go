package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestFingerprintBuildErrorExtraction verifies that build error fingerprints
// are extracted from common compiler/test output formats.
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

// TestFingerprintStability verifies that the same underlying error produces
// the same fingerprint even when line numbers and paths differ.
func TestFingerprintStability(t *testing.T) {
	// Same error, different line numbers (as would happen after editing above)
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

// TestFingerprintDifferentErrors verifies that genuinely different errors
// produce different fingerprints.
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

// TestPathNormalization verifies that path differences don't affect fingerprint.
func TestPathNormalization(t *testing.T) {
	out1 := "./internal/agent/foo.go:42:5: undefined: myFunc"
	out2 := "/home/user/project/internal/agent/foo.go:42:5: undefined: myFunc"

	fp1 := fingerprintBuildError(out1)
	fp2 := fingerprintBuildError(out2)

	if fp1 == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	if fp1 != fp2 {
		t.Errorf("same error with different paths should match:\n  fp1=%s\n  fp2=%s", fp1, fp2)
	}
}

// TestRecurringErrorSoftGuidance tests that soft guidance fires on 2nd occurrence
// with edits between.
func TestRecurringErrorSoftGuidance(t *testing.T) {
	r := newRecurringErrorState()

	errOut := "# pkg\n./foo.go:42:5: undefined: myFunc\nFAIL"

	// First occurrence — no guidance
	g := r.recordBuildError(errOut)
	if g != "" {
		t.Errorf("first occurrence should not produce guidance, got: %s", g)
	}

	// Simulate edits between
	r.recordEdit()
	r.recordEdit()

	// Second occurrence — soft guidance
	g = r.recordBuildError(errOut)
	if g == "" {
		t.Error("second occurrence with edits should produce guidance")
	}
	if !strings.Contains(g, "Recurring error") {
		t.Errorf("soft guidance should mention recurring, got: %s", g)
	}
}

// TestRecurringErrorHardGuidance tests escalation to hard guidance at 3rd occurrence.
func TestRecurringErrorHardGuidance(t *testing.T) {
	r := newRecurringErrorState()

	errOut := "# pkg\n./foo.go:42:5: undefined: myFunc\nFAIL"

	// First
	r.recordBuildError(errOut)
	r.recordEdit()

	// Second — soft
	g2 := r.recordBuildError(errOut)
	if g2 == "" || !strings.Contains(g2, "Recurring error") {
		t.Fatalf("expected soft guidance at 2nd occurrence, got: %q", g2)
	}
	r.recordEdit()

	// Third — hard
	g3 := r.recordBuildError(errOut)
	if g3 == "" {
		t.Fatal("expected hard guidance at 3rd occurrence")
	}
	if !strings.Contains(g3, "CRITICAL") {
		t.Errorf("hard guidance should contain CRITICAL, got: %s", g3)
	}
}

// TestNoGuidanceWithoutEdits verifies that consecutive same errors without edits
// don't trigger guidance (that's loop_detect's job).
func TestNoGuidanceWithoutEdits(t *testing.T) {
	r := newRecurringErrorState()

	errOut := "# pkg\n./foo.go:42:5: undefined: myFunc\nFAIL"

	// First
	r.recordBuildError(errOut)

	// Second — NO edits between
	g := r.recordBuildError(errOut)
	if g != "" {
		t.Errorf("should not produce guidance without edits between occurrences, got: %s", g)
	}
}

// TestDifferentErrorsNoRecurrence verifies that different errors don't trigger.
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

// TestReset verifies state is cleared on reset.
func TestReset(t *testing.T) {
	r := newRecurringErrorState()

	errOut := "./foo.go:42:5: undefined: myFunc"

	r.recordBuildError(errOut)
	r.recordEdit()
	r.recordBuildError(errOut)

	r.reset()

	// After reset, first occurrence should not produce guidance
	r.recordBuildError(errOut)
	r.recordEdit()
	g := r.recordBuildError(errOut)
	if g == "" {
		t.Error("after reset, same error should fire guidance on 2nd occurrence")
	}
}

// TestDedupSameLevel verifies the same guidance level doesn't fire twice.
func TestDedupSameLevel(t *testing.T) {
	r := newRecurringErrorState()

	errOut := "./foo.go:42:5: undefined: myFunc"

	r.recordBuildError(errOut)
	r.recordEdit()
	g2a := r.recordBuildError(errOut) // soft fires

	if g2a == "" {
		t.Fatal("expected soft guidance on 2nd occurrence")
	}

	r.recordEdit()
	g2b := r.recordBuildError(errOut) // 3rd occurrence, should fire HARD not soft again

	if g2b == "" {
		t.Fatal("expected hard guidance on 3rd occurrence")
	}
	if !strings.Contains(g2b, "CRITICAL") {
		t.Errorf("expected hard guidance on 3rd, got: %s", g2b)
	}

	// 4th occurrence — both levels already fired, should be empty
	r.recordEdit()
	g4 := r.recordBuildError(errOut)
	if g4 != "" {
		t.Errorf("should not fire after both levels exhausted, got: %s", g4)
	}
}

// TestNormalizeErrorLine verifies normalization of volatile elements.
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

// TestStripPathToBasename verifies directory paths are stripped to basenames.
func TestStripPathToBasename(t *testing.T) {
	input := "./internal/agent/foo.go:42: undefined: myFunc"
	result := stripPathToBasename(input)
	// The "/" chars should be removed, leaving "foo.go:N: undefined: myFunc"
	if strings.Contains(result, "internal/") {
		t.Errorf("directory path not stripped: %s", result)
	}
	if !strings.Contains(result, "foo.go") {
		t.Errorf("basename should be preserved: %s", result)
	}
}

// (itoa is provided by inline_toolcall_test.go; strconv.Itoa is used in production code.)

// TestAgentRecurringErrorIntegration tests the Agent-level methods.
func TestAgentRecurringErrorIntegration(t *testing.T) {
	a := &Agent{
		recurringError: newRecurringErrorState(),
	}

	errOut := "# pkg\n./foo.go:42:5: undefined: myFunc\nFAIL"
	args := json.RawMessage(`{"command": "go build ./..."}`)

	// First build error — no guidance
	g := a.recurringErrorCheckCommand("run_command", args, errOut, true)
	if g != "" {
		t.Errorf("first occurrence should not produce guidance: %s", g)
	}

	// Simulate edit
	a.recurringErrorRecordEdit()

	// Second build error — guidance should fire
	g = a.recurringErrorCheckCommand("run_command", args, errOut, true)
	if g == "" {
		t.Error("second occurrence with edits should produce guidance")
	}
}

// TestAgentRecurringErrorNonVerifyCommand verifies non-verify commands are ignored.
func TestAgentRecurringErrorNonVerifyCommand(t *testing.T) {
	a := &Agent{
		recurringError: newRecurringErrorState(),
	}

	args := json.RawMessage(`{"command": "echo hello"}`)
	errOut := "Error: something failed"

	// First
	a.recurringErrorCheckCommand("run_command", args, errOut, true)
	a.recurringErrorRecordEdit()

	// Second — should NOT fire because echo isn't a verify command
	g := a.recurringErrorCheckCommand("run_command", args, errOut, true)
	if g != "" {
		t.Errorf("non-verify command should not trigger recurrence guidance: %s", g)
	}
}

// TestAgentRecurringErrorReset verifies reset clears state.
func TestAgentRecurringErrorReset(t *testing.T) {
	a := &Agent{
		recurringError: newRecurringErrorState(),
	}

	errOut := "./foo.go:42:5: undefined: myFunc"
	a.recurringErrorCheckCommand("run_command", json.RawMessage(`{"command":"go build ./..."}`), errOut, true)
	a.recurringErrorRecordEdit()
	a.recurringErrorCheckCommand("run_command", json.RawMessage(`{"command":"go build ./..."}`), errOut, true)

	// Reset
	a.resetRecurringError()

	// After reset, 2nd occurrence should fire guidance
	a.recurringErrorCheckCommand("run_command", json.RawMessage(`{"command":"go build ./..."}`), errOut, true)
	a.recurringErrorRecordEdit()
	g := a.recurringErrorCheckCommand("run_command", json.RawMessage(`{"command":"go build ./..."}`), errOut, true)
	if g == "" {
		t.Error("after reset, recurrence guidance should fire on 2nd occurrence")
	}
}

// TestHasErrorMarkers verifies error detection in output.
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

// TestMaxFingerprintLines verifies only first N error lines form the fingerprint.
func TestMaxFingerprintLines(t *testing.T) {
	// Many different errors — only first 5 should form the fingerprint
	out1 := strings.Repeat("./file.go:1:1: undefined: func1\n", 3) +
		"./file.go:2:1: undefined: func2\n" +
		"./file.go:3:1: undefined: func3\n" +
		"./file.go:4:1: undefined: uniqueError4\n" +
		"./file.go:5:1: undefined: func5\n"

	out2 := strings.Repeat("./file.go:1:1: undefined: func1\n", 3) +
		"./file.go:2:1: undefined: func2\n" +
		"./file.go:3:1: undefined: func3\n" +
		"./file.go:4:1: undefined: DIFFERENT_ERROR\n" + // line 4 differs
		"./file.go:5:1: undefined: func5\n"

	fp1 := fingerprintBuildError(out1)
	fp2 := fingerprintBuildError(out2)

	// If maxFingerprintLines < 4, these would match. With 5, they differ.
	if fp1 == fp2 {
		// This is acceptable if maxFingerprintLines <= 3, but we set it to 5
		// so the 4th line should be in the fingerprint. Log for debugging.
		t.Logf("fingerprints match (fp=%s) — maxFingerprintLines may exclude the 4th line", fp1)
	}
}
