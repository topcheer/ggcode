package tui

import (
	"os"
	"strings"
	"testing"
)

// zz_issue2815_test.go guards against the knight spinner-leak recurrence
// (#2815): the run/propose result handlers only cleared loading - the
// spinner tick chain self-renews on IsActive() and never terminated, and
// the four status fields stayed populated after task completion.

// TestIssue2815ResultHandlersStopSpinnerAndClearStatus: source-level
// invariant - both result handlers must call spinner.Stop() and clear the
// four status fields the command side sets (the update_done.go idiom).
func TestIssue2815ResultHandlersStopSpinnerAndClearStatus(t *testing.T) {
	src, err := os.ReadFile("update_knight.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(src)
	for _, fn := range []string{
		"func (m Model) handleKnightTaskResultMsg",
		"func (m Model) handleKnightProjectProposalResultMsg",
	} {
		start := strings.Index(s, fn)
		if start < 0 {
			t.Fatalf("%s not found", fn)
		}
		body := s[start:]
		if end := strings.Index(body, "\n}\n"); end > 0 {
			body = body[:end]
		}
		if !strings.Contains(body, "m.spinner.Stop()") {
			t.Errorf("%s: missing spinner.Stop() - render loop leaks (#2815 recurrence)", fn)
		}
		for _, field := range []string{"m.statusActivity = \"\"", "m.statusToolName = \"\"", "m.statusToolArg = \"\"", "m.statusToolCount = 0"} {
			if !strings.Contains(body, field) {
				t.Errorf("%s: missing %s - status residue", fn, field)
			}
		}
	}
}

// TestIssue2815CommandSideStillStarts: the command side keeps Start+status
// population (guards against accidental over-cleanup).
func TestIssue2815CommandSideStillStarts(t *testing.T) {
	src, err := os.ReadFile("knight_commands.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		`m.spinner.Start("Knight task")`,
		`m.spinner.Start("Knight proposal")`,
		`m.statusActivity = "Knight task"`,
		`m.statusActivity = "Knight proposal"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("command side lost %q", want)
		}
	}
}
