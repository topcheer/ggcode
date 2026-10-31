package agent

import (
	"strings"
	"testing"
)

// Regression guard for #4726: the remote-run inhibit marker used to be a
// package-level atomic, so in a process hosting several Agents (TUI main +
// per-task A2A/A CP delegates - internal/a2a/handler.go:532,
// internal/acp/agent_loop.go:66) a concurrent LOCAL run on ANY instance
// consumed the marker meant for the remote one: the local user's refusal was
// silently skipped while the poisoned remote run still wrote to the ledger.
// The flag must live on the Agent instance.
func TestIssue4726_InhibitIsPerAgent(t *testing.T) {
	main := newIssue3460Agent(t)
	peer := newIssue3460Agent(t) // second Agent in the same process

	// TUI arms the marker for a remote run arriving on `main`.
	main.InhibitNextRefusalLedgerWrite()

	// A concurrent local run on the OTHER agent must not steal it.
	if peer.consumeRemoteRefusalInhibit() {
		t.Fatal("peer agent consumed main's inhibit marker - cross-Agent steal (#4726 regression)")
	}

	// The armed agent's own gate still sees it, consume-once.
	if !main.consumeRemoteRefusalInhibit() {
		t.Fatal("armed agent must consume its own marker")
	}
	if main.consumeRemoteRefusalInhibit() {
		t.Fatal("marker must be consume-once")
	}
}

// The integrity consequence, pinned end-to-end: peer's local refusal records
// normally even while main's remote marker is armed.
func TestIssue4726_PeerLocalRefusalStillRecordsWhileMainArmed(t *testing.T) {
	main := newIssue3460Agent(t)
	peer := newIssue3460Agent(t)

	main.InhibitNextRefusalLedgerWrite()                  // remote inbound for main
	peer.recordUserRefusals("never use git push --force") // peer's local run gate: consume()==false -> records

	if n := len(peer.refusalLedger.data.Entries); n != 1 {
		t.Fatalf("peer local refusal must record while main armed, got %d entries", n)
	}
	if s := peer.RefusalSummary(); !strings.Contains(s, "push") {
		t.Fatalf("summary missing refusal: %s", s)
	}
	if len(main.refusalLedger.data.Entries) != 0 {
		t.Fatalf("main must still be clean (remote run not executed yet), got %d", len(main.refusalLedger.data.Entries))
	}
}
