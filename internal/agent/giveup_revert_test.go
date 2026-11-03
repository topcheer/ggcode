package agent

import "testing"

func Test1823GiveupRevert(t *testing.T) {

	g := &giveupRevertState{}
	if g.recordRollback() {
		t.Fatal("rollback alone must not fire")
	}

	g2 := &giveupRevertState{}
	g2.markGiveupText("After investigation, this isn't possible with the current API.")
	if !g2.sawGiveup {
		t.Fatal("give-up text must set the flag")
	}
	if !g2.recordRollback() {
		t.Fatal("give-up + rollback must fire")
	}

	if g2.recordRollback() {
		t.Fatal("must fire at most once per run")
	}

	g3 := &giveupRevertState{}
	g3.markGiveupText("The build passes and tests are green.")
	if g3.sawGiveup {
		t.Fatal("neutral text must not set the give-up flag")
	}

	var nilG *giveupRevertState
	if nilG.recordRollback() {
		t.Fatal("nil state must not fire")
	}

	if got := giveupRevertGuidance(); len(got) < 40 || got[0] != '[' {
		t.Fatalf("guidance text malformed: %q", got[:20])
	}
}
