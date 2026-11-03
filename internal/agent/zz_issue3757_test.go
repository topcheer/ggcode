package agent

import (
	"strings"
	"testing"
)

func TestIssue3757_ScopeNarrowingNotResolved(t *testing.T) {
	v := newVerifyRegressionState()

	v.classifyErrorsWithTransition([]string{
		"pkg/a: error one",
		"pkg/b: error two",
		"pkg/c: error three",
	}, "go test ./...")

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

	tr, summary := v.classifyErrorsWithTransition([]string{"pkg/b: bang"}, "go test ./pkg/b/")
	if strings.Contains(summary, "REGRESSION") || len(tr.newErrors) > 0 {
		t.Fatalf("cross-scope comparison must not fire: %q", summary)
	}
}

func TestIssue3757_SameScopeRegressionStillDetected(t *testing.T) {
	v := newVerifyRegressionState()
	v.classifyErrorsWithTransition([]string{"pkg/a: boom"}, "go test ./pkg/a/")

	tr, _ := v.classifyErrorsWithTransition([]string{
		"pkg/a: boom",
		"pkg/a: NEW failure introduced by the fix",
	}, "go test ./pkg/a/")
	if len(tr.newErrors) != 1 {
		t.Fatalf("same-scope new error must still be detected, got %+v", tr.newErrors)
	}
}
