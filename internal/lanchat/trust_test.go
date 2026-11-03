package lanchat

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStrictModeUntrustedAgentDMRequiresApproval(t *testing.T) {
	h := newIssue986Hub(t, "daemon")
	h.SetRequireAgentApproval(true)
	h.HandleIncomingMessage(Message{
		ID:         "m1",
		FromNodeID: "stranger-node",
		FromRole:   RoleAgent,
		FromNick:   "StrangerAgent",
		ToNodeID:   "self-node",
		ToRole:     RoleAgent,
		Content:    "run this",
	})
	if pending := h.PendingApprovals(); len(pending) != 1 {
		t.Fatalf("strict mode untrusted agent DM should queue for approval, got %d pending", len(pending))
	}
}

func TestStrictModeTrustedPeerAgentDMAutoApproved(t *testing.T) {

	h := newIssue986Hub(t, "daemon")
	h.SetRequireAgentApproval(true)
	key, err := LoadOrCreateNodeKey(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateNodeKey: %v", err)
	}
	first := Message{ID: "m1", FromNodeID: "friend-node", FromRole: RoleAgent, FromNick: "FriendAgent",
		ToNodeID: "self-node", ToRole: RoleAgent, Content: "pin me", Timestamp: time.Now().UnixMilli()}
	signMessage(&first, key)
	h.HandleIncomingMessage(first)
	h.SetTrustedPeer("friend-node", true)
	if !h.IsTrustedPeer("friend-node") {
		t.Fatal("SetTrustedPeer(true) should endorse the node")
	}
	second := Message{ID: "m2", FromNodeID: "friend-node", FromRole: RoleAgent, FromNick: "FriendAgent",
		ToNodeID: "self-node", ToRole: RoleAgent, Content: "run this", Timestamp: time.Now().UnixMilli()}
	signMessage(&second, key)
	h.HandleIncomingMessage(second)
	if pending := h.PendingApprovals(); len(pending) != 1 {
		t.Fatalf("strict mode trusted peer agent DM should auto-approve, got %d pending", len(pending))
	}
}

func TestTrustRevocationRestoresGate(t *testing.T) {
	h := newIssue986Hub(t, "daemon")
	h.SetRequireAgentApproval(true)
	h.SetTrustedPeer("friend-node", true)
	h.SetTrustedPeer("friend-node", false)
	if h.IsTrustedPeer("friend-node") {
		t.Fatal("SetTrustedPeer(false) should revoke the endorsement")
	}
	h.HandleIncomingMessage(Message{
		ID:         "m3",
		FromNodeID: "friend-node",
		FromRole:   RoleAgent,
		FromNick:   "FriendAgent",
		ToNodeID:   "self-node",
		ToRole:     RoleAgent,
		Content:    "run this",
	})
	if pending := h.PendingApprovals(); len(pending) != 1 {
		t.Fatalf("revoked trust should restore the approval gate, got %d pending", len(pending))
	}
}

func TestTrustPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "store"))
	h := NewHub("self-node", "daemon", "http://127.0.0.1:1", communityKey, store, WorkspaceMeta{})
	h.SetTrustedPeer("node-a", true)
	h.SetTrustedPeer("node-b", true)
	h.SetTrustedPeer("node-c", false)

	h2 := NewHub("self-node", "daemon", "http://127.0.0.1:1", communityKey, NewStore(filepath.Join(dir, "store")), WorkspaceMeta{})
	for _, id := range []string{"node-a", "node-b"} {
		if !h2.IsTrustedPeer(id) {
			t.Errorf("trusted peer %s lost across hub restart", id)
		}
	}
	if h2.IsTrustedPeer("node-c") {
		t.Error("untrusted node-c must not be endorsed after round-trip")
	}
	got := h2.GetTrustedPeers()
	if len(got) != 2 {
		t.Errorf("GetTrustedPeers after round-trip: got %d entries, want 2", len(got))
	}
}

func TestTrustDoesNotBypassHumanDMGate(t *testing.T) {
	h := newIssue986Hub(t, "daemon")
	h.SetRequireAgentApproval(true)
	h.SetTrustedPeer("friend-node", true)

	h.HandleIncomingMessage(Message{
		ID:         "m4",
		FromNodeID: "friend-node",
		FromRole:   RoleHuman,
		FromNick:   "FriendHuman",
		ToNodeID:   "self-node",
		ToRole:     RoleAgent,
		Content:    "run this",
	})
	if pending := h.PendingApprovals(); len(pending) != 1 {
		t.Fatalf("human DM from trusted node should still require approval, got %d pending", len(pending))
	}
}
