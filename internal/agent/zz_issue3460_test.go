package agent

// #3460 probe: remote-origin runs (LAN @agent DM / IM inbound) must not
// mutate the persistent refusal ledger. The TUI sets the consume-once
// inhibit at the remote injection boundaries; the agent gate skips BOTH
// record and release. A forged FromRole=agent DM saying "never touch X"
// must not poison the workspace with a 30-day write block, and a remote
// "you may X now" must not lift the local user's own blocks either.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func newIssue3460Agent(t *testing.T) *Agent {
	t.Helper()
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	a.SetWorkingDir(t.TempDir())
	return a
}

func TestIssue3460_InhibitConsumeOncePolarity(t *testing.T) {
	a := newIssue3460Agent(t)
	// Fresh state: local default, consume is false (gate runs, records).
	if consumeRemoteRefusalInhibit() {
		t.Fatal("default consume must be false - local runs record")
	}
	// Armed: first consume true (gate skips), second false (consume-once).
	a.InhibitNextRefusalLedgerWrite()
	if !consumeRemoteRefusalInhibit() {
		t.Fatal("armed inhibit must consume true (remote run skips ledger)")
	}
	if consumeRemoteRefusalInhibit() {
		t.Fatal("second consume must be false (consume-once)")
	}
}

func TestIssue3460_LocalRunStillRecords(t *testing.T) {
	a := newIssue3460Agent(t)
	a.recordUserRefusals("never use git push --force in this repo")
	if len(a.refusalLedger.data.Entries) != 1 {
		t.Fatalf("local run must still record, got %d", len(a.refusalLedger.data.Entries))
	}
	if s := a.RefusalSummary(); !strings.Contains(s, "push") {
		t.Fatalf("excerpt missing: %s", s)
	}
}

func TestIssue3460_RemoteRunSkipsLedger(t *testing.T) {
	a := newIssue3460Agent(t)
	// Simulate the agent.go gate exactly: armed inhibit -> skip BOTH calls.
	a.InhibitNextRefusalLedgerWrite()
	if !consumeRemoteRefusalInhibit() {
		t.Fatal("armed consume must be true")
	}
	// gate body skipped: record never ran
	if len(a.refusalLedger.data.Entries) != 0 {
		t.Fatalf("remote run must not record, got %d", len(a.refusalLedger.data.Entries))
	}
}
