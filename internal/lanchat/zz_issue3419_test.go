package lanchat

// #3419 probe: Message.ID is part of the signed canonical payload, so a
// captured DM replayed under a NEW id (to dodge the seenMsgIDs dedup, or
// after a dedup-table restart) no longer verifies - tampering any field,
// including the id, breaks the signature.

import "testing"

func TestIssue3419_TamperedIDFailsVerification(t *testing.T) {
	key, err := LoadOrCreateNodeKey(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateNodeKey: %v", err)
	}
	m := Message{
		ID: "orig-id", FromNodeID: "friend-node", FromRole: RoleAgent, FromNick: "P",
		ToNodeID: "self-node", ToRole: RoleAgent, Content: "hello", Timestamp: 1700000000000,
	}
	signMessage(&m, key)
	if ok, _, err := verifyMessage(&m); !ok || err != nil {
		t.Fatalf("roundtrip must verify, ok=%v err=%v", ok, err)
	}
	// Replay under a fresh id, every other byte identical: must NOT verify.
	replay := m
	replay.ID = "replay-fresh-id"
	if ok, _, _ := verifyMessage(&replay); ok {
		t.Fatal("replaying a captured DM under a new id must fail verification (ID is signed)")
	}
}
