package tool

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/lanchat"
)

func addPeer(t *testing.T, hub *lanchat.Hub, nodeID, nick string) {
	t.Helper()
	hub.HandlePresence(lanchat.Participant{
		NodeID:    nodeID,
		AgentNick: nick,
		Online:    true,
		Endpoint:  "http://localhost:1",
		Workspace: "/tmp/test-project",
	})
}

func TestResolveRecipientsNickCollisionAmbiguous(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	addPeer(t, hub, "node-a", "ggcode")
	addPeer(t, hub, "node-b", "ggcode")

	resolved, ambiguous := tool.resolveRecipients([]string{"ggcode"})
	if len(resolved) != 0 {
		t.Fatalf("#1272: colliding nick must not resolve to an arbitrary peer, got %v", resolved)
	}
	if len(ambiguous) != 1 {
		t.Fatalf("expected exactly one ambiguity report, got %v", ambiguous)
	}
	if !strings.Contains(ambiguous[0], "node-a") || !strings.Contains(ambiguous[0], "node-b") {
		t.Fatalf("ambiguity error must list both candidate node_ids, got: %s", ambiguous[0])
	}
}

func TestResolveRecipientsExactNodeIDBeatsNickCollision(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	addPeer(t, hub, "node-a", "ggcode")
	addPeer(t, hub, "node-b", "ggcode")

	resolved, ambiguous := tool.resolveRecipients([]string{"node-a"})
	if len(ambiguous) != 0 {
		t.Fatalf("node_id addressing must never be ambiguous, got %v", ambiguous)
	}
	if len(resolved) != 1 || resolved[0] != "node-a" {
		t.Fatalf("node-a must resolve exactly, got %v", resolved)
	}
}

func TestResolveRecipientsPrefixMultiMatchAmbiguous(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	addPeer(t, hub, "node-a", "dev-alice")
	addPeer(t, hub, "node-b", "dev-bob")

	resolved, ambiguous := tool.resolveRecipients([]string{"dev"})
	if len(resolved) != 0 {
		t.Fatalf("multi-match prefix must not first-wins, got %v", resolved)
	}
	if len(ambiguous) != 1 || !strings.Contains(ambiguous[0], "node-a") || !strings.Contains(ambiguous[0], "node-b") {
		t.Fatalf("prefix ambiguity must list both candidates, got %v", ambiguous)
	}

	resolved, ambiguous = tool.resolveRecipients([]string{"dev-ali"})
	if len(ambiguous) != 0 || len(resolved) != 1 || resolved[0] != "node-a" {
		t.Fatalf("unique prefix must resolve cleanly, resolved=%v ambiguous=%v", resolved, ambiguous)
	}
}

func TestResolveRecipientsUniqueNickStillWorks(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	addPeer(t, hub, "node-a", "alice")

	resolved, ambiguous := tool.resolveRecipients([]string{"alice"})
	if len(ambiguous) != 0 || len(resolved) != 1 || resolved[0] != "node-a" {
		t.Fatalf("unique nick must resolve, resolved=%v ambiguous=%v", resolved, ambiguous)
	}
}

func TestResolveRecipientsAmbiguousPrefixKeepsExactNodeID(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	addPeer(t, hub, "node-a", "alice")
	addPeer(t, hub, "node-b", "alex")

	resolved, ambiguous := tool.resolveRecipients([]string{"al", "node-a"})
	if len(ambiguous) != 1 {
		t.Fatalf("al must stay ambiguous (alice+alex), got %v", ambiguous)
	}

	if len(resolved) != 1 || resolved[0] != "node-a" {
		t.Fatalf("exact node_id must resolve alongside the ambiguous prefix, got %v", resolved)
	}
}

func TestResolveRecipientsResolvedPeerStillCountsForAmbiguity(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	addPeer(t, hub, "node-a", "alice")
	addPeer(t, hub, "node-b", "alex")

	resolved, ambiguous := tool.resolveRecipients([]string{"node-a", "al"})
	if len(resolved) != 1 || resolved[0] != "node-a" {
		t.Fatalf("node-a must resolve, got %v", resolved)
	}
	if len(ambiguous) != 1 {
		t.Fatalf("al must remain ambiguous even though alice is already resolved, got %v", ambiguous)
	}
}

func TestResolveRecipientsUniquePrefixDedupesResolved(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	addPeer(t, hub, "node-a", "alice")
	addPeer(t, hub, "node-b", "bob")

	resolved, ambiguous := tool.resolveRecipients([]string{"node-a", "alice"})
	if len(ambiguous) != 0 {
		t.Fatalf("no ambiguity expected, got %v", ambiguous)
	}
	if len(resolved) != 1 || resolved[0] != "node-a" {
		t.Fatalf("duplicate reference to the same peer must resolve once, got %v", resolved)
	}
}
