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

// askProbeCmd hits a destructive ask-rule (rm -f) while being harmless
// when actually executed in the end-to-end test (temp.txt does not exist,
// -f silences the error). #4273: the probe deliberately avoids the
// shell-substitution rules - PR #4238 downgrades them to warnings, and this
// test must keep triggering a genuine Ask under both rule-table shapes.
const askProbeCmd = "rm -f temp.txt"

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
	// #4273: ask-allowed events are downgraded ALLOWS, not denials - three
	// of them must NOT produce the "was denied / sandbox-probing" notice
	// anymore (old behavior fired at threshold 3 with factually wrong
	// wording; the downgraded-allow notice now needs 10 and is neutral -
	// pinned in zz_issue4273_test.go).
	if esc := ledger.Escalation(); esc != "" {
		t.Fatalf("3 downgraded asks must not escalate (#4273), got: %q", esc)
	}
}

func TestIssue3282_EscalationSurfacesInGatePreWarning(t *testing.T) {
	ledger := &SecurityLedger{}
	rc := RunCommand{Policy: newBypassPolicy(), SecLedger: ledger}
	gate := NewCommandGate()

	var last string
	for i := 0; i < downgradedAllowThreshold; i++ {
		_, pre, blocked := rc.applyCommandGate(gate, askProbeCmd)
		if blocked != "" {
			t.Fatalf("unexpected block: %s", blocked)
		}
		last = pre
	}
	// #4273: the downgraded-allow notice is a SECURITY NOTICE with neutral
	// wording - it must never claim the commands were "denied".
	if !strings.Contains(last, "SECURITY NOTICE") {
		t.Fatalf("downgraded asks must surface the neutral notice via preWarning, got: %q", last)
	}
	if strings.Contains(last, "was denied") {
		t.Fatalf("downgraded-allow notice must not use denial wording (#4273), got: %q", last)
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
	for i := 0; i < downgradedAllowThreshold; i++ {
		res, err := rc.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("execute %d: %v", i+1, err)
		}
		if res.IsError {
			t.Fatalf("bypass downgrade must allow execution: %s", res.Content)
		}
		if i == downgradedAllowThreshold-1 && !strings.Contains(res.Content, "SECURITY NOTICE") {
			t.Fatalf("Nth execute must carry the neutral downgraded-allow notice in its result, got: %.200s", res.Content)
		}
		if i == downgradedAllowThreshold-1 && strings.Contains(res.Content, "was denied") {
			t.Fatalf("downgraded-allow notice must not use denial wording (#4273), got: %.200s", res.Content)
		}
	}
}
