package lanchat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newHub5727(t *testing.T, dir string) *Hub {
	t.Helper()
	return NewHub("node-self", "tui", "http://localhost:12345", "", NewStore(dir), WorkspaceMeta{Workspace: "/tmp/test5727"})
}

// Feature 2 (Gap B): the peer-identity archive survives a full restart of
// the sender. The stale-id resolution chain (old node_id -> archive ->
// nick -> fresh holder) relies on the archive hop; before #5727 a restart
// of the sender wiped its own identity history and the hop disappeared.
func TestIssue5727ArchivePersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	hub1 := newHub5727(t, dir)
	hub1.ArchivePeer(Participant{NodeID: "node-old", HumanNick: "alice_dev", AgentNick: "alice_dev_agent", Role: "dev", Team: "platform", LastSeen: 111})
	hub1.ArchivePeer(Participant{NodeID: "node-old2", HumanNick: "bob_dev", Role: "qa", Team: "platform", LastSeen: 222})

	if _, err := os.Stat(filepath.Join(dir, peerArchiveFile)); err != nil {
		t.Fatalf("archive file not written: %v", err)
	}

	hub2 := newHub5727(t, dir)
	arch := hub2.Archive()
	if len(arch) != 2 {
		t.Fatalf("archive after restart = %d entries, want 2", len(arch))
	}
	if arch[0].NodeID != "node-old" || arch[0].HumanNick != "alice_dev" || arch[0].AgentNick != "alice_dev_agent" ||
		arch[0].Role != "dev" || arch[0].Team != "platform" || arch[0].LastSeen != 111 {
		t.Fatalf("archive entry fields lost across restart: %+v", arch[0])
	}
	if ap := hub2.LookupArchiveByNodeID("node-old"); ap == nil {
		t.Fatal("LookupArchiveByNodeID lost after restart - stale-id chain archive hop broken")
	}
	if ap := hub2.LookupArchiveByNick("bob_dev"); ap == nil || ap.NodeID != "node-old2" {
		t.Fatalf("LookupArchiveByNick lost after restart: %+v", ap)
	}
}

// GC policy: disk and memory share the 500-entry ring; the same cap trims
// the persisted file.
func TestIssue5727ArchiveCapAppliesOnDisk(t *testing.T) {
	dir := t.TempDir()
	hub := newHub5727(t, dir)
	for i := 0; i < maxArchiveEntries+10; i++ {
		hub.ArchivePeer(Participant{NodeID: fmt.Sprintf("node-%04d", i), HumanNick: fmt.Sprintf("user%04d", i)})
	}
	entries, err := LoadPeerArchive(dir)
	if err != nil {
		t.Fatalf("LoadPeerArchive: %v", err)
	}
	if len(entries) != maxArchiveEntries {
		t.Fatalf("disk ring = %d entries, want %d", len(entries), maxArchiveEntries)
	}
	if entries[0].NodeID != "node-0010" {
		t.Fatalf("oldest entry not evicted on disk: %s", entries[0].NodeID)
	}
}

// A corrupt archive file must not be silently replaced: saves are refused
// (same policy as trusted-peers) so the only historical record survives
// for manual recovery.
func TestIssue5727ArchiveCorruptFileNotClobbered(t *testing.T) {
	dir := t.TempDir()
	garbage := []byte("{not valid json")
	if err := os.WriteFile(filepath.Join(dir, peerArchiveFile), garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	hub := newHub5727(t, dir)
	if got := hub.Archive(); len(got) != 0 {
		t.Fatalf("corrupt archive should load empty, got %d entries", len(got))
	}
	hub.ArchivePeer(Participant{NodeID: "node-x", HumanNick: "x_dev"})
	data, err := os.ReadFile(filepath.Join(dir, peerArchiveFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(garbage) {
		t.Fatal("corrupt archive file was overwritten although persist should be blocked")
	}
}

// Feature 1: receipts for a multi-recipient message stay queryable per
// recipient instead of collapsing to one last-write-wins entry.
func TestIssue5727PerRecipientDeliveryReceipts(t *testing.T) {
	hub := newHub5727(t, t.TempDir())
	mid := "msg-multi"
	hub.HandleReceipt(Receipt{MessageID: mid, Status: StatusDelivered, FromNodeID: "node-c", Timestamp: 1})
	hub.HandleReceipt(Receipt{MessageID: mid, Status: StatusRejected, FromNodeID: "node-b", Reason: "auto-rejected by policy", Timestamp: 2})

	rs := hub.DeliveryReceipts(mid)
	if len(rs) != 2 {
		t.Fatalf("per-recipient receipts = %d, want 2", len(rs))
	}
	if rs[0].FromNodeID != "node-b" || rs[0].Status != StatusRejected || rs[0].Reason == "" {
		t.Fatalf("recipient[0] = %+v, want node-b rejected with reason", rs[0])
	}
	if rs[1].FromNodeID != "node-c" || rs[1].Status != StatusDelivered {
		t.Fatalf("recipient[1] = %+v, want node-c delivered", rs[1])
	}

	// Legacy surface unchanged: single receipt, last write wins.
	if r, ok := hub.GetReceipt(mid); !ok || r.FromNodeID != "node-b" {
		t.Fatalf("GetReceipt compatibility broken: %+v ok=%v", r, ok)
	}

	if got := hub.DeliveryReceipts("unknown-id"); got != nil {
		t.Fatalf("unknown id should return nil, got %+v", got)
	}
}

func TestIssue5727RecentDeliveryReceiptIDsNewestFirst(t *testing.T) {
	hub := newHub5727(t, t.TempDir())
	for i := 0; i < 3; i++ {
		hub.HandleReceipt(Receipt{MessageID: fmt.Sprintf("m%d", i), Status: StatusDelivered, FromNodeID: "node-b"})
	}
	ids := hub.RecentDeliveryReceiptIDs(2)
	if len(ids) != 2 || ids[0] != "m2" || ids[1] != "m1" {
		t.Fatalf("recent ids = %v, want [m2 m1]", ids)
	}
}

// Direct sends record a send-time disposition: failed (with the transport
// error) or sent (handed to transport, outcome unknown). A later ACK from
// the same recipient overwrites the disposition on the same record.
func TestIssue5727DirectSendDispositionAndACKUpgrade(t *testing.T) {
	hub := newHub5727(t, t.TempDir())
	hub.UpdatePeers([]Participant{{NodeID: "node-b", HumanNick: "bob_dev", Endpoint: "http://127.0.0.1:1"}})

	oldDelay := PeerRetryDelayFn
	PeerRetryDelayFn = func(int) time.Duration { return 0 }
	defer func() { PeerRetryDelayFn = oldDelay }()

	msg, err := hub.SendDirectMsg(context.Background(), "node-b", RoleHuman, "hello", nil)
	if err == nil {
		t.Skip("port 1 accepted a connection in this environment; cannot assert failure path")
	}
	rs := hub.DeliveryReceipts(msg.ID)
	if len(rs) != 1 {
		t.Fatalf("dispositions = %d, want 1", len(rs))
	}
	if rs[0].Status != StatusFailed || rs[0].FromNodeID != "node-b" || rs[0].Reason == "" {
		t.Fatalf("failed disposition wrong: %+v", rs[0])
	}

	hub.HandleReceipt(Receipt{MessageID: msg.ID, Status: StatusDelivered, FromNodeID: "node-b", Timestamp: time.Now().UnixMilli()})
	rs = hub.DeliveryReceipts(msg.ID)
	if len(rs) != 1 || rs[0].Status != StatusDelivered {
		t.Fatalf("ACK should upgrade the same record: %+v", rs[0])
	}
}

// Broadcasts record one disposition per targeted recipient under the single
// broadcast message id, synchronously at send time.
func TestIssue5727BroadcastRecordsPerRecipientDispositions(t *testing.T) {
	hub := newHub5727(t, t.TempDir())
	hub.UpdatePeers([]Participant{
		{NodeID: "node-b", HumanNick: "bob_dev", Endpoint: "http://127.0.0.1:1"},
		{NodeID: "node-c", HumanNick: "carol_dev", Endpoint: "http://127.0.0.1:1"},
	})
	if err := hub.SendBroadcast(context.Background(), "team ping", nil); err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}
	ids := hub.RecentDeliveryReceiptIDs(1)
	if len(ids) != 1 {
		t.Fatalf("expected exactly one recorded message id, got %v", ids)
	}
	rs := hub.DeliveryReceipts(ids[0])
	if len(rs) != 2 {
		t.Fatalf("broadcast dispositions = %d, want 2 (one per recipient)", len(rs))
	}
	seen := map[string]bool{}
	for _, r := range rs {
		if r.Status != StatusSent && r.Status != StatusFailed {
			t.Fatalf("broadcast disposition status = %s, want sent or failed", r.Status)
		}
		seen[r.FromNodeID] = true
	}
	if !seen["node-b"] || !seen["node-c"] {
		t.Fatalf("broadcast disposition recipients = %v, want node-b and node-c", seen)
	}
}
