package agent

import (
	"strings"
	"testing"
)

func TestVerifyRegression_FirstRunNoAnnotation(t *testing.T) {
	v := newVerifyRegressionState()
	errors := []string{
		"foo.go:42: undefined: someFunc",
		"bar.go:10: syntax error",
	}
	result := v.classifyErrors(errors)
	if result != "" {
		t.Errorf("first run should return empty annotation, got: %q", result)
	}
	if !v.hasBaseline {
		t.Error("first run should set hasBaseline=true")
	}
	if len(v.prevErrors) != 2 {
		t.Errorf("baseline should have 2 errors, got %d", len(v.prevErrors))
	}
}

func TestVerifyRegression_AllPersistent(t *testing.T) {
	v := newVerifyRegressionState()
	errors := []string{
		"foo.go:42: undefined: someFunc",
		"bar.go:10: syntax error",
	}
	v.classifyErrors(errors) // establish baseline

	// Same errors again — should be persistent
	result := v.classifyErrors(errors)
	if result == "" {
		t.Fatal("should return non-empty annotation for persistent errors")
	}
	if !strings.Contains(result, "PERSISTENT") {
		t.Error("should contain PERSISTENT category")
	}
	if strings.Contains(result, "REGRESSION") {
		t.Error("should NOT contain REGRESSION category")
	}
	if strings.Contains(result, "PROGRESS") {
		t.Error("should NOT contain PROGRESS category")
	}
}

func TestVerifyRegression_NewErrorDetected(t *testing.T) {
	v := newVerifyRegressionState()
	v.classifyErrors([]string{
		"foo.go:42: undefined: someFunc",
	})

	// Fix the first error but introduce a new one
	result := v.classifyErrors([]string{
		"bar.go:10: undefined: otherFunc",
	})
	if !strings.Contains(result, "REGRESSION") {
		t.Error("should contain REGRESSION for new error")
	}
	if !strings.Contains(result, "1 NEW") {
		t.Errorf("should report 1 NEW error, got: %s", result)
	}
	if !strings.Contains(result, "PROGRESS") {
		t.Error("should contain PROGRESS for resolved error")
	}
	if !strings.Contains(result, "1 error") {
		t.Errorf("should report 1 resolved error, got: %s", result)
	}
}

func TestVerifyRegression_LineNumberStability(t *testing.T) {
	v := newVerifyRegressionState()
	v.classifyErrors([]string{
		"foo.go:42: undefined: someFunc",
	})

	// Same error but at a different line number — should NOT be NEW
	result := v.classifyErrors([]string{
		"foo.go:50: undefined: someFunc",
	})
	if strings.Contains(result, "REGRESSION") {
		t.Error("same error at different line should NOT be REGRESSION")
	}
}

func TestVerifyRegression_PathStability(t *testing.T) {
	v := newVerifyRegressionState()
	v.classifyErrors([]string{
		"./internal/agent/foo.go:42: undefined: someFunc",
	})

	// Same error with absolute path — should NOT be NEW
	result := v.classifyErrors([]string{
		"/home/user/project/internal/agent/foo.go:42: undefined: someFunc",
	})
	if strings.Contains(result, "REGRESSION") {
		t.Error("same error at different path should NOT be REGRESSION")
	}
}

func TestVerifyRegression_PassClearsBaseline(t *testing.T) {
	v := newVerifyRegressionState()
	v.classifyErrors([]string{
		"foo.go:42: undefined: someFunc",
	})

	// Verification passes — should clear baseline
	result := v.classifyErrors(nil)
	if result != "" {
		t.Error("passing verification should return empty annotation")
	}

	// Next failure should be treated as first run (no REGRESSION annotation)
	result = v.classifyErrors([]string{
		"foo.go:42: undefined: someFunc",
	})
	if result != "" {
		t.Error("after clear, first failure should be treated as new baseline (empty annotation)")
	}
}

func TestVerifyRegression_MixedErrors(t *testing.T) {
	v := newVerifyRegressionState()
	v.classifyErrors([]string{
		"foo.go:42: undefined: funcA",
		"bar.go:10: undefined: funcB",
		"baz.go:5: syntax error",
	})

	// Fix funcB, keep funcA and syntax error, add new error
	result := v.classifyErrors([]string{
		"foo.go:42: undefined: funcA", // persistent
		"baz.go:5: syntax error",      // persistent
		"new.go:1: undefined: funcC",  // NEW regression
	})
	if !strings.Contains(result, "1 NEW") {
		t.Errorf("should detect 1 NEW error, got: %s", result)
	}
	if !strings.Contains(result, "PROGRESS") {
		t.Error("should report funcB as resolved")
	}
	if !strings.Contains(result, "PERSISTENT") {
		t.Error("should report persistent errors")
	}
}

func TestVerifyRegression_NewErrorCount(t *testing.T) {
	v := newVerifyRegressionState()
	v.classifyErrors([]string{"a.go:1: error one"})

	// Multiple new errors
	result := v.classifyErrors([]string{
		"b.go:1: error two",
		"c.go:1: error three",
		"d.go:1: error four",
	})
	if !strings.Contains(result, "3 NEW") {
		t.Errorf("should report 3 NEW errors, got: %s", result)
	}
}

func TestVerifyRegression_Reset(t *testing.T) {
	v := newVerifyRegressionState()
	v.classifyErrors([]string{"a.go:1: error one"})
	v.reset()

	if v.hasBaseline {
		t.Error("reset should clear hasBaseline")
	}
	if len(v.prevErrors) != 0 {
		t.Error("reset should clear prevErrors")
	}
}

func TestBuildRegressionSummary_MaxErrorsCapped(t *testing.T) {
	// Generate more errors than the cap
	newErrors := make([]string, maxRegressionErrors+3)
	for i := range newErrors {
		newErrors[i] = "error"
	}
	summary := buildRegressionSummary(newErrors, nil, 0)
	if !strings.Contains(summary, "more new error") {
		t.Error("should show 'more' indicator when exceeding cap")
	}
}
