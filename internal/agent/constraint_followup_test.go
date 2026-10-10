package agent

// Tests for the r492 AREX closed loop: the r392 constraint audit forces a
// per-item verdict; this file's follow-up state parses that verdict and
// drives bounded targeted follow-up for [not done] items.

import (
	"strings"
	"testing"
)

func newArmedAudit(items ...string) *constraintAuditState {
	a := newConstraintAuditState()
	a.constraints = items
	a.fired = true
	return a
}

func TestParseUnmetItemsNumberedAndBare(t *testing.T) {
	reply := "here's where things stand:\n" +
		"1. [done] fixed the deadlock with tests\n" +
		"2. [not done] didn't touch the docs\n" +
		"[not done] third item left for later\n"
	unmet, isVerdict := parseUnmetItems(reply)
	if !isVerdict {
		t.Fatal("verdict lines present must be recognized")
	}
	if len(unmet) != 2 {
		t.Fatalf("want 2 unmet, got %d: %+v", len(unmet), unmet)
	}
	if unmet[0].Num != 2 {
		t.Errorf("numbered [not done] must carry its item number, got %d", unmet[0].Num)
	}
	if unmet[1].Num != 0 {
		t.Errorf("bare line must have Num=0, got %d", unmet[1].Num)
	}
	if !strings.Contains(unmet[1].Text, "third item") {
		t.Errorf("bare line text should be retained, got %q", unmet[1].Text)
	}
}

func TestParseUnmetItemsNonVerdictReply(t *testing.T) {
	unmet, isVerdict := parseUnmetItems("I'll keep working on it, the fix is halfway through")
	if isVerdict || unmet != nil {
		t.Fatal("a non-verdict reply must not consume the loop")
	}
	if _, isVerdict := parseUnmetItems(""); isVerdict {
		t.Fatal("empty text is not a verdict reply")
	}
}

func TestParseUnmetItemsAllDone(t *testing.T) {
	unmet, isVerdict := parseUnmetItems("1. [done] a\n2. [done] b")
	if !isVerdict {
		t.Fatal("still a verdict reply")
	}
	if len(unmet) != 0 {
		t.Fatalf("all done must yield zero unmet, got %d", len(unmet))
	}
}

func TestFollowupLoopBoundedAndClosing(t *testing.T) {
	audit := newArmedAudit("fix deadlock", "update docs", "add regression test")
	f := newConstraintFollowupState()
	reply := "1. [done] done\n2. [not done] docs untouched\n3. [not done] no test yet"

	// Round 1: targeted follow-up naming both unmet items by list text.
	msg1 := f.checkAndInject(reply, audit)
	if msg1 == "" {
		t.Fatal("unmet items must trigger follow-up injection")
	}
	for _, want := range []string{"2 item(s)", "item 2: update docs", "item 3: add regression test", "targeted search or tool call"} {
		if !strings.Contains(msg1, want) {
			t.Errorf("round-1 prompt missing %q:\n%s", want, msg1)
		}
	}
	if got := f.currentRound(); got != 1 {
		t.Errorf("round must be 1, got %d", got)
	}

	// Round 2: still unmet -> second targeted injection.
	if msg2 := f.checkAndInject(reply, audit); msg2 == "" {
		t.Fatal("second unmet verdict must inject round 2")
	}
	if got := f.currentRound(); got != 2 {
		t.Errorf("round must be 2, got %d", got)
	}

	// Round 3: budget exhausted -> ONE closing notice, then silence.
	msg3 := f.checkAndInject(reply, audit)
	if msg3 == "" || !strings.Contains(msg3, "state each remaining gap") {
		t.Fatalf("post-budget reply must be the explicit-gap closing notice, got:\n%s", msg3)
	}
	if msg4 := f.checkAndInject(reply, audit); msg4 != "" {
		t.Fatalf("closing notice must fire exactly once, got:\n%s", msg4)
	}
}

func TestFollowupClosesCleanlyWhenAllDone(t *testing.T) {
	audit := newArmedAudit("one requirement", "another requirement")
	f := newConstraintFollowupState()
	if msg := f.checkAndInject("1. [done] yes\n2. [done] also yes", audit); msg != "" {
		t.Fatalf("all-done verdict must not inject, got:\n%s", msg)
	}
}

func TestFollowupInactiveBeforeAuditFires(t *testing.T) {
	audit := newConstraintAuditState() // not fired
	audit.constraints = []string{"a requirement", "b requirement"}
	f := newConstraintFollowupState()
	if msg := f.checkAndInject("1. [not done] nope", audit); msg != "" {
		t.Fatalf("follow-up must stay inert until the audit fired, got:\n%s", msg)
	}
}

func TestFollowupReset(t *testing.T) {
	f := newConstraintFollowupState()
	f.round = 2
	f.finalNotice = true
	f.reset()
	if f.round != 0 || f.finalNotice {
		t.Fatal("reset must clear round and finalNotice")
	}
}
