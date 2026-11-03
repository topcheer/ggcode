package agent

import "testing"

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

	if w := f.checkFalsePremise("The grep found 3 matches with the definition."); w == "" {
		t.Error("genuine non-zero confabulation no longer flagged")
	}
}

func TestIssue546BuildErrorSurvivesUnrelatedSuccess(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("run_command", "# build check\n./main.go:12: undefined: Foo\nFAIL\t./... \t[build failed]", true)

	f.recordToolResult("run_command", "main.go\ngo.mod", false)
	if w := f.checkFalsePremise("The build passed and all tests pass now."); w == "" {
		t.Error("build error was cleared by an unrelated ls success — lie undetected")
	}
}

func TestIssue531SupersedeSemanticsPreserved(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("read_file", "Error: file not found", true)
	f.recordToolResult("read_file", "contents here", false)
	if w := f.checkFalsePremise("I have read the file and the contents of the file show the config."); w != "" {
		t.Errorf("stale read error not superseded (#331 regression): %s", w)
	}
}

func TestIssue546NonVerifyCommandErrorStillSuperseded(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("run_command", "Error: connection refused (dial tcp 127.0.0.1:8080)", true)
	f.recordToolResult("run_command", "ok", false)
	if w := f.checkFalsePremise("The command ran successfully and it works now."); w != "" {
		t.Errorf("non-verify command error not superseded by later success: %s", w)
	}
}
