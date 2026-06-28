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
	tool := LanChatTool{Hub: hub}
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
	tool, _ := newTestLanChatTool(t)

	// With no peers, broadcast should succeed (no-op, no HTTP)
	result, err := tool.doSend(context.Background(), "hello", []string{"*"}, false, "")
	if err != nil {
		t.Fatalf("doSend with '*' returned error: %v", err)
	}
	if result.IsError {
		t.Errorf("doSend with '*' should succeed, got error: %s", result.Content)
	}
	// Verify it says "broadcast" in the result
	if !strings.Contains(strings.ToLower(result.Content), "broadcast") {
		t.Errorf("result should mention broadcast, got: %s", result.Content)
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
