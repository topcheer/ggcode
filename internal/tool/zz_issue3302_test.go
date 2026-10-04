package tool

// #3302 probes: the escalation warning's MAIN sentence must name the real
// last-denied command and read as "command X denied N times by the gate" -
// the old format stuffed the literal "gate/sandbox" into the command slot
// ("the command gate/sandbox has denied..."), leaving the actual commands
// buried in the recent list while the collected lastCmd map went unread.

import (
	"strings"
	"testing"
)

func TestIssue3302_EscalationNamesRealCommand(t *testing.T) {
	l := &SecurityLedger{}
	for i := 0; i < 3; i++ {
		l.Record("gate", "block", "rm -rf /var"+strings.Repeat("x", i))
	}
	got := l.Escalation()
	if got == "" {
		t.Fatal("escalation must fire at threshold 3")
	}
	// Main sentence: the LAST denied command is named, not the denier.
	if !strings.Contains(got, `"rm -rf /varxx" was denied 3 times`) {
		t.Fatalf("main sentence must name the last denied command, got: %q", got)
	}
	// And it must read as the gate doing the denying, never a command
	// named "gate/sandbox".
	if strings.Contains(got, "the command gate/sandbox") {
		t.Fatalf("slot-misfill regression: denier literal in command slot, got: %q", got)
	}
	if !strings.Contains(got, "by the gate (rule \"block\" via gate)") {
		t.Fatalf("denier attribution wording missing, got: %q", got)
	}
}
