package lanchat

import (
	"path/filepath"
	"testing"
)

// R32 web-of-trust (PANDA arXiv:2609.38482): trusted peer NODES are exempt
// from the requireAgentApproval gate. Trust is keyed by node_id (stable
// device identity), not nick, and persists in trusted-peers.json.

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
	h.SetTrustedPeer("friend-node", true)
	if !h.IsTrustedPeer("friend-node") {
		t.Fatal("SetTrustedPeer(true) should endorse the node")
	}
	// #3399: the strict-mode exemption is dual-factor - operator trust AND a
	// discovery-layer presence record. A genuinely trusted peer in an
	// established relationship has exchanged presence, so establish it here.
	h.HandlePresence(Participant{NodeID: "friend-node", HumanNick: "Friend", AgentNick: "FriendAgent"})
	h.HandleIncomingMessage(Message{
		ID:         "m2",
		FromNodeID: "friend-node",
		FromRole:   RoleAgent,
		FromNick:   "FriendAgent",
		ToNodeID:   "self-node",
		ToRole:     RoleAgent,
		Content:    "run this",
	})
	if pending := h.PendingApprovals(); len(pending) != 0 {
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
	h.SetTrustedPeer("node-c", false) // never persisted

	// Fresh hub on the same store dir must see the endorsements.
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
	// FromRole is peer-supplied and treated as a routing hint only: even a
	// trusted node claiming the human role must pass the manual gate
	// (#986 discipline - trust exempts agent-to-agent traffic, not humans).
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
