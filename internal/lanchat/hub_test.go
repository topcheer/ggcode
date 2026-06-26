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

// ── Team feature tests ──

func TestParseNickRoleTeam(t *testing.T) {
	tests := []struct {
		input string
		nick  string
		role  string
		team  string
	}{
		{"alice", "alice", "developer", "dev-team"},
		{"alice@frontend", "alice", "frontend", "dev-team"},
		{"alice@frontend@platform", "alice", "frontend", "platform"},
		{"alice@@platform", "alice", "developer", "platform"},
		{"  bob @ devops @ sre  ", "bob", "devops", "sre"},
		{"", "", "developer", "dev-team"},
	}
	for _, tc := range tests {
		nick, role, team := ParseNickRoleTeam(tc.input)
		if nick != tc.nick || role != tc.role || team != tc.team {
			t.Errorf("ParseNickRoleTeam(%q) = (%q, %q, %q); want (%q, %q, %q)",
				tc.input, nick, role, team, tc.nick, tc.role, tc.team)
		}
	}
}

func TestParseNickRoleBackwardCompat(t *testing.T) {
	// ParseNickRole should still work as a 2-value return
	nick, role := ParseNickRole("alice@frontend")
	if nick != "alice" || role != "frontend" {
		t.Errorf("ParseNickRole backward compat failed: got (%q, %q)", nick, role)
	}
}

func TestSetNickRoleTeam(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-A", "tui", "http://localhost:11111", "", store, WorkspaceMeta{Workspace: "/tmp"})

	// Initial state: default team
	if hub.Team() != DefaultTeam {
		t.Fatalf("initial team should be %q, got %q", DefaultTeam, hub.Team())
	}

	// Set nick with team
	if err := hub.SetNickRoleTeam("alice", "frontend", "platform"); err != nil {
		t.Fatalf("SetNickRoleTeam: %v", err)
	}

	// Verify in-memory state
	if hub.Role() != "frontend" {
		t.Errorf("role = %q, want 'frontend'", hub.Role())
	}
	if hub.Team() != "platform" {
		t.Errorf("team = %q, want 'platform'", hub.Team())
	}
	if hub.HumanNick() != "alice_frontend" {
		t.Errorf("humanNick = %q, want 'alice_frontend'", hub.HumanNick())
	}

	// Verify persistence
	loadedTeam, err := LoadTeam(tmp)
	if err != nil {
		t.Fatalf("LoadTeam: %v", err)
	}
	if loadedTeam != "platform" {
		t.Errorf("persisted team = %q, want 'platform'", loadedTeam)
	}

	// Verify SelfParticipant includes team
	self := hub.SelfParticipant()
	if self.Team != "platform" {
		t.Errorf("SelfParticipant team = %q, want 'platform'", self.Team)
	}
}

func TestPresencePropagatesTeam(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-A", "tui", "http://localhost:11111", "", store, WorkspaceMeta{Workspace: "/tmp"})

	// Simulate an incoming presence from a peer with a team
	hub.HandlePresence(Participant{
		NodeID:    "node-B",
		HumanNick: "bob_backend",
		AgentNick: "bob_backend_agent",
		Endpoint:  "http://localhost:22222",
		Role:      "backend",
		Team:      "platform",
	})

	hub.mu.RLock()
	peer := hub.peers["node-B"]
	hub.mu.RUnlock()
	if peer == nil {
		t.Fatal("peer not added")
	}
	if peer.Team != "platform" {
		t.Errorf("peer team = %q, want 'platform'", peer.Team)
	}
	if peer.Role != "backend" {
		t.Errorf("peer role = %q, want 'backend'", peer.Role)
	}
}

func TestSetNickRolePreservesTeam(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-A", "tui", "http://localhost:11111", "", store, WorkspaceMeta{Workspace: "/tmp"})

	// Set nick with team first
	hub.SetNickRoleTeam("alice", "frontend", "platform")

	// Now use old SetNickRole API — should preserve team
	hub.SetNickRole("alice", "devops")

	if hub.Team() != "platform" {
		t.Errorf("team should be preserved as 'platform', got %q", hub.Team())
	}
	if hub.Role() != "devops" {
		t.Errorf("role should be 'devops', got %q", hub.Role())
	}
}

func TestHandleNickChangeUpdatesTeam(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-A", "tui", "http://localhost:11111", "", store, WorkspaceMeta{Workspace: "/tmp"})

	// Add a peer
	hub.HandlePresence(Participant{
		NodeID:    "node-B",
		HumanNick: "bob_dev",
		Endpoint:  "http://localhost:22222",
	})

	// Simulate incoming NickChange with team
	hub.HandleNickChange(NickChange{
		NodeID:    "node-B",
		HumanNick: "bob_devops",
		AgentNick: "bob_devops_agent",
		Role:      "devops",
		Team:      "sre",
	})

	hub.mu.RLock()
	peer := hub.peers["node-B"]
	hub.mu.RUnlock()
	if peer.Team != "sre" {
		t.Errorf("peer team should be 'sre' after nick change, got %q", peer.Team)
	}
	if peer.Role != "devops" {
		t.Errorf("peer role should be 'devops', got %q", peer.Role)
	}
}

func TestDefaultTeam(t *testing.T) {
	if DefaultTeam != "dev-team" {
		t.Errorf("DefaultTeam = %q, want 'dev-team'", DefaultTeam)
	}
}

func TestSelfParticipantIncludesWorkspaceAndTeam(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-A", "tui", "http://localhost:11111", "", store, WorkspaceMeta{
		Workspace:   "/home/alice/project",
		ProjectName: "project",
		Languages:   []string{"go", "typescript"},
	})

	hub.SetNickRoleTeam("alice", "frontend", "platform")
	self := hub.SelfParticipant()

	if self.Team != "platform" {
		t.Errorf("SelfParticipant team = %q, want 'platform'", self.Team)
	}
	if self.Workspace != "/home/alice/project" {
		t.Errorf("SelfParticipant workspace = %q, want '/home/alice/project'", self.Workspace)
	}
	if self.Role != "frontend" {
		t.Errorf("SelfParticipant role = %q, want 'frontend'", self.Role)
	}
	if len(self.Languages) != 2 {
		t.Errorf("SelfParticipant languages len = %d, want 2", len(self.Languages))
	}
}

func TestParticipantsIncludeTeam(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-A", "tui", "http://localhost:11111", "", store, WorkspaceMeta{})

	hub.SetNickRoleTeam("alice", "frontend", "platform")
	hub.HandlePresence(Participant{
		NodeID:    "node-B",
		HumanNick: "bob_backend",
		Endpoint:  "http://localhost:22222",
		Role:      "backend",
		Team:      "platform",
	})
	hub.HandlePresence(Participant{
		NodeID:    "node-C",
		HumanNick: "charlie_devops",
		Endpoint:  "http://localhost:33333",
		Role:      "devops",
		Team:      "sre",
	})

	participants := hub.Participants()
	if len(participants) != 3 {
		t.Fatalf("expected 3 participants, got %d", len(participants))
	}

	teamMap := make(map[string]int)
	for _, p := range participants {
		teamMap[p.Team]++
	}
	if teamMap["platform"] != 2 {
		t.Errorf("platform team count = %d, want 2", teamMap["platform"])
	}
	if teamMap["sre"] != 1 {
		t.Errorf("sre team count = %d, want 1", teamMap["sre"])
	}
}

func TestPresencePropagatesTeamViaHandlePresence(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-A", "tui", "http://localhost:11111", "", store, WorkspaceMeta{})

	// Peer initially has no team
	hub.HandlePresence(Participant{
		NodeID:    "node-B",
		HumanNick: "bob_dev",
		Endpoint:  "http://localhost:22222",
		Role:      "developer",
		// Team intentionally omitted
	})

	hub.mu.RLock()
	peer := hub.peers["node-B"]
	hub.mu.RUnlock()
	if peer.Team != "" {
		t.Errorf("peer team should be empty initially, got %q", peer.Team)
	}

	// Peer sends updated presence with team
	hub.HandlePresence(Participant{
		NodeID:    "node-B",
		HumanNick: "bob_dev",
		Endpoint:  "http://localhost:22222",
		Role:      "developer",
		Team:      "platform",
	})

	hub.mu.RLock()
	peer = hub.peers["node-B"]
	hub.mu.RUnlock()
	if peer.Team != "platform" {
		t.Errorf("peer team should be 'platform' after update, got %q", peer.Team)
	}
}

func TestTeamPersistenceAcrossSessionReload(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp)
	hub := NewHub("node-A", "tui", "http://localhost:11111", "", store, WorkspaceMeta{})

	// SetSessionID creates a session-scoped store at <baseDir>/sessions/<sessionID>
	hub.SetSessionID(tmp, "session-1")
	hub.SetNickRoleTeam("alice", "frontend", "platform")

	// Verify persisted to session dir
	sessionDir := filepath.Join(tmp, "sessions", "session-1")
	loadedTeam, err := LoadTeam(sessionDir)
	if err != nil {
		t.Fatalf("LoadTeam: %v", err)
	}
	if loadedTeam != "platform" {
		t.Fatalf("persisted team = %q, want 'platform'", loadedTeam)
	}
	loadedRole, err := LoadRole(sessionDir)
	if err != nil {
		t.Fatalf("LoadRole: %v", err)
	}
	if loadedRole != "frontend" {
		t.Fatalf("persisted role = %q, want 'frontend'", loadedRole)
	}

	// Simulate session reload — SetSessionID should restore from disk
	hub2 := NewHub("node-A", "tui", "http://localhost:11111", "", NewStore(tmp), WorkspaceMeta{})
	hub2.SetSessionID(tmp, "session-1")

	if hub2.Team() != "platform" {
		t.Errorf("after reload, team = %q, want 'platform'", hub2.Team())
	}
	if hub2.Role() != "frontend" {
		t.Errorf("after reload, role = %q, want 'frontend'", hub2.Role())
	}
}

func TestParseNickRoleTeamEmptyParts(t *testing.T) {
	// Empty role between two @ signs should default
	nick, role, team := ParseNickRoleTeam("alice@@platform")
	if nick != "alice" {
		t.Errorf("nick = %q, want 'alice'", nick)
	}
	if role != DefaultRole {
		t.Errorf("role = %q, want %q", role, DefaultRole)
	}
	if team != "platform" {
		t.Errorf("team = %q, want 'platform'", team)
	}

	// Empty team after @
	nick, role, team = ParseNickRoleTeam("alice@frontend@")
	if nick != "alice" || role != "frontend" || team != DefaultTeam {
		t.Errorf("ParseNickRoleTeam('alice@frontend@') = (%q, %q, %q), want (%q, %q, %q)",
			nick, role, team, "alice", "frontend", DefaultTeam)
	}
}
