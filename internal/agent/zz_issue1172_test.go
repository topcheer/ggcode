package agent

import (
	"strings"
	"testing"
)

var issue1172ErrOut = "./foo.go:42:5: undefined: myFunc\nFAIL"

func TestIssue1172NoEditRerunDoesNotInflateCount(t *testing.T) {
	r := newRecurringErrorState()

	fp := fingerprintBuildError(issue1172ErrOut)

	if g := r.recordBuildError(issue1172ErrOut); g != "" {
		t.Fatalf("first occurrence must be silent: %s", g)
	}

	for i := 0; i < 10; i++ {
		if g := r.recordBuildError(issue1172ErrOut); g != "" {
			t.Fatalf("no-edit rerun #%d must not produce guidance: %s", i+1, g)
		}
	}
	if got := r.fingerprintCounts[fp]; got != 1 {
		t.Fatalf("no-edit reruns must not inflate fingerprint count past baseline 1, got %d", got)
	}
}

func TestIssue1172EditSeparatedCyclesEscalateCorrectly(t *testing.T) {
	r := newRecurringErrorState()

	if g := r.recordBuildError(issue1172ErrOut); g != "" {
		t.Fatalf("first occurrence must be silent: %s", g)
	}

	r.recordEdit()
	g := r.recordBuildError(issue1172ErrOut)
	if !strings.Contains(g, "2 times") {
		t.Fatalf("second occurrence (edit-separated) must fire soft guidance with count 2, got: %s", g)
	}

	r.recordEdit()
	g = r.recordBuildError(issue1172ErrOut)
	if !strings.Contains(g, "3") || !strings.Contains(g, "edit-and-rebuild cycles") {
		t.Fatalf("third recurrence must fire hard guidance with count 3, got: %s", g)
	}
}

func TestIssue1172RerunBetweenEditCyclesDoesNotSkipSoftLevel(t *testing.T) {
	r := newRecurringErrorState()

	r.recordBuildError(issue1172ErrOut)
	r.recordEdit()
	r.recordBuildError(issue1172ErrOut)

	if g := r.recordBuildError(issue1172ErrOut); g != "" {
		t.Fatalf("no-edit rerun between cycles must be silent: %s", g)
	}

	r.recordEdit()
	g := r.recordBuildError(issue1172ErrOut)

	if !strings.Contains(g, "3") || !strings.Contains(g, "edit-and-rebuild cycles") {
		t.Fatalf("next edit-separated recurrence must be hard level (count=3), got: %s", g)
	}
}
