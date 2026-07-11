package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/lanchat"
)

func newTestLanChatTool(t *testing.T) (LanChatTool, *lanchat.Hub) {
	t.Helper()
	tmp := t.TempDir()
	store := lanchat.NewStore(filepath.Join(tmp, "lanchat-store"))
	hub := lanchat.NewHub("node-self", "tui", "http://localhost:1", "", store, lanchat.WorkspaceMeta{
		Workspace: "/tmp/test-project",
	})
	tool := LanChatTool{Hub: hub, rateLimiter: newAgentRateLimiter()}
	return tool, hub
}

func TestLanChatListIncludesTeam(t *testing.T) {
	tool, hub := newTestLanChatTool(t)

	hub.SetNickRoleTeam("alice", "frontend", "platform")
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_backend",
		AgentNick: "bob_backend_agent",
		Endpoint:  "http://localhost:2",
		Role:      "backend",
		Team:      "platform",
		Online:    true,
	})

	result := tool.doList("")
	if result.IsError {
		t.Fatalf("doList returned error: %s", result.Content)
	}

	// doList output format: "Participants (N):\n<json array>"
	// Strip the header line to parse the JSON array
	output := result.Content
	idx := strings.Index(output, "[")
	if idx < 0 {
		t.Fatalf("doList output has no JSON array: %s", output)
	}
	type peerEntry struct {
		Team string `json:"team"`
		Role string `json:"role"`
	}
	var peers []peerEntry
	if err := json.Unmarshal([]byte(output[idx:]), &peers); err != nil {
		t.Fatalf("failed to parse doList JSON: %v\noutput: %s", err, output)
	}

	if len(peers) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(peers))
	}

	teamMap := map[string]int{}
	for _, p := range peers {
		teamMap[p.Team]++
	}
	if teamMap["platform"] != 2 {
		t.Errorf("expected 2 participants in 'platform' team, got %d", teamMap["platform"])
	}
}

func TestLanChatSendStarTriggersBroadcast(t *testing.T) {
	tool, hub := newTestLanChatTool(t)

	// Add an online participant so broadcast has a target
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
	})

	// broadcast should not be rate-limited for human (transport fails in test,
	// but that's expected — we only verify no rate-limit error)
	result, err := tool.doSend(context.Background(), "hello", []string{"*"}, false, "")
	if err != nil {
		t.Fatalf("doSend with '*' returned error: %v", err)
	}
	if result.IsError && strings.Contains(result.Content, "rate-limited") {
		t.Errorf("doSend with '*' should not be rate-limited for human: %s", result.Content)
	}
}

func TestLanChatSendEmptyNodeIDTriggersError(t *testing.T) {
	tool, _ := newTestLanChatTool(t)

	// Empty toNodeID is now an error (recipient required)
	result, err := tool.doSend(context.Background(), "hello", nil, false, "")
	if err != nil {
		t.Fatalf("doSend with nil toNodeIDs returned error: %v", err)
	}
	if !result.IsError {
		t.Errorf("doSend with no recipients should return error")
	}
}

func TestLanChatSendRequiresContent(t *testing.T) {
	tool, _ := newTestLanChatTool(t)

	result, err := tool.doSend(context.Background(), "", []string{"node-x"}, false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("doSend with empty content should return error")
	}
}

func TestLanChatSendTeamUnknownTeam(t *testing.T) {
	tool, hub := newTestLanChatTool(t)

	hub.SetNickRoleTeam("alice", "frontend", "platform")
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_backend",
		Endpoint:  "http://localhost:2",
		Role:      "backend",
		Team:      "platform",
		Online:    true,
	})

	// Try to send to non-existent team
	result, err := tool.doSendTeam(context.Background(), "hello", "nonexistent", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("send to unknown team should return error")
	}
	// Should list valid teams
	if !strings.Contains(result.Content, "platform") {
		t.Errorf("error should list valid teams including 'platform', got: %s", result.Content)
	}
}

func TestLanChatSendTeamRequiresContent(t *testing.T) {
	tool, _ := newTestLanChatTool(t)

	result, err := tool.doSendTeam(context.Background(), "", "platform", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("send_team with empty content should return error")
	}
}

func TestLanChatSendTeamRequiresTeam(t *testing.T) {
	tool, _ := newTestLanChatTool(t)

	result, err := tool.doSendTeam(context.Background(), "hello", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("send_team with empty team should return error")
	}
}

func TestLanChatSendTeamRequiresContentAndTeam(t *testing.T) {
	tool, _ := newTestLanChatTool(t)

	// Both empty
	result, err := tool.doSendTeam(context.Background(), "", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("send_team with empty content and team should return error")
	}
}

func TestLanChatSendTeamSkipsSelf(t *testing.T) {
	tool, hub := newTestLanChatTool(t)

	// Self is in "platform" team
	hub.SetNickRoleTeam("alice", "frontend", "platform")

	// No other peers — should report 0 members even though self is in the team
	result, err := tool.doSendTeam(context.Background(), "hello team", "platform", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should error since no other online team members exist (self is skipped)
	if !result.IsError {
		// Actually with 0 members it might not error but report 0 sent
		// Let me check: doSendTeam checks len(members) == 0 → error
	}
	if !result.IsError {
		t.Errorf("send_team with only self should error (0 members after skipping self)")
	}
}

func TestLanChatSetIdentity(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	hub.SetNickRoleTeam("alice", "frontend", "platform")

	// Change all three
	result, err := tool.doSetIdentity("bob", "backend", "infra")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("doSetIdentity returned error: %s", result.Content)
	}
	if got := hub.HumanNick(); got != "bob_backend" {
		t.Errorf("HumanNick = %q, want %q", got, "bob_backend")
	}
	if got := hub.Role(); got != "backend" {
		t.Errorf("Role = %q, want %q", got, "backend")
	}
	if got := hub.Team(); got != "infra" {
		t.Errorf("Team = %q, want %q", got, "infra")
	}
}

func TestLanChatSetIdentityPartial(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	hub.SetNickRoleTeam("alice", "frontend", "platform")

	// Only change role, keep nick and team
	result, err := tool.doSetIdentity("", "backend", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("doSetIdentity returned error: %s", result.Content)
	}
	if got := hub.HumanNick(); got != "alice_backend" {
		t.Errorf("HumanNick = %q, want %q", got, "alice_backend")
	}
	if got := hub.Role(); got != "backend" {
		t.Errorf("Role = %q, want %q", got, "backend")
	}
	if got := hub.Team(); got != "platform" {
		t.Errorf("Team = %q, want %q", got, "platform")
	}
}

func TestLanChatSetIdentityRequiresOne(t *testing.T) {
	tool, _ := newTestLanChatTool(t)

	result, err := tool.doSetIdentity("", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("doSetIdentity with all empty should return error")
	}
}
