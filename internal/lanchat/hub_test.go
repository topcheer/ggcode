package lanchat

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRandomNick(t *testing.T) {
	nick := RandomNick()
	if len(nick) < 4 {
		t.Errorf("RandomNick too short: %s", nick)
	}
}

func TestAgentNick(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Alice", "Alice_agent"},
		{"Bob_agent", "Bob_agent"},
		{"", "_agent"},
	}
	for _, tt := range tests {
		got := AgentNick(tt.input)
		if got != tt.want {
			t.Errorf("AgentNick(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStoreAppendAndLoad(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)

	sessionID := "test-session"

	for i := 0; i < 105; i++ {
		msg := Message{
			ID:       "msg-" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Content:  "hello",
			FromRole: RoleHuman,
		}
		if err := store.Append(sessionID, msg); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}

	msgs, err := store.LoadRecent(sessionID, 0)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}

	if len(msgs) != maxHistoryPerSession {
		t.Errorf("LoadRecent returned %d messages, want %d", len(msgs), maxHistoryPerSession)
	}
}

func TestStoreLoadEmpty(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)

	msgs, err := store.LoadRecent("nonexistent", 0)
	if err != nil {
		t.Fatalf("LoadRecent on nonexistent: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("LoadRecent returned %d messages, want 0", len(msgs))
	}
}

func TestLoadSaveNick(t *testing.T) {
	tmp := t.TempDir()

	// No persisted nick yet — returns "" with no error
	nick, err := LoadNick(tmp)
	if err != nil {
		t.Fatalf("LoadNick on nonexistent: %v", err)
	}
	if nick != "" {
		t.Errorf("LoadNick = %q, want empty", nick)
	}

	// Save and reload
	if err := SaveNick(tmp, "MyNick"); err != nil {
		t.Fatalf("SaveNick: %v", err)
	}

	nick, err = LoadNick(tmp)
	if err != nil {
		t.Fatalf("LoadNick: %v", err)
	}
	if nick != "MyNick" {
		t.Errorf("LoadNick = %q, want %q", nick, "MyNick")
	}

	// Verify file path
	if _, err := os.Stat(filepath.Join(tmp, "lanchat-nick")); err != nil {
		t.Errorf("nick file not created: %v", err)
	}
}

func TestHubParticipants(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-a", "tui", "http://localhost:1234", "", store, WorkspaceMeta{Workspace: "/tmp/test"})

	parts := hub.Participants()
	if len(parts) != 1 {
		t.Fatalf("expected 1 participant (self), got %d", len(parts))
	}

	self := parts[0]
	if self.NodeID != "node-a" {
		t.Errorf("self.NodeID = %q, want node-a", self.NodeID)
	}
	if self.Online != true {
		t.Error("self should be online")
	}
	if self.HumanNick == "" {
		t.Error("human nick should not be empty")
	}
}

func TestHubSetNick(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-a", "tui", "http://localhost:1234", "", store, WorkspaceMeta{Workspace: "/tmp/test"})

	origNick := hub.HumanNick()
	if err := hub.SetNickRole("Alice", "frontend"); err != nil {
		t.Fatalf("SetNickRole: %v", err)
	}

	if hub.HumanNick() != "Alice_frontend" {
		t.Errorf("HumanNick = %q, want Alice_frontend", hub.HumanNick())
	}
	if hub.AgentNick() != "Alice_frontend_agent" {
		t.Errorf("AgentNick = %q, want Alice_frontend_agent", hub.AgentNick())
	}

	// Verify persisted
	nick, _ := LoadNick(tmp)
	if nick != "Alice_frontend" {
		t.Errorf("persisted nick = %q, want Alice_frontend", nick)
	}

	// origNick should differ
	if origNick == "Alice" {
		t.Error("random nick should differ from Alice")
	}
}

func TestHubApprovalFlow(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-a", "tui", "http://localhost:1234", "", store, WorkspaceMeta{Workspace: "/tmp/test"})

	// Simulate an incoming @agent message
	msg := Message{
		ID:         "msg-1",
		FromNodeID: "node-b",
		FromRole:   RoleHuman,
		FromNick:   "Bob",
		ToNodeID:   "node-a",
		ToRole:     RoleAgent,
		Content:    "analyze main.go",
	}
	hub.HandleIncomingMessage(msg)

	// Should be in pending approvals
	pending := hub.PendingApprovals()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(pending))
	}

	// Approve it
	approved, err := hub.ApproveMessage("msg-1")
	if err != nil {
		t.Fatalf("ApproveMessage: %v", err)
	}
	if approved.Content != "analyze main.go" {
		t.Errorf("approved content = %q", approved.Content)
	}

	// Should no longer be pending
	pending = hub.PendingApprovals()
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after approval, got %d", len(pending))
	}
}

func TestHubRejectFlow(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-a", "tui", "http://localhost:1234", "", store, WorkspaceMeta{Workspace: "/tmp/test"})

	msg := Message{
		ID:         "msg-2",
		FromNodeID: "node-b",
		FromRole:   RoleHuman,
		FromNick:   "Bob",
		ToNodeID:   "node-a",
		ToRole:     RoleAgent,
		Content:    "do something",
	}
	hub.HandleIncomingMessage(msg)

	if err := hub.RejectMessage("msg-2", "not now"); err != nil {
		t.Fatalf("RejectMessage: %v", err)
	}

	pending := hub.PendingApprovals()
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after reject, got %d", len(pending))
	}
}

func TestMessageIsBroadcast(t *testing.T) {
	tests := []struct {
		msg  Message
		want bool
	}{
		{Message{ToNodeID: ""}, true},
		{Message{ToNodeID: "node-a"}, false},
	}
	for _, tt := range tests {
		if got := tt.msg.IsBroadcast(); got != tt.want {
			t.Errorf("IsBroadcast = %v, want %v", got, tt.want)
		}
	}
}

// TestPresenceNoDuplicateJoinAfterRecovery verifies that when a peer goes
// offline and recovers within peerDeleteAfter, no duplicate "is online"
// notification is fired. The notifiedJoin flag must persist across the
// offline→online transition.
func TestPresenceNoDuplicateJoinAfterRecovery(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-self", "tui", "http://localhost:1234", "", store, WorkspaceMeta{Workspace: "/tmp"})

	joinCount := 0
	hub.SetCallbacks(nil, nil, func(p Participant) {
		joinCount++
	}, nil, nil, nil)

	// Simulate a peer joining via presence
	peer := Participant{
		NodeID:    "node-b",
		HumanNick: "Alice_frontend",
		Endpoint:  "http://localhost:5555",
	}
	hub.HandlePresence(peer)

	// Wait for async callback
	time.Sleep(50 * time.Millisecond)
	if joinCount != 1 {
		t.Fatalf("expected 1 join notification, got %d", joinCount)
	}

	// Mark the peer offline by aging out LastSeen
	hub.mu.Lock()
	if p, ok := hub.peers["node-b"]; ok {
		p.LastSeen = time.Now().Add(-ageOffline - time.Second).Unix()
	}
	hub.mu.Unlock()

	// Call UpdatePeers to trigger offline detection (empty list = no mDNS peers)
	hub.UpdatePeers(nil)

	// The peer should be offline but NOT deleted
	hub.mu.RLock()
	p, exists := hub.peers["node-b"]
	hub.mu.RUnlock()
	if !exists {
		t.Fatal("peer should still exist in map (not deleted)")
	}
	if p.Online {
		t.Error("peer should be marked offline")
	}
	if !p.notifiedJoin {
		t.Error("notifiedJoin should still be true")
	}

	// Now simulate recovery — peer sends presence again
	hub.HandlePresence(peer)
	time.Sleep(50 * time.Millisecond)

	// Should NOT have fired another join notification
	if joinCount != 1 {
		t.Errorf("expected 1 join notification (no duplicate on recovery), got %d", joinCount)
	}

	// Peer should be back online
	hub.mu.RLock()
	p = hub.peers["node-b"]
	hub.mu.RUnlock()
	if !p.Online {
		t.Error("peer should be online after recovery")
	}
	if p.notifiedLeave {
		t.Error("notifiedLeave should be reset to false on recovery")
	}
}

// TestPresenceLeaveNotificationDelayed verifies that the leave notification
// is delayed by offlineNotifyDelay. A peer that goes offline and stays
// offline should only fire leave after the delay period.
func TestPresenceLeaveNotificationDelayed(t *testing.T) {
	// Save and restore constants
	origDelay := offlineNotifyDelay
	origOffline := ageOffline
	offlineNotifyDelay = 200 * time.Millisecond
	ageOffline = 2 * time.Second // long enough that test operations don't cause re-staleness
	defer func() {
		offlineNotifyDelay = origDelay
		ageOffline = origOffline
	}()

	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-self", "tui", "http://localhost:1234", "", store, WorkspaceMeta{Workspace: "/tmp"})

	leaveCount := 0
	hub.SetCallbacks(nil, nil, nil, func(nodeID, humanNick string) {
		leaveCount++
	}, nil, nil)

	// Peer joins
	peer := Participant{
		NodeID:    "node-c",
		HumanNick: "Bob_backend",
		Endpoint:  "http://localhost:6666",
	}
	hub.HandlePresence(peer)
	time.Sleep(20 * time.Millisecond)

	// Age the peer and call UpdatePeers — should mark offline but NOT notify yet
	hub.mu.Lock()
	hub.peers["node-c"].LastSeen = time.Now().Add(-ageOffline - time.Second).Unix()
	hub.mu.Unlock()
	hub.UpdatePeers(nil)
	time.Sleep(20 * time.Millisecond)

	if leaveCount != 0 {
		t.Errorf("leave should be delayed, but got %d notifications", leaveCount)
	}

	// Peer is offline but within the delay window
	hub.mu.RLock()
	p := hub.peers["node-c"]
	hub.mu.RUnlock()
	if p.Online {
		t.Error("peer should be offline")
	}
	if p.notifiedLeave {
		t.Error("leave should not be notified yet (within delay)")
	}

	// Wait for the delay to pass and call UpdatePeers again
	time.Sleep(offlineNotifyDelay + 50*time.Millisecond)
	hub.UpdatePeers(nil)
	time.Sleep(20 * time.Millisecond)

	if leaveCount != 1 {
		t.Errorf("expected 1 leave notification after delay, got %d", leaveCount)
	}
}

// TestPresenceLeaveSuppressedOnQuickRecovery verifies that if a peer
// goes offline and recovers within offlineNotifyDelay, NO leave
// notification is fired at all.
func TestPresenceLeaveSuppressedOnQuickRecovery(t *testing.T) {
	origDelay := offlineNotifyDelay
	origOffline := ageOffline
	offlineNotifyDelay = 200 * time.Millisecond
	ageOffline = 2 * time.Second // long enough that peer doesn't re-stale during test sleeps
	defer func() {
		offlineNotifyDelay = origDelay
		ageOffline = origOffline
	}()

	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-self", "tui", "http://localhost:1234", "", store, WorkspaceMeta{Workspace: "/tmp"})

	leaveCount := 0
	hub.SetCallbacks(nil, nil, nil, func(nodeID, humanNick string) {
		leaveCount++
	}, nil, nil)

	// Peer joins
	peer := Participant{
		NodeID:    "node-d",
		HumanNick: "Carol_devops",
		Endpoint:  "http://localhost:7777",
	}
	hub.HandlePresence(peer)
	time.Sleep(20 * time.Millisecond)

	// Peer goes stale → offline marked but leave not yet notified
	hub.mu.Lock()
	hub.peers["node-d"].LastSeen = time.Now().Add(-ageOffline - time.Second).Unix()
	hub.mu.Unlock()
	hub.UpdatePeers(nil)
	time.Sleep(20 * time.Millisecond)

	// Verify leave was NOT fired yet (within offlineNotifyDelay)
	if leaveCount != 0 {
		t.Fatalf("leave should not fire within delay, got %d", leaveCount)
	}

	// Peer recovers before offlineNotifyDelay expires
	hub.HandlePresence(peer)
	time.Sleep(20 * time.Millisecond)

	// Wait beyond offlineNotifyDelay, then call UpdatePeers.
	// Peer is online (LastSeen fresh from HandlePresence), so no leave.
	time.Sleep(offlineNotifyDelay + 50*time.Millisecond)
	hub.UpdatePeers(nil)
	time.Sleep(20 * time.Millisecond)

	if leaveCount != 0 {
		t.Errorf("expected 0 leave notifications (quick recovery), got %d", leaveCount)
	}
}

// TestPresencePeerDeletedAfterLongAbsence verifies that a peer is only
// deleted from the map after peerDeleteAfter, and that deletion clears
// notifiedNicks so a future re-appearance gets a fresh join notification.
func TestPresencePeerDeletedAfterLongAbsence(t *testing.T) {
	origDelete := peerDeleteAfter
	origOffline := ageOffline
	peerDeleteAfter = 200 * time.Millisecond
	ageOffline = 50 * time.Millisecond
	defer func() {
		peerDeleteAfter = origDelete
		ageOffline = origOffline
	}()

	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-self", "tui", "http://localhost:1234", "", store, WorkspaceMeta{Workspace: "/tmp"})

	joinCount := 0
	hub.SetCallbacks(nil, nil, func(p Participant) {
		joinCount++
	}, nil, nil, nil)

	peer := Participant{
		NodeID:    "node-e",
		HumanNick: "Dave_frontend",
		Endpoint:  "http://localhost:8888",
	}
	hub.HandlePresence(peer)
	time.Sleep(20 * time.Millisecond)
	if joinCount != 1 {
		t.Fatalf("expected 1 join, got %d", joinCount)
	}

	// Age the peer well beyond peerDeleteAfter
	hub.mu.Lock()
	hub.peers["node-e"].LastSeen = time.Now().Add(-peerDeleteAfter - time.Hour).Unix()
	hub.mu.Unlock()
	hub.UpdatePeers(nil)

	// Peer should be deleted
	hub.mu.RLock()
	_, exists := hub.peers["node-e"]
	hub.mu.RUnlock()
	if exists {
		t.Fatal("peer should be deleted after peerDeleteAfter")
	}

	// notifiedNicks should be cleared for this nick
	hub.mu.RLock()
	inNotified := hub.notifiedNicks["Dave_frontend"]
	hub.mu.RUnlock()
	if inNotified {
		t.Error("notifiedNicks should be cleared on deletion")
	}

	// Re-appearance should fire a new join notification
	hub.HandlePresence(peer)
	time.Sleep(20 * time.Millisecond)
	if joinCount != 2 {
		t.Errorf("expected 2 join notifications after delete+reappear, got %d", joinCount)
	}
}

// TestPresenceNoMDNSBasedDeletion verifies that a peer is NOT deleted
// just because mDNS didn't see it in the current discovery results,
// even if it's offline.
func TestPresenceNoMDNSBasedDeletion(t *testing.T) {
	origOffline := ageOffline
	ageOffline = 2 * time.Second
	defer func() { ageOffline = origOffline }()

	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-self", "tui", "http://localhost:1234", "", store, WorkspaceMeta{Workspace: "/tmp"})

	// Peer joins via presence
	hub.HandlePresence(Participant{
		NodeID:    "node-f",
		HumanNick: "Eve_testing",
		Endpoint:  "http://localhost:9999",
	})
	time.Sleep(20 * time.Millisecond)

	// Age the peer so it goes offline
	hub.mu.Lock()
	hub.peers["node-f"].LastSeen = time.Now().Add(-ageOffline - time.Second).Unix()
	hub.mu.Unlock()

	// Call UpdatePeers with an EMPTY list (mDNS doesn't see the peer)
	hub.UpdatePeers(nil)

	// Peer should still exist — deletion is time-based only, not mDNS-based
	hub.mu.RLock()
	_, exists := hub.peers["node-f"]
	hub.mu.RUnlock()
	if !exists {
		t.Fatal("peer should NOT be deleted based on mDNS absence alone")
	}
}

// TestPresenceHeartbeatProbesAllPeers verifies that UpdatePeers probes
// ALL known peers whose LastSeen is stale — even peers that are NOT in
// the current mDNS discovery results. This ensures liveness checking is
// decoupled from mDNS: a peer missed by mDNS but alive on HTTP still
// gets probed and stays online.
func TestPresenceHeartbeatProbesAllPeers(t *testing.T) {
	origHeartbeat := presenceHeartbeat
	presenceHeartbeat = 50 * time.Millisecond
	defer func() { presenceHeartbeat = origHeartbeat }()

	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-self", "tui", "http://localhost:1234", "", store, WorkspaceMeta{Workspace: "/tmp"})

	// Add a peer directly to the hub (simulating one discovered earlier)
	hub.mu.Lock()
	hub.peers["node-g"] = &Participant{
		NodeID:    "node-g",
		HumanNick: "Frank_backend",
		Endpoint:  "http://localhost:11111",
		Online:    true,
		LastSeen:  time.Now().Unix(), // fresh now
	}
	hub.mu.Unlock()

	// Wait until LastSeen is stale beyond presenceHeartbeat
	time.Sleep(presenceHeartbeat + 20*time.Millisecond)

	// Call UpdatePeers with a DIFFERENT peer (node-h), NOT node-g.
	// mDNS "discovers" only node-h, completely missing node-g.
	hub.UpdatePeers([]Participant{
		{NodeID: "node-h", Endpoint: "http://localhost:22222"},
	})

	// node-g should STILL exist in the hub (not deleted)
	hub.mu.RLock()
	g, existsG := hub.peers["node-g"]
	hub.mu.RUnlock()
	if !existsG {
		t.Fatal("node-g should still exist in hub even though mDNS missed it")
	}

	// node-g should still be online (within ageOffline window)
	if !g.Online {
		t.Error("node-g should be online — it was recently added and within ageOffline")
	}

	// node-h should have been added by mDNS discovery
	hub.mu.RLock()
	_, existsH := hub.peers["node-h"]
	hub.mu.RUnlock()
	if !existsH {
		t.Fatal("node-h should have been added by mDNS discovery")
	}
}
