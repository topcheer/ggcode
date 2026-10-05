package lanchat

import (
	"testing"
)

// #3399 regression probe: the strict-mode (#3386) trusted-peer auto-approval
// exemption must require TWO factors - the operator's trust endorsement
// (trusted-peers.json) AND a discovery-layer presence record for the same
// node_id in h.peers. A forged from_node_id (self-reported body field, no
// authentication) carries no presence trail and must fail closed into the
// manual approval gate instead of being silently auto-approved.
func newStrictTestHub(t *testing.T) *Hub {
	t.Helper()
	store := NewStore(t.TempDir())
	h := NewHub("node-self", "tui", "http://localhost:0", "", store, WorkspaceMeta{Workspace: "/tmp"})
	h.requireAgentApproval = true
	return h
}

func TestDecideAutoApproval_TrustedPeerWithoutPresenceFailsClosed(t *testing.T) {
	h := newStrictTestHub(t)
	h.mu.Lock()
	h.trustedPeers["node-forged"] = true
	msg := Message{FromNodeID: "node-forged", FromRole: RoleAgent, FromNick: "attacker_agent"}
	approved, rejected := h.decideAutoApprovalLocked(msg)
	h.mu.Unlock()
	if approved {
		t.Fatal("#3399: strict-mode trusted-peer exemption granted to a node with operator trust but NO discovery-presence record - forged from_node_id silently auto-approved")
	}
	if rejected {
		t.Fatal("fail-closed downgrade must land in the manual approval gate, not auto-reject")
	}
}

func TestDecideAutoApproval_TrustedPeerWithPresenceStillExempt(t *testing.T) {
	h := newStrictTestHub(t)
	h.mu.Lock()
	h.trustedPeers["node-real"] = true
	h.peers["node-real"] = &Participant{NodeID: "node-real", Online: true}
	msg := Message{FromNodeID: "node-real", FromRole: RoleAgent, FromNick: "peer_agent"}
	approved, rejected := h.decideAutoApprovalLocked(msg)
	h.mu.Unlock()
	if !approved || rejected {
		t.Fatalf("genuine trusted+presence-verified agent DM must keep the exemption, got approved=%v rejected=%v", approved, rejected)
	}
}

func TestDecideAutoApproval_UntrustedAgentStillManualGate(t *testing.T) {
	h := newStrictTestHub(t)
	h.mu.Lock()
	// Presence-verified but NOT operator-trusted: single factor must not exempt.
	h.peers["node-unknown"] = &Participant{NodeID: "node-unknown", Online: true}
	msg := Message{FromNodeID: "node-unknown", FromRole: RoleAgent, FromNick: "unknown_agent"}
	approved, rejected := h.decideAutoApprovalLocked(msg)
	h.mu.Unlock()
	if approved || rejected {
		t.Fatalf("untrusted agent DM must go through the manual gate in strict mode, got approved=%v rejected=%v", approved, rejected)
	}
}

func TestDecideAutoApproval_NonStrictModeUnchanged(t *testing.T) {
	h := newStrictTestHub(t)
	h.mu.Lock()
	h.requireAgentApproval = false
	msg := Message{FromNodeID: "node-anyone", FromRole: RoleAgent, FromNick: "any_agent"}
	approved, rejected := h.decideAutoApprovalLocked(msg)
	h.mu.Unlock()
	if !approved || rejected {
		t.Fatalf("non-strict mode keeps the legacy LAN default (auto-approve agent DMs), got approved=%v rejected=%v", approved, rejected)
	}
}
