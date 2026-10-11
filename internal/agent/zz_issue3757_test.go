package agent

// #3757 probes: the regression classifier must not compare error sets
// across DIFFERENT verification scopes. Narrowing `go test ./...` to
// `go test ./pkg/` used to label the dropped wide-scope errors RESOLVED
// ("your fix is working" - rewarding narrowed verification), and
// alternating packages manufactured REGRESSION noise.

import (
	"strings"
	"testing"
)

func TestIssue3757_ScopeNarrowingNotResolved(t *testing.T) {
	v := newVerifyRegressionState()
	// Baseline: wide scope, 3 errors.
	v.classifyErrorsWithTransition([]string{
		"pkg/a: error one",
		"pkg/b: error two",
		"pkg/c: error three",
	}, "go test ./...")
	// Narrowed scope: same classifier, only pkg/a's error remains.
	tr, summary := v.classifyErrorsWithTransition([]string{
		"pkg/a: error one",
	}, "go test ./pkg/a/")
	if strings.Contains(summary, "RESOLVED") || tr.resolvedCount > 0 {
		t.Fatalf("pure scope narrowing must not be reported as RESOLVED: %q (resolved=%d)", summary, tr.resolvedCount)
	}
	if strings.Contains(summary, "REGRESSION") || len(tr.newErrors) > 0 {
		t.Fatalf("reseeded scope must not report NEW errors either: %q", summary)
	}
}

func TestIssue3757_AlternatingScopesNoNoise(t *testing.T) {
	v := newVerifyRegressionState()
	v.classifyErrorsWithTransition([]string{"pkg/a: boom"}, "go test ./pkg/a/")
	// Switch to pkg/b: the different scope must reseed, not flag pkg/b's
	// error as NEW/REGRESSION against pkg/a's baseline.
	tr, summary := v.classifyErrorsWithTransition([]string{"pkg/b: bang"}, "go test ./pkg/b/")
	if strings.Contains(summary, "REGRESSION") || len(tr.newErrors) > 0 {
		t.Fatalf("cross-scope comparison must not fire: %q", summary)
	}
}

func TestIssue3757_SameScopeRegressionStillDetected(t *testing.T) {
	v := newVerifyRegressionState()
	v.classifyErrorsWithTransition([]string{"pkg/a: boom"}, "go test ./pkg/a/")
	// SAME scope, a new error appears: genuine regression detection must
	// keep working exactly as before.
	tr, _ := v.classifyErrorsWithTransition([]string{
		"pkg/a: boom",
		"pkg/a: NEW failure introduced by the fix",
	}, "go test ./pkg/a/")
	if len(tr.newErrors) != 1 {
		t.Fatalf("same-scope new error must still be detected, got %+v", tr.newErrors)
	}
}
