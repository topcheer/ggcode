package agent

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

	if a.consumeRemoteRefusalInhibit() {
		t.Fatal("default consume must be false - local runs record")
	}

	a.InhibitNextRefusalLedgerWrite()
	if !a.consumeRemoteRefusalInhibit() {
		t.Fatal("armed inhibit must consume true (remote run skips ledger)")
	}
	if a.consumeRemoteRefusalInhibit() {
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

	a.InhibitNextRefusalLedgerWrite()
	if !a.consumeRemoteRefusalInhibit() {
		t.Fatal("armed consume must be true")
	}

	if len(a.refusalLedger.data.Entries) != 0 {
		t.Fatalf("remote run must not record, got %d", len(a.refusalLedger.data.Entries))
	}
}
