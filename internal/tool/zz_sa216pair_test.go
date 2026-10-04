package tool

// Probes for the sa-216 detection-surface pair:
//   - #3293: RunCommand.Clone() dropped the SecLedger pointer - every
//     sub-agent registry clone (spawn_agent / trial_fork / skill / swarm /
//     desktop) silently lost sandbox-probe detection for its run_command.
//   - #3294: StartCommandTool had no ledger at all, and its three denial
//     paths (block, bypass ask->allow, supervised ask-as-block) recorded
//     nothing - background starts were invisible to the probing
//     fingerprint.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestIssue3293_ClonePreservesSecLedger(t *testing.T) {
	ledger := &SecurityLedger{}
	src := &RunCommand{WorkingDir: "/w", SecLedger: ledger}
	clone, ok := src.Clone().(*RunCommand)
	if !ok {
		t.Fatal("clone type")
	}
	if clone.SecLedger != ledger {
		t.Fatal("Clone() must carry the session SecLedger pointer - a dropped pointer blinds every sub-agent's denial accounting")
	}
	// The cloned tool's records land in the SAME session ledger and its
	// escalation (threshold = 3 same-source denials) fires from the SHARED
	// window regardless of which clone recorded.
	for i := 0; i < 3; i++ {
		clone.SecLedger.Record("gate", "block", "rm -rf /")
	}
	if got := ledger.Escalation(); !strings.Contains(got, `rule "block" via gate`) {
		t.Fatalf("clone record must aggregate into the shared ledger, escalation=%q", got)
	}
}

func TestIssue3294_StartCommandDenialsRecorded(t *testing.T) {
	// Needs a blocked-command shape the gate rejects and an ask shape it
	// flags; use the gate's own classification to stay coupled to real
	// rules rather than hardcoding strings the gate may retune.
	gate := NewCommandGate()

	blockCmd := "rm -rf /"
	if !gate.Check(blockCmd).IsBlocked() {
		t.Skip("gate no longer blocks the sample command; retune probe input")
	}
	if !gate.Check(askProbeCmd).NeedsConfirmation() {
		t.Skip("gate no longer asks for the sample command; retune probe input")
	}

	// Bypass mode: block + ask->allow both recorded (helpers shared with
	// the #3282 probes: newBypassPolicy + askProbeCmd). A real manager is
	// required: the Manager nil-guard precedes the gate.
	ledger := &SecurityLedger{}
	tk := StartCommandTool{Manager: NewCommandJobManager(t.TempDir()), Policy: newBypassPolicy(), SecLedger: ledger}
	if _, err := tk.Execute(context.Background(), mustJSON3294(t, map[string]any{"command": blockCmd})); err != nil {
		t.Fatal(err)
	}
	if _, err := tk.Execute(context.Background(), mustJSON3294(t, map[string]any{"command": askProbeCmd})); err != nil {
		t.Fatal(err)
	}
	events := ledgerEvents3294(ledger)
	kinds := map[string]int{}
	for _, e := range events {
		if e.Denier != "gate" {
			t.Fatalf("start_command denials record under denier=gate, got %q", e.Denier)
		}
		kinds[e.RuleKind]++
	}
	if kinds["block"] != 1 {
		t.Fatalf("gate block via start_command must record kind=block, kinds=%v", kinds)
	}
	if kinds["ask-allowed"] != 1 {
		t.Fatalf("bypass ask->allow via start_command must record kind=ask-allowed, kinds=%v", kinds)
	}

	// Supervised mode: ask becomes a hard denial for background jobs and
	// is recorded under its own kind.
	ledger2 := &SecurityLedger{}
	tk2 := StartCommandTool{Manager: NewCommandJobManager(t.TempDir()), SecLedger: ledger2}
	if _, err := tk2.Execute(context.Background(), mustJSON3294(t, map[string]any{"command": askProbeCmd})); err != nil {
		t.Fatal(err)
	}
	for _, e := range ledgerEvents3294(ledger2) {
		if e.RuleKind != "ask-blocked" {
			t.Fatalf("supervised ask-as-block must record kind=ask-blocked, got %q", e.RuleKind)
		}
	}
}

func TestIssue3294_NilLedgerStillSafe(t *testing.T) {
	tk := StartCommandTool{} // unwired: zero-value tool must not panic
	if _, err := tk.Execute(context.Background(), mustJSON3294(t, map[string]any{"command": "echo hi"})); err != nil {
		t.Fatal(err)
	}
}

func mustJSON3294(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ledgerEvents3294(l *SecurityLedger) []DenialEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]DenialEvent, len(l.events))
	copy(out, l.events)
	return out
}
