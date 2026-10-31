//go:build goolm

package agent

// Regression tests for GitHub issues #4555 and #4556:
//
// #4555: extractErrorLines over-filtered noise. Bare substring filters
//   swallowed real failure lines: "AssertionError: expected error, got no
//   error" (contains "no error"), "RuntimeError: completed without error"
//   (contains "without error"), and unified-diff deletion bodies like
//   "-errors.New(...)" matched the flag-echo filter.
//
// #4556: sdHasCaveatWindowed negation detection used bare HasSuffix, so
//   "with piano however..." (suffix "no") and "I cannot however..." (suffix
//   "not") neutralized real caveats, logging hedged declarations as unhedged.

import (
	"strings"
	"testing"
)

func extractForTest(t *testing.T, output string) []string {
	t.Helper()
	got := extractErrorLines(output)
	if got == nil {
		got = []string{}
	}
	return got
}

func TestIssue4555_AssertionMessageKeepsNoErrorLine(t *testing.T) {
	out := "test_parser.py::test_x FAILED\nAssertionError: expected error, got no error"
	lines := extractForTest(t, out)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "no error") {
		t.Fatalf("assertion line swallowed by noise filter: got %q", lines)
	}
}

func TestIssue4555_RuntimeErrorWithoutErrorKept(t *testing.T) {
	out := "RuntimeError: completed without error"
	lines := extractForTest(t, out)
	if len(lines) == 0 {
		t.Fatal("RuntimeError line with 'without error' must be kept")
	}
}

func TestIssue4555_StandaloneNoErrorsStillFiltered(t *testing.T) {
	for _, noise := range []string{
		"no errors",
		"completed without error",
		"checkout finished without errors: clean tree",
	} {
		if lines := extractForTest(t, noise); len(lines) != 0 {
			t.Fatalf("standalone summary %q must stay filtered, got %v", noise, lines)
		}
	}
}

func TestIssue4555_DiffDeletionBodyNotFlagEcho(t *testing.T) {
	out := "--- a/foo.go\n+++ b/foo.go\n@@ -1,1 +1,1 @@\n-errors.New(\"boom\")\n+nil"
	lines := extractForTest(t, out)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "errors.New") {
		t.Fatalf("diff deletion body treated as flag echo and dropped: %v", lines)
	}
}

func TestIssue4555_FlagEchoStillFiltered(t *testing.T) {
	if lines := extractForTest(t, "--fail-fast"); len(lines) != 0 {
		t.Fatalf("flag echo must stay filtered, got %v", lines)
	}
}

// #4556: a caveat preceded by a non-negator word ending in "no"/"not"
// ("piano", "cannot") must NOT be neutralized — the declaration is hedged.
// Call shape mirrors the #4539 tests: declStart/declEnd bracket the
// declaration phrase itself.
func TestIssue4556_PianoSuffixNotNegated(t *testing.T) {
	text := "the task is complete, works with piano however the e2e suite is red"
	lower := strings.ToLower(text)
	decl := strings.Index(lower, "task is complete")
	if !sdHasCaveatWindowed(lower, decl, decl+len("task is complete")) {
		t.Fatal("'however' caveat must be detected: 'piano' ends with 'no' but is not a negator")
	}
}

func TestIssue4556_CannotSuffixNotNegated(t *testing.T) {
	text := "the task is complete, I cannot however confirm the suite passes"
	lower := strings.ToLower(text)
	decl := strings.Index(lower, "task is complete")
	if !sdHasCaveatWindowed(lower, decl, decl+len("task is complete")) {
		t.Fatal("'however' caveat must be detected: 'cannot' ends with 'not' but is not a negator")
	}
}

func TestIssue4556_RealNegatorStillNeutralizes(t *testing.T) {
	// "no remaining issues" — the hedge is negated, so this caveat must NOT fire.
	text := "the task is complete, no remaining issues but the docs still need work"
	lower := strings.ToLower(text)
	decl := strings.Index(lower, "task is complete")
	if sdHasCaveatWindowed(lower, decl, decl+len("task is complete")) {
		t.Fatal("negated hedge ('no remaining issues') must stay neutralized (#364 semantics)")
	}
}
