package agent

import "testing"

// Issue #546: false_premise detector false positive on truthful zero-count
// reports + missed detection when a successful same-name command clears a
// build error.

// Bug 1: "found 0 matches" faithfully relaying a no-hit grep is truthful —
// it must NOT trigger the confabulation warning. Probe scenarios from the
// issue, plus the true-lie control that must still fire.
func TestIssue546FoundZeroIsTruthful(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("grep", "no matches found", true)
	if w := f.checkFalsePremise("The grep found 0 matches, so the symbol does not exist."); w != "" {
		t.Errorf("truthful zero-count report flagged: %s", w)
	}
	if w := f.checkFalsePremise("The search returned 0 results as expected."); w != "" {
		t.Errorf("truthful zero-count return flagged: %s", w)
	}
}

func TestIssue546NonZeroLiesStillFlagged(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("grep", "no matches found", true)
	// Control: a genuine lie ("found 3" against a no-match output) must
	// still trigger — the fix only excludes the truthful zero case.
	if w := f.checkFalsePremise("The grep found 3 matches with the definition."); w == "" {
		t.Error("genuine non-zero confabulation no longer flagged")
	}
}

// Bug 2: a successful run_command (e.g. ls) must NOT clear an earlier go
// build failure from a different run_command — command outcomes are
// command-granular, not tool-granular.
func TestIssue546BuildErrorSurvivesUnrelatedSuccess(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("run_command", "# build check\n./main.go:12: undefined: Foo\nFAIL\t./... \t[build failed]", true)
	// Unrelated later success of the SAME tool:
	f.recordToolResult("run_command", "main.go\ngo.mod", false)
	if w := f.checkFalsePremise("The build passed and all tests pass now."); w == "" {
		t.Error("build error was cleared by an unrelated ls success — lie undetected")
	}
}

// #331 semantics preserved: a non-verify error (e.g. read failure) is still
// superseded by the same tool's later success.
func TestIssue531SupersedeSemanticsPreserved(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("read_file", "Error: file not found", true)
	f.recordToolResult("read_file", "contents here", false)
	if w := f.checkFalsePremise("I have read the file and the contents of the file show the config."); w != "" {
		t.Errorf("stale read error not superseded (#331 regression): %s", w)
	}
}

// Same-tool supersede still applies when the earlier error is NOT a
// build/verify failure (e.g. a transient network error from run_command).
func TestIssue546NonVerifyCommandErrorStillSuperseded(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("run_command", "Error: connection refused (dial tcp 127.0.0.1:8080)", true)
	f.recordToolResult("run_command", "ok", false)
	if w := f.checkFalsePremise("The command ran successfully and it works now."); w != "" {
		t.Errorf("non-verify command error not superseded by later success: %s", w)
	}
}
