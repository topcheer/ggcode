package agent

import (
	"strings"
	"testing"
)

func TestIssue4726_InhibitIsPerAgent(t *testing.T) {
	main := newIssue3460Agent(t)
	peer := newIssue3460Agent(t)

	main.InhibitNextRefusalLedgerWrite()

	if peer.consumeRemoteRefusalInhibit() {
		t.Fatal("peer agent consumed main's inhibit marker - cross-Agent steal (#4726 regression)")
	}

	if !main.consumeRemoteRefusalInhibit() {
		t.Fatal("armed agent must consume its own marker")
	}
	if main.consumeRemoteRefusalInhibit() {
		t.Fatal("marker must be consume-once")
	}
}

func TestIssue4726_PeerLocalRefusalStillRecordsWhileMainArmed(t *testing.T) {
	main := newIssue3460Agent(t)
	peer := newIssue3460Agent(t)

	main.InhibitNextRefusalLedgerWrite()
	peer.recordUserRefusals("never use git push --force")

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
