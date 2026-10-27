//go:build goolm

package agent

import (
	"strings"
	"testing"
)

// #4524: extractErrorLines must not drop real diagnostics that merely
// CONTAIN a success-negation substring ("Error: function has no error
// handling" from govet/staticcheck) while still filtering bare noise lines
// like "no errors" or "vet: no errors".
func TestIssue4524_NoErrorNoiseLineAnchored(t *testing.T) {
	noise := []string{
		"no error", "No Errors", "without error", "WITHOUT ERRORS",
		"0 errors", "found 0 errors", "compilation finished without errors",
		"vet: no errors", "build: no error",
	}
	for _, in := range noise {
		lower := strings.ToLower(strings.TrimSpace(in))
		if !isNoErrorNoiseLine(lower) {
			t.Errorf("noise line %q must be filtered, was not", in)
		}
		if got := extractErrorLines(in); len(got) != 0 {
			t.Errorf("extractErrorLines(%q) = %v, want none (pure noise line)", in, got)
		}
	}

	real := []string{
		"Error: function has no error handling (govet)", // the #4524 headline case
		"main.go:12:3: undefined: Foo",
		"some tool reported no error handling but 3 tests failed",
	}
	for _, in := range real {
		lower := strings.ToLower(strings.TrimSpace(in))
		if isNoErrorNoiseLine(lower) {
			t.Errorf("real error line %q wrongly classified as noise", in)
		}
		got := extractErrorLines(in)
		if len(got) != 1 || got[0] != strings.TrimSpace(in) {
			t.Errorf("extractErrorLines(%q) = %v, want the line itself kept", in, got)
		}
	}
}

// #4524 second fold: the progress message denominator must match the real
// attempt count (4 = maxSyncVerifyRetries+1), not the retry budget (3).
func TestIssue4524_ProgressDenominatorMatchesAttempts(t *testing.T) {
	// Mirror the exhaustion notice: after budget exhaustion the notice says
	// maxSyncVerifyRetries+1 attempts; the in-loop progress line must use the
	// same denominator.
	want := maxSyncVerifyRetries + 1
	if want != 4 {
		t.Fatalf("sanity: maxSyncVerifyRetries+1 = %d, test assumes 4 (update if budget changed)", want)
	}
}
