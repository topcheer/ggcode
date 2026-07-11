package tool

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// newTestLanChatToolWithWorkspace creates a test tool with a custom workspace path.
func newTestLanChatToolWithWorkspace(t *testing.T, workspace string) (LanChatTool, *lanchat.Hub) {
	t.Helper()
	tmp := t.TempDir()
	store := lanchat.NewStore(filepath.Join(tmp, "lanchat-store"))
	hub := lanchat.NewHub("node-self", "tui", "http://localhost:1", "", store, lanchat.WorkspaceMeta{
		Workspace: workspace,
	})
	tool := LanChatTool{Hub: hub, rateLimiter: newAgentRateLimiter()}
	return tool, hub
}

// TestSharedWorkspaceHint_SameWorkspace verifies that a DM to a peer sharing
// the same workspace path includes a shared-workspace collaboration warning.
func TestSharedWorkspaceHint_SameWorkspace(t *testing.T) {
	tool, hub := newTestLanChatToolWithWorkspace(t, "/home/alice/project")

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Role:      "dev",
		Online:    true,
		Workspace: "/home/alice/project", // same workspace!
	})

	ctx := context.Background()
	r, err := tool.doSend(ctx, "can you review my PR?", []string{"node-bob"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The send may fail on network (no real transport in test), but the result
	// content should still contain the workspace hint
	if !strings.Contains(r.Content, "Shared workspace") {
		t.Errorf("expected 'Shared workspace' in result, got: %s", r.Content)
	}
	if !strings.Contains(r.Content, "bob_dev_agent") {
		t.Errorf("expected peer name 'bob_dev_agent' in hint, got: %s", r.Content)
	}
	if !strings.Contains(r.Content, "/home/alice/project") {
		t.Errorf("expected workspace path in hint, got: %s", r.Content)
	}
}

// TestSharedWorkspaceHint_DifferentWorkspace verifies that cross-workspace DMs
// do NOT include the shared-workspace warning.
func TestSharedWorkspaceHint_DifferentWorkspace(t *testing.T) {
	tool, hub := newTestLanChatToolWithWorkspace(t, "/home/alice/project")

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Role:      "dev",
		Online:    true,
		Workspace: "/home/bob/other-project", // different workspace
	})

	ctx := context.Background()
	r, err := tool.doSend(ctx, "quick question", []string{"node-bob"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(r.Content, "Shared workspace") {
		t.Errorf("cross-workspace DM should NOT contain shared-workspace hint, got: %s", r.Content)
	}
}

// TestSharedWorkspaceHint_EmptyWorkspace verifies that peers with unknown
// workspace are treated as cross-workspace (no false positive).
func TestSharedWorkspaceHint_EmptyWorkspace(t *testing.T) {
	tool, hub := newTestLanChatToolWithWorkspace(t, "/home/alice/project")

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
		// Workspace not set (empty)
	})

	ctx := context.Background()
	r, err := tool.doSend(ctx, "hi", []string{"node-bob"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(r.Content, "Shared workspace") {
		t.Errorf("DM to peer with unknown workspace should NOT contain hint, got: %s", r.Content)
	}
}

// TestSharedWorkspaceHint_HumanSendSkipped verifies that human-originated DMs
// do not get the workspace hint (only agent-originated messages need it).
func TestSharedWorkspaceHint_HumanSendSkipped(t *testing.T) {
	tool, hub := newTestLanChatToolWithWorkspace(t, "/home/alice/project")

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
		Workspace: "/home/alice/project", // same workspace
	})

	ctx := context.Background()
	r, err := tool.doSend(ctx, "hello", []string{"node-bob"}, false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(r.Content, "Shared workspace") {
		t.Errorf("human-originated DM should NOT contain agent hint, got: %s", r.Content)
	}
}

// TestSharedWorkspaceHint_MixedRecipients verifies that when sending to
// multiple recipients where only some share the workspace, the hint names
// only the same-workspace peers.
func TestSharedWorkspaceHint_MixedRecipients(t *testing.T) {
	tool, hub := newTestLanChatToolWithWorkspace(t, "/shared/ws")

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		AgentNick: "bob_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
		Workspace: "/shared/ws", // same workspace
	})
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-carol",
		AgentNick: "carol_agent",
		Endpoint:  "http://localhost:3",
		Online:    true,
		Workspace: "/other/ws", // different workspace
	})

	hint := tool.sharedWorkspaceHint([]string{"node-bob", "node-carol"})
	if hint == "" {
		t.Fatal("expected non-empty hint for mixed recipients")
	}
	if !strings.Contains(hint, "bob_agent") {
		t.Errorf("hint should mention bob_agent (same workspace), got: %s", hint)
	}
	if strings.Contains(hint, "carol_agent") {
		t.Errorf("hint should NOT mention carol_agent (different workspace), got: %s", hint)
	}
}

// TestSharedWorkspaceHint_NoSameWorkspacePeers verifies that when none of the
// recipients share the workspace, the hint is empty.
func TestSharedWorkspaceHint_NoSameWorkspacePeers(t *testing.T) {
	tool, hub := newTestLanChatToolWithWorkspace(t, "/my/ws")

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		AgentNick: "bob_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
		Workspace: "/their/ws",
	})

	hint := tool.sharedWorkspaceHint([]string{"node-bob"})
	if hint != "" {
		t.Errorf("expected empty hint for cross-workspace peer, got: %s", hint)
	}
}

// TestSharedWorkspaceHint_BroadcastIncludesHint verifies that broadcast
// includes the shared-workspace hint when same-workspace peers exist.
func TestSharedWorkspaceHint_BroadcastIncludesHint(t *testing.T) {
	tool, hub := newTestLanChatToolWithWorkspace(t, "/shared/ws")

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		AgentNick: "bob_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
		Workspace: "/shared/ws",
	})

	ctx := context.Background()
	r, err := tool.doBroadcastAll(ctx, "team update", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// broadcast will fail on network but the result should still have the hint
	// since we collect participants before sending
	if !strings.Contains(r.Content, "Shared workspace") {
		t.Errorf("broadcast to same-workspace peers should include hint, got: %s", r.Content)
	}
}

// TestSharedWorkspaceHint_SendTeamIncludesHint verifies that send_team
// includes the shared-workspace hint when team members share the workspace.
func TestSharedWorkspaceHint_SendTeamIncludesHint(t *testing.T) {
	tool, hub := newTestLanChatToolWithWorkspace(t, "/shared/ws")

	hub.SetNickRoleTeam("test", "dev", "myteam")
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		AgentNick: "bob_agent",
		Endpoint:  "http://localhost:2",
		Role:      "dev",
		Team:      "myteam",
		Online:    true,
		Workspace: "/shared/ws",
	})

	ctx := context.Background()
	r, err := tool.doSendTeam(ctx, "sync up", "myteam", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(r.Content, "Shared workspace") {
		t.Errorf("send_team to same-workspace peers should include hint, got: %s", r.Content)
	}
}
