package tool

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/lanchat"
)

func addTeamRolePeer(t *testing.T, hub *lanchat.Hub, nodeID, team, role string) {
	t.Helper()
	hub.HandlePresence(lanchat.Participant{
		NodeID:   nodeID,
		Online:   true,
		Endpoint: "http://localhost:1",
		Team:     team,
		Role:     role,
	})
}

func TestIssue2142_ArchiveTeamRoleSharedIsAmbiguous(t *testing.T) {
	tool, hub := newTestLanChatTool(t)

	hub.ArchivePeer(lanchat.Participant{NodeID: "node-gone", HumanNick: "alice", Team: "T", Role: "R"})

	addTeamRolePeer(t, hub, "node-b", "T", "R")
	addTeamRolePeer(t, hub, "node-c", "T", "R")

	resolved, ambiguous := tool.resolveRecipients([]string{"node-gone"})
	if len(resolved) != 0 {
		t.Fatalf("shared team|role must not coin-flip deliver, resolved=%v", resolved)
	}
	if len(ambiguous) != 1 {
		t.Fatalf("expected one ambiguity report, got %v", ambiguous)
	}
	if !strings.Contains(ambiguous[0], "node-b") || !strings.Contains(ambiguous[0], "node-c") {
		t.Fatalf("ambiguity must name both candidates, got: %s", ambiguous[0])
	}
}

func TestIssue2142_ArchiveTeamRoleUniqueResolves(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	hub.ArchivePeer(lanchat.Participant{NodeID: "node-gone", HumanNick: "alice", Team: "T", Role: "R"})
	addTeamRolePeer(t, hub, "node-b", "T", "R")

	resolved, ambiguous := tool.resolveRecipients([]string{"node-gone"})
	if len(ambiguous) != 0 {
		t.Fatalf("unique team|role must not be ambiguous, got %v", ambiguous)
	}
	if len(resolved) != 1 || resolved[0] != "node-b" {
		t.Fatalf("unique team|role must resolve to the returning peer, got %v", resolved)
	}
}
