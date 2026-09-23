package agent

import "testing"

// #2664: the redirect token scan only recognized `>`, `>>`, and `>file`
// forms. stderr-only (`2>`/`2>>`), stdout-fd-prefixed (`1>`), bash
// aggregation (`&>`/`>&`), and attached forms (`2>file`) all write files
// yet returned false - so a `go build ./... 2> build.err` run between two
// identical `go test ./...` calls left the commandCache entry alive (stale
// [cached] replay with a false "no source files have changed" note) and the
// build-idempotency detector counted 0 edits.
func TestIssue2664_RedirectFormsDetected(t *testing.T) {
	mutating := []string{
		// Issue's six cases (all verified missed on pre-fix main).
		"go build ./... 2> build.err",
		"go build ./... 2>> build.err",
		"go build ./... 1> out.txt",
		"go build ./... &> log.txt",
		"go build ./... >& log.txt",
		"go build ./... > /dev/null 2> err.txt",
		// Attached (no-space) variants of the same forms.
		"go build ./... 2>build.err",
		"go build ./... 1>out.txt",
		"go build ./... &>log.txt",
		"go test ./... &> session.log",
	}
	for _, cmd := range mutating {
		if !shellMutatesSources(cmd) {
			t.Errorf("shellMutatesSources(%q) = false, want true (#2664 missed redirect form)", cmd)
		}
	}
}

// #1875/#2664 FP guards: fd-prefixed and aggregation redirects into
// /dev/* devices are noise suppression and must stay non-mutating; the
// duplicate-fd form `2>&1` writes no file of its own; quoted content and
// bare non-file targets still pass through.
func TestIssue2664_RedirectFPGuards(t *testing.T) {
	nonMutating := []string{
		"go build ./... 2> /dev/null",
		"go build ./... &> /dev/null",
		"go build ./... >& /dev/null",
		"go build ./... 2>> /dev/null",
		"go build ./... 2>&1",
		"go build ./... > /dev/null 2>&1", // canonical noise idiom (#1875)
		"go build ./... 2> log",           // target has no "." or "/": not file-shaped
		"grep '2> note' readme",
	}
	for _, cmd := range nonMutating {
		if shellMutatesSources(cmd) {
			t.Errorf("shellMutatesSources(%q) = true, want false (FP on non-mutating form)", cmd)
		}
	}
}
