package agent

import (
	"strings"
	"testing"
)

func TestFalsePremise_Reset(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("run_command", "exit code 1: build failed", true)
	f.warningCount = 1
	f.reset()
	if len(f.recentErrors) != 0 {
		t.Errorf("expected recentErrors cleared, got %d", len(f.recentErrors))
	}
	if f.warningCount != 0 {
		t.Errorf("expected warningCount 0, got %d", f.warningCount)
	}
}

func TestFalsePremise_RecordToolResult_IgnoresSuccess(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("run_command", "all good", false)
	if len(f.recentErrors) != 0 {
		t.Errorf("should not record successful results")
	}
}

func TestFalsePremise_RecordToolResult_RecordsError(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("run_command", "build failed", true)
	if len(f.recentErrors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(f.recentErrors))
	}
	if f.recentErrors[0].toolName != "run_command" {
		t.Errorf("expected run_command, got %s", f.recentErrors[0].toolName)
	}
}

func TestFalsePremise_RecordToolResult_BoundedRing(t *testing.T) {
	f := newFalsePremiseState()
	for i := 0; i < 10; i++ {
		f.recordToolResult("run_command", "err", true)
	}
	if len(f.recentErrors) > 5 {
		t.Errorf("ring should be bounded to 5, got %d", len(f.recentErrors))
	}
}

func TestFalsePremise_BuildSuccessContradiction(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("run_command", "exit status 1: compilation failed", true)

	msg := f.checkFalsePremise("The build passed and all tests are green.")
	if msg == "" {
		t.Fatal("expected false premise warning for build success claim after build error")
	}
	if !strings.Contains(msg, "build/test success") {
		t.Errorf("expected build/test success in message, got: %s", msg)
	}
}

func TestFalsePremise_FoundResultsContradiction(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("grep", "no matches found", true)

	msg := f.checkFalsePremise("I found 3 matches for the pattern.")
	if msg == "" {
		t.Fatal("expected false premise warning for found results claim after empty search")
	}
	if !strings.Contains(msg, "search results") {
		t.Errorf("expected search results in message, got: %s", msg)
	}
}

func TestFalsePremise_FileExistsContradiction(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("read_file", "no such file or directory", true)

	msg := f.checkFalsePremise("I read the file and it contains the config.")
	if msg == "" {
		t.Fatal("expected false premise warning for file existence claim after not-found error")
	}
	if !strings.Contains(msg, "file existence") {
		t.Errorf("expected file existence in message, got: %s", msg)
	}
}

func TestFalsePremise_GenericSuccessContradiction(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("edit_file", "anchor not found", true)

	msg := f.checkFalsePremise("The fix is complete and everything works now.")
	if msg == "" {
		t.Fatal("expected false premise warning for generic success claim after tool error")
	}
}

func TestFalsePremise_NoContradictionWhenAcknowledgingError(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("edit_file", "anchor not found", true)

	// Agent acknowledges the error explicitly
	msg := f.checkFalsePremise("The edit failed, so I need to re-read the file.")
	if msg != "" {
		t.Errorf("expected no warning when agent acknowledges error, got: %s", msg)
	}
}

func TestFalsePremise_NoErrorNoWarning(t *testing.T) {
	f := newFalsePremiseState()
	msg := f.checkFalsePremise("The build passed and everything works.")
	if msg != "" {
		t.Errorf("expected no warning when no tool errors tracked, got: %s", msg)
	}
}

func TestFalsePremise_FreshnessExpiry(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("run_command", "build failed", true)

	// Age beyond freshness window
	f.ageErrors()
	f.ageErrors()
	f.ageErrors()

	msg := f.checkFalsePremise("The build passed.")
	if msg != "" {
		t.Errorf("expected no warning after freshness expiry, got: %s", msg)
	}
}

func TestFalsePremise_WarningCap(t *testing.T) {
	f := newFalsePremiseState()
	// Record two errors
	f.recordToolResult("run_command", "build failed", true)
	f.recordToolResult("edit_file", "anchor not found", true)

	// First detection
	msg1 := f.checkFalsePremise("The build passed.")
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second detection (different error, still fresh)
	f.recentErrors[1].turnsAgo = 0 // keep fresh
	msg2 := f.checkFalsePremise("The fix is complete.")
	if msg2 == "" {
		t.Fatal("expected second warning")
	}

	// Third should be capped
	f.recordToolResult("run_command", "another error", true)
	msg3 := f.checkFalsePremise("All done successfully.")
	if msg3 != "" {
		t.Errorf("expected cap at 2 warnings, got third: %s", msg3)
	}
}

func TestFalsePremise_AlreadyMatchedSkips(t *testing.T) {
	f := newFalsePremiseState()
	f.recordToolResult("run_command", "build failed", true)

	msg1 := f.checkFalsePremise("Build passed.")
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Same error, same claim - should not re-fire
	msg2 := f.checkFalsePremise("Build passed again.")
	if msg2 != "" {
		t.Errorf("should not re-fire for already-matched error, got: %s", msg2)
	}
}

func TestIndicatesNoResult(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"no matches found", true},
		{"returned 0 results", true},
		{"did not match anything", true},
		{"found 5 matches", false},
		{"here are the results", false},
	}
	for _, c := range cases {
		if got := indicatesNoResult(c.input); got != c.want {
			t.Errorf("indicatesNoResult(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestIndicatesNotFound(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"no such file or directory", true},
		{"file not found", true},
		{"does not exist", true},
		{"stat: permission denied", true},
		{"file contents here", false},
	}
	for _, c := range cases {
		if got := indicatesNotFound(c.input); got != c.want {
			t.Errorf("indicatesNotFound(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestMatchesBuildSuccessClaim(t *testing.T) {
	positive := []string{
		"the build passed successfully",
		"all tests pass",
		"compiles cleanly",
		"lint passed",
		"tests passed",
	}
	for _, s := range positive {
		if !matchesBuildSuccessClaim(strings.ToLower(s)) {
			t.Errorf("expected match for %q", s)
		}
	}

	negative := []string{
		"the build failed",
		"running the build now",
		"need to compile",
	}
	for _, s := range negative {
		if matchesBuildSuccessClaim(strings.ToLower(s)) {
			t.Errorf("did not expect match for %q", s)
		}
	}
}

func TestMatchesGenericSuccessClaim(t *testing.T) {
	positive := []string{
		"the fix is complete",
		"everything works now",
		"done!",
		"resolved the issue",
		"all set",
	}
	for _, s := range positive {
		if !matchesGenericSuccessClaim(strings.ToLower(s)) {
			t.Errorf("expected match for %q", s)
		}
	}
}
