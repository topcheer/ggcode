package lanchat

// #3399/#3402 (A-track TUFU) probes: DM-layer Ed25519 signatures with
// first-sight identity pinning and fingerprint-bound strict-mode
// exemptions. The forged-from_node_id attack from #3399 (enumerate a
// trusted node_id, POST with a spoofed body field) must no longer reach
// the exemption: the claim only exempts when the message carries a valid
// signature from the exact key pinned at endorsement time.

import (
	"path/filepath"
	"testing"
	"time"
)

func issue3402SignedDM(t *testing.T, key *NodeKey, id, fromNode, content string) Message {
	t.Helper()
	m := Message{
		ID:         id,
		FromNodeID: fromNode,
		FromRole:   RoleAgent,
		FromNick:   "PeerAgent",
		ToNodeID:   "self-node",
		ToRole:     RoleAgent,
		Content:    content,
		Timestamp:  time.Now().UnixMilli(),
	}
	signMessage(&m, key)
	return m
}

func issue3402Key(t *testing.T, dir string) *NodeKey {
	t.Helper()
	key, err := LoadOrCreateNodeKey(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateNodeKey: %v", err)
	}
	return key
}

// Full chain: first signed DM pins the sender key (message still queues -
// endorsement not key-bound yet), endorsement binds the pinned
// fingerprint, and the next signed DM auto-approves.
func TestIssue3402_TrustedPeerExemptionRequiresVerifiedSignature(t *testing.T) {
	h := newIssue986Hub(t, "daemon")
	h.SetRequireAgentApproval(true)
	dir := filepath.Join(t.TempDir(), "peer-home")
	key := issue3402Key(t, dir)

	h.HandleIncomingMessage(issue3402SignedDM(t, key, "m1", "friend-node", "first"))
	if pending := h.PendingApprovals(); len(pending) != 1 {
		t.Fatalf("unsigned-trust window: first signed DM before endorsement should queue, got %d pending", len(pending))
	}
	h.SetTrustedPeer("friend-node", true)
	if !h.IsTrustedPeer("friend-node") {
		t.Fatal("SetTrustedPeer(true) should endorse the node")
	}
	h.HandleIncomingMessage(issue3402SignedDM(t, key, "m2", "friend-node", "second"))
	if pending := h.PendingApprovals(); len(pending) != 1 {
		t.Fatalf("verified+endorsed DM should auto-approve (no NEW pending), got %d pending", len(pending))
	}
}

// The #3399 attack: an attacker holding their OWN valid key claims the
// trusted node's node_id. The signature is valid for the attacker key,
// but the pin (friend-node -> real key) conflicts, so the message stays
// unverified and the exemption never fires.
func TestIssue3402_ForgedNodeIDWithValidOwnKeyDoesNotExempt(t *testing.T) {
	h := newIssue986Hub(t, "daemon")
	h.SetRequireAgentApproval(true)
	realDir := filepath.Join(t.TempDir(), "real")
	realKey := issue3402Key(t, realDir)

	// Pin the real key via a first signed DM, then bind trust to it.
	h.HandleIncomingMessage(issue3402SignedDM(t, realKey, "m1", "friend-node", "hello"))
	h.SetTrustedPeer("friend-node", true)

	attackerKey := issue3402Key(t, filepath.Join(t.TempDir(), "attacker"))
	h.HandleIncomingMessage(issue3402SignedDM(t, attackerKey, "m2", "friend-node", "spoofed"))
	if pending := h.PendingApprovals(); len(pending) != 2 {
		t.Fatalf("forged from_node_id with attacker key must queue (pin conflict), got %d pending (want 2: m1 pre-endorse + spoofed)", len(pending))
	}
}

// Unsigned messages from an endorsed node are pre-#3402 peers: routed,
// unverified, never exempt in strict mode.
func TestIssue3402_UnsignedDMFromEndorsedNodeStillQueues(t *testing.T) {
	h := newIssue986Hub(t, "daemon")
	h.SetRequireAgentApproval(true)
	h.SetTrustedPeer("friend-node", true)
	h.HandleIncomingMessage(Message{
		ID:         "m1",
		FromNodeID: "friend-node",
		FromRole:   RoleAgent,
		FromNick:   "PeerAgent",
		ToNodeID:   "self-node",
		ToRole:     RoleAgent,
		Content:    "unsigned",
	})
	if pending := h.PendingApprovals(); len(pending) != 1 {
		t.Fatalf("unsigned DM from endorsed (unpinned) node must queue, got %d pending", len(pending))
	}
}

// Tampering with any canonical field (here: Content) breaks verification.
func TestIssue3402_TamperedContentFailsVerification(t *testing.T) {
	dir := t.TempDir()
	key := issue3402Key(t, dir)
	m := issue3402SignedDM(t, key, "m1", "friend-node", "original")
	m.Content = "tampered"
	if ok, _, err := verifyMessage(&m); ok || err == nil {
		t.Fatalf("tampered content must fail verification, ok=%v err=%v", ok, err)
	}
}
