package agent

import "testing"

// #4399-A: bare "still" must not veto a legitimate release. The reaffirm
// pattern's negation group after `still` used to be optional, so
// "ok, you can still modify config.yaml now" matched via the empty group
// (+ \b between space and 'm') and return-0'd release() before any lift
// evaluation. The group is now required ("still don't" / "still not").
func TestIssue4399BareStillNoLongerVetoesLegitRelease(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't modify config.yaml", "nl", refusalKindDirect)
	if n := l.release("ok, you can still modify config.yaml now"); n != 1 {
		t.Fatalf("legitimate release vetoed by bare 'still': released=%d, want 1", n)
	}
}

// #4399-A counterweight: true "still <negation>" continuations still veto.
func TestIssue4399StillNegationStillVetoes(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't modify config.yaml", "nl", refusalKindDirect)
	if n := l.release("ok, still don't modify config.yaml"); n != 0 {
		t.Fatalf("'still don't' reaffirm must veto: released=%d, want 0", n)
	}
	if n := l.release("ok, we still not touching config.yaml, that's right"); n != 0 {
		t.Fatalf("'still not' reaffirm must veto: released=%d, want 0", n)
	}
}

// #4399-B: filler adverbs no longer satisfy the authorize gate. "ok, now
// take a look at config.yaml" is a read-only request, not an authorization;
// it must not silently delete the persisted block. Real authorizations
// (go ahead / modify / touch) still release via the weak-lift path.
func TestIssue4399FillerAdverbsDoNotAuthorize(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't modify config.yaml", "nl", refusalKindDirect)

	if n := l.release("ok, now take a look at config.yaml"); n != 0 {
		t.Fatalf("read-only 'ok, now ...' must not release: released=%d, want 0", n)
	}
	if n := l.release("fine, can you check config.yaml again"); n != 0 {
		t.Fatalf("question-form 'can ... again' must not release: released=%d, want 0", n)
	}

	// Counterweights: real weak-lift releases still work.
	if n := l.release("ok, go ahead and modify config.yaml"); n != 1 {
		t.Fatalf("'ok, go ahead' must release: released=%d, want 1", n)
	}
	l2 := newRefusalTestLedger(t)
	l2.record("don't modify config.yaml", "nl", refusalKindDirect)
	if n := l2.release("ok, you can touch config.yaml now"); n != 1 {
		t.Fatalf("'ok, touch' authorization must release: released=%d, want 1", n)
	}
}

// #4400: start_command shares the command match domain with run_command,
// so the read-only exemption covers it. `tail -f` never mutates the target;
// write evidence (pipe to tee) disqualifies the exemption and falls through
// to normal entry matching (fail-closed), same as run_command.
func TestIssue4400StartCommandReadOnlyExempt(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record(`don't touch build/output`, "nl", refusalKindDirect)

	tail := mustJSON3469(t, map[string]string{"command": "tail -f build/output"})
	if msg := l.checkBlocked("start_command", tail); msg != "" {
		t.Fatalf("read-only start_command 'tail -f' must be exempt, got: %s", msg)
	}

	tee := mustJSON3469(t, map[string]string{"command": "tail -f build/output | tee copy.log"})
	if msg := l.checkBlocked("start_command", tee); msg == "" {
		t.Fatal("write-evidence start_command must fall through to matching and block")
	}

	// run_command behavior unchanged.
	if msg := l.checkBlocked("run_command", tail); msg != "" {
		t.Fatalf("read-only run_command must stay exempt, got: %s", msg)
	}
}
