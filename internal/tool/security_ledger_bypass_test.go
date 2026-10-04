package tool

import (
	"strings"
	"testing"
)

// #3282: bypass-downgraded asks must be recorded in the security ledger,
// and repeated downgrades must escalate (the probing fingerprint).
// Unit-level: call applyCommandGate directly so no ask-class command is
// actually executed (real sudo blocks on password prompt on some hosts).
func TestRunCommand_BypassAskDowngrade_RecordsAndEscalates(t *testing.T) {
	rc := RunCommand{Policy: newBypassPolicy(), SecLedger: &SecurityLedger{}}
	gate := NewCommandGate()
	for i := 1; i <= 3; i++ {
		cleaned, preWarn, blocked := rc.applyCommandGate(gate, "sudo echo hello")
		if blocked != "" {
			t.Fatalf("run %d: bypass must downgrade ask to allow, got blocked: %s", i, blocked)
		}
		if cleaned == "" && preWarn == "" && i < 3 {
			// first two runs: no escalation expected
		}
		if i < 3 && strings.Contains(preWarn, "SECURITY ESCALATION") {
			t.Fatalf("run %d: escalation too early", i)
		}
		if i == 3 && !strings.Contains(preWarn, "SECURITY ESCALATION") {
			t.Fatalf("run 3 must carry escalation warning, got: %q", preWarn)
		}
	}
	evs := rc.SecLedger.Events()
	if len(evs) != 3 {
		t.Fatalf("want 3 ask-allowed events, got %d", len(evs))
	}
	for _, e := range evs {
		if e.Denier != "gate" || e.RuleKind != "ask-allowed" {
			t.Fatalf("wrong event: %+v", e)
		}
	}
}

// Supervised (non-bypass) asks are returned as confirmation prompts and
// must NOT be recorded as ask-allowed (they were not allowed).
func TestRunCommand_SupervisedAsk_NotRecorded(t *testing.T) {
	rc := RunCommand{Policy: newSupervisedPolicy(), SecLedger: &SecurityLedger{}}
	gate := NewCommandGate()
	_, _, blocked := rc.applyCommandGate(gate, "sudo echo hello")
	if blocked == "" {
		t.Fatal("supervised mode must return confirmation prompt")
	}
	if n := len(rc.SecLedger.Events()); n != 0 {
		t.Fatalf("supervised ask must not record ask-allowed, got %d events", n)
	}
}
