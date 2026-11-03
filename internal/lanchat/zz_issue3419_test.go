package lanchat

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

	replay := m
	replay.ID = "replay-fresh-id"
	if ok, _, _ := verifyMessage(&replay); ok {
		t.Fatal("replaying a captured DM under a new id must fail verification (ID is signed)")
	}
}
