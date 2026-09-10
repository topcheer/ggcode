package agent

import "testing"

// #1823 case 2: give-up + rollback pairing fires exactly once, and only
// when BOTH signals appear in the same run.
func Test1823GiveupRevert(t *testing.T) {
	// Rollback without prior give-up text: must not fire.
	g := &giveupRevertState{}
	if g.recordRollback() {
		t.Fatal("rollback alone must not fire")
	}

	// Give-up text, then rollback: fires.
	g2 := &giveupRevertState{}
	g2.markGiveupText("After investigation, this isn't possible with the current API.")
	if !g2.sawGiveup {
		t.Fatal("give-up text must set the flag")
	}
	if !g2.recordRollback() {
		t.Fatal("give-up + rollback must fire")
	}

	// Second rollback does not re-fire (max once per run).
	if g2.recordRollback() {
		t.Fatal("must fire at most once per run")
	}

	// Neutral text: no signal.
	g3 := &giveupRevertState{}
	g3.markGiveupText("The build passes and tests are green.")
	if g3.sawGiveup {
		t.Fatal("neutral text must not set the give-up flag")
	}

	// Nil state is a no-op (bare-Agent safety).
	var nilG *giveupRevertState
	if nilG.recordRollback() {
		t.Fatal("nil state must not fire")
	}

	// Guidance text pins the escalation ask.
	if got := giveupRevertGuidance(); len(got) < 40 || got[0] != '[' {
		t.Fatalf("guidance text malformed: %q", got[:20])
	}
}
