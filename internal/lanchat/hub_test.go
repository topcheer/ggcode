package lanchat

import (
	"os"
	"path/filepath"
	"testing"
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

	// No persisted nick yet
	_, err := LoadNick(tmp)
	if err == nil {
		t.Error("expected error loading nonexistent nick")
	}

	// Save and reload
	if err := SaveNick(tmp, "MyNick"); err != nil {
		t.Fatalf("SaveNick: %v", err)
	}

	nick, err := LoadNick(tmp)
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
	if err := hub.SetNick("Alice"); err != nil {
		t.Fatalf("SetNick: %v", err)
	}

	if hub.HumanNick() != "Alice" {
		t.Errorf("HumanNick = %q, want Alice", hub.HumanNick())
	}
	if hub.AgentNick() != "Alice_agent" {
		t.Errorf("AgentNick = %q, want Alice_agent", hub.AgentNick())
	}

	// Verify persisted
	nick, _ := LoadNick(tmp)
	if nick != "Alice" {
		t.Errorf("persisted nick = %q, want Alice", nick)
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
