package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// #3282: bypass-downgraded asks must feed the security ledger. The
// RunCommand.SecLedger field comment promises three Record sources (gate
// block, bypass-downgraded ask, sandbox EPERM) but only two were wired —
// a prober repeatedly triggering downgraded-allows left no trace, missing
// the highest-value sandbox-probing detection surface (#3281 reviewer
// ruling: mandatory follow-up).

// askProbeCmd hits the injection ask-rule ("$(..." command substitution)
// while being harmless when actually executed in the end-to-end test.
const askProbeCmd = "echo $(date)"

func TestIssue3282_BypassAskDowngradeRecordsLedger(t *testing.T) {
	ledger := &SecurityLedger{}
	rc := RunCommand{Policy: newBypassPolicy(), SecLedger: ledger}
	gate := NewCommandGate()

	for i := 1; i <= 3; i++ {
		_, _, blocked := rc.applyCommandGate(gate, askProbeCmd)
		if blocked != "" {
			t.Fatalf("bypass mode must downgrade ask to allow, got blocked: %s", blocked)
		}
		events := ledger.Events()
		if len(events) != i {
			t.Fatalf("after %d downgraded asks: ledger has %d events, want %d", i, len(events), i)
		}
		if e := events[len(events)-1]; e.Denier != "gate" || e.RuleKind != "ask-allowed" {
			t.Fatalf("event %d = denier=%q ruleKind=%q, want gate/ask-allowed", i, e.Denier, e.RuleKind)
		}
	}
	if esc := ledger.Escalation(); !strings.Contains(esc, "SECURITY ESCALATION") {
		t.Fatalf("3 downgraded asks must escalate (probing fingerprint), got: %q", esc)
	}
}

func TestIssue3282_EscalationSurfacesInGatePreWarning(t *testing.T) {
	ledger := &SecurityLedger{}
	rc := RunCommand{Policy: newBypassPolicy(), SecLedger: ledger}
	gate := NewCommandGate()

	var last string
	for i := 0; i < 3; i++ {
		_, pre, blocked := rc.applyCommandGate(gate, askProbeCmd)
		if blocked != "" {
			t.Fatalf("unexpected block: %s", blocked)
		}
		last = pre
	}
	if !strings.Contains(last, "SECURITY ESCALATION") {
		t.Fatalf("3rd downgraded ask must surface the escalation notice via preWarning, got: %q", last)
	}
}

func TestIssue3282_SupervisedAskNotRecordedAsAllowed(t *testing.T) {
	ledger := &SecurityLedger{}
	rc := RunCommand{Policy: newSupervisedPolicy(), SecLedger: ledger}
	gate := NewCommandGate()

	_, _, blocked := rc.applyCommandGate(gate, askProbeCmd)
	if !strings.HasPrefix(blocked, "⚠️") {
		t.Fatalf("supervised mode must return the ask-blocked marker, got: %q", blocked)
	}
	// The supervised path never allowed anything - recording it as
	// "ask-allowed" would fabricate probing telemetry.
	if n := len(ledger.Events()); n != 0 {
		t.Fatalf("supervised ask must not record ask-allowed events, got %d", n)
	}
}

func TestIssue3282_NilLedgerBypassDowngradeSafe(t *testing.T) {
	rc := RunCommand{Policy: newBypassPolicy()} // SecLedger nil = unwired
	gate := NewCommandGate()
	if _, _, blocked := rc.applyCommandGate(gate, askProbeCmd); blocked != "" {
		t.Fatalf("nil ledger must not turn a downgrade into a block: %s", blocked)
	}
}

func TestIssue3282_EndToEndExecuteEscalationVisible(t *testing.T) {
	ledger := &SecurityLedger{}
	rc := RunCommand{Policy: newBypassPolicy(), SecLedger: ledger}
	input, err := json.Marshal(map[string]string{
		"command":     askProbeCmd,
		"description": "issue 3282 probe",
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
		if i == 2 && !strings.Contains(res.Content, "SECURITY ESCALATION") {
			t.Fatalf("3rd execute must carry the escalation notice in its result, got: %.200s", res.Content)
		}
	}
}
