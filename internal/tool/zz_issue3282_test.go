package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// #4273 rewrite: the #3282 contract (bypass-downgraded asks feed the
// security ledger and escalate as denials) was overturned - a
// downgraded-allow is NOT a denial (the command runs), and feeding it to
// the ledger produced the factually-wrong "was denied N times"
// SECURITY ESCALATION banner on ordinary bypass workflows. Real probing
// evidence remains the BLOCKED path, which records ("gate","block").
// Kept from #3282: supervised asks are not recorded as allowed, and a
// nil ledger must not turn a downgrade into a block.

func TestIssue4273_BypassAskDowngradeNoLedgerRecord(t *testing.T) {
	ledger := &SecurityLedger{}
	rc := RunCommand{Policy: newBypassPolicy(), SecLedger: ledger}
	gate := NewCommandGate()

	for i := 1; i <= 3; i++ {
		_, _, blocked := rc.applyCommandGate(gate, "echo $(date)")
		if blocked != "" {
			t.Fatalf("bypass mode must downgrade ask to allow, got blocked: %s", blocked)
		}
		if n := len(ledger.Events()); n != 0 {
			t.Fatalf("downgraded asks must NOT be recorded as denials (#4273), got %d events after %d asks", n, i)
		}
	}
	if esc := ledger.Escalation(); strings.Contains(esc, "SECURITY ESCALATION") {
		t.Fatalf("no denial events recorded, escalation must stay empty, got: %q", esc)
	}
}

func TestIssue4273_DecodeToShellStillAsks(t *testing.T) {
	// The one real capture the old bare-symbol rules had: decode-then-shell.
	gate := NewCommandGate()
	if r := gate.Check("echo xxx | base64 -d | sh"); r.Behavior != Ask {
		t.Fatalf("decode-then-shell must Ask, got %v (%s)", r.Behavior, r.Reason)
	}
}

func TestIssue4273_SupervisedAskNotRecordedAsAllowed(t *testing.T) {
	ledger := &SecurityLedger{}
	rc := RunCommand{Policy: newSupervisedPolicy(), SecLedger: ledger}
	gate := NewCommandGate()

	// A command that still asks under content rules (decode-then-shell).
	_, _, blocked := rc.applyCommandGate(gate, "echo xxx | base64 -d | sh")
	if !strings.HasPrefix(blocked, "⚠️") {
		t.Fatalf("supervised mode must return the ask-blocked marker, got: %q", blocked)
	}
	// The supervised path never allowed anything - recording it as
	// "ask-allowed" would fabricate probing telemetry.
	if n := len(ledger.Events()); n != 0 {
		t.Fatalf("supervised ask must not record ask-allowed events, got %d", n)
	}
}

func TestIssue4273_NilLedgerBypassDowngradeSafe(t *testing.T) {
	rc := RunCommand{Policy: newBypassPolicy()} // SecLedger nil = unwired
	gate := NewCommandGate()
	if _, _, blocked := rc.applyCommandGate(gate, "echo $(date)"); blocked != "" {
		t.Fatalf("nil ledger must not turn a downgrade into a block: %s", blocked)
	}
}

func TestIssue4273_EndToEndDowngradeNoEscalation(t *testing.T) {
	ledger := &SecurityLedger{}
	rc := RunCommand{Policy: newBypassPolicy(), SecLedger: ledger}
	input, err := json.Marshal(map[string]string{
		"command":     "echo $(date)",
		"description": "issue 4273 downgrade",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		res, err := rc.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("execute %d: %v", i+1, err)
		}
		if res.IsError {
			t.Fatalf("bypass downgrade must allow execution: %s", res.Content)
		}
		if strings.Contains(res.Content, "SECURITY ESCALATION") {
			t.Fatalf("downgraded-allow must not carry a denial escalation banner (#4273), got: %.200s", res.Content)
		}
	}
}
