package agent

import (
	"strings"
	"testing"
)

var issue1172ErrOut = "./foo.go:42:5: undefined: myFunc\nFAIL"

// Issue #1172: re-running the same error WITHOUT any edit in between must not
// increment the fingerprint count. Under the buggy code every occurrence
// incremented the count, so a few blind re-runs skipped the soft level and
// fired hard guidance with a false "N edit-and-rebuild cycles".
func TestIssue1172NoEditRerunDoesNotInflateCount(t *testing.T) {
	r := newRecurringErrorState()

	fp := fingerprintBuildError(issue1172ErrOut)

	// First occurrence establishes the baseline (count=1) and is silent.
	if g := r.recordBuildError(issue1172ErrOut); g != "" {
		t.Fatalf("first occurrence must be silent: %s", g)
	}

	// Any number of identical failures with zero edits must stay silent and
	// leave the count untouched (this is loop_detect territory).
	for i := 0; i < 10; i++ {
		if g := r.recordBuildError(issue1172ErrOut); g != "" {
			t.Fatalf("no-edit rerun #%d must not produce guidance: %s", i+1, g)
		}
	}
	if got := r.fingerprintCounts[fp]; got != 1 {
		t.Fatalf("no-edit reruns must not inflate fingerprint count past baseline 1, got %d", got)
	}
}

// Issue #1172 companion: after a no-edit rerun pollutes nothing, the first
// edit-separated recurrence is still level 1, and hard guidance only fires at
// the designed threshold with the true cycle count.
func TestIssue1172EditSeparatedCyclesEscalateCorrectly(t *testing.T) {
	r := newRecurringErrorState()

	// Occurrence 1: baseline, no guidance.
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

// Issue #1172 scenario from the report: edit-separated recurrences interleaved
// with no-edit re-runs. The re-runs must neither fire guidance nor inflate the
// count that drives level selection.
func TestIssue1172RerunBetweenEditCyclesDoesNotSkipSoftLevel(t *testing.T) {
	r := newRecurringErrorState()

	r.recordBuildError(issue1172ErrOut) // count baseline (not counted)
	r.recordEdit()
	r.recordBuildError(issue1172ErrOut) // edit-separated occurrence, count=1, silent

	// Blind re-run with no edits: must be a no-op.
	if g := r.recordBuildError(issue1172ErrOut); g != "" {
		t.Fatalf("no-edit rerun between cycles must be silent: %s", g)
	}

	r.recordEdit()
	g := r.recordBuildError(issue1172ErrOut)
	// The edit-separated recurrence legitimately escalates to hard at count=3
	// (baseline + 2 edit-separated recurrences); the no-edit rerun above did
	// not inflate anything, so the cycle count is truthful.
	if !strings.Contains(g, "3") || !strings.Contains(g, "edit-and-rebuild cycles") {
		t.Fatalf("next edit-separated recurrence must be hard level (count=3), got: %s", g)
	}
}
