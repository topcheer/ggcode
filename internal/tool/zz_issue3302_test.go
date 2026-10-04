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
	// Five variants so the recent-list cap of 3 is discriminating: the
	// newest three must win, the two earliest must not appear.
	for i := 0; i < 5; i++ {
		l.Record("gate", "block", "rm -rf /var"+strings.Repeat("x", i))
	}
	got := l.Escalation()
	if got == "" {
		t.Fatal("escalation must fire at threshold 3")
	}
	// Main sentence: the LAST denied command is named, not the denier.
	if !strings.Contains(got, `"rm -rf /varxxxx" was denied 5 times`) {
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
	// "Recent denied commands" must list the NEWEST three, newest first
	// (the legacy head-scan yielded the earliest matches while the label
	// said Recent). Count must be exactly 3 (cap) out of 5 recorded.
	i0 := strings.Index(got, "Recent denied commands:")
	if i0 < 0 {
		t.Fatalf("recent list missing: %q", got)
	}
	tail := got[i0:]
	if n := strings.Count(tail, "rm -rf /var"); n != 3 {
		t.Fatalf("recent list must hold exactly the cap of 3 of 5 recorded, got %d: %q", n, tail)
	}
	// Disambiguate with closing quotes: "varxxx" is a prefix of
	// "varxxxx", so bare substring indexes collide.
	if !(strings.Index(tail, `varxxxx"`) < strings.Index(tail, `varxxx"`) &&
		strings.Index(tail, `varxxx"`) < strings.Index(tail, `varxx"`) &&
		!strings.Contains(tail, `varx",`) && !strings.HasSuffix(strings.TrimSpace(tail), `"rm -rf /varx"`) &&
		!strings.Contains(tail, `"rm -rf /var"`)) {
		t.Fatalf("recent list must be newest-first with the two oldest excluded, got: %q", tail)
	}
}
