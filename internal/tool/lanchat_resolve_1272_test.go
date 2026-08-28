package tool

// Regression tests for GitHub issue #1272: DM recipient resolution must not
// silently pick an arbitrary peer when nicks collide.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// addPeer registers an online participant with the given identity.
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

// TestResolveRecipientsNickCollisionAmbiguous pins #1272: two distinct nodes
// sharing a nick must produce an ambiguity error naming both node_ids,
// instead of the old silent last-writer-wins delivery.
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

// TestResolveRecipientsExactNodeIDBeatsNickCollision: even with a colliding
// nick present, exact node_id addressing must keep working.
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

// TestResolveRecipientsPrefixMultiMatchAmbiguous pins #1272: a prefix matching
// multiple distinct peers must error (the old code broke on the first hit).
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

	// A unique prefix still resolves.
	resolved, ambiguous = tool.resolveRecipients([]string{"dev-ali"})
	if len(ambiguous) != 0 || len(resolved) != 1 || resolved[0] != "node-a" {
		t.Fatalf("unique prefix must resolve cleanly, resolved=%v ambiguous=%v", resolved, ambiguous)
	}
}

// TestResolveRecipientsUniqueNickStillWorks: single-owner nick keeps the
// normal exact-match fast path.
func TestResolveRecipientsUniqueNickStillWorks(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	addPeer(t, hub, "node-a", "alice")

	resolved, ambiguous := tool.resolveRecipients([]string{"alice"})
	if len(ambiguous) != 0 || len(resolved) != 1 || resolved[0] != "node-a" {
		t.Fatalf("unique nick must resolve, resolved=%v ambiguous=%v", resolved, ambiguous)
	}
}
