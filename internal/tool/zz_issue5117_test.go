package tool

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// #5117: a DM resolved against a stale node_id (recipient seat restarted,
// hub still holds the offline participant) used to burn a ~10s unicast
// timeout per retry with no signal that the addressing itself was stale.
// The send result must warn up front so callers refresh via action=list
// instead of retrying blindly.
func TestIssue5117StaleRecipientWarnedOnSend(t *testing.T) {
	tool, hub := newTestLanChatToolWithWorkspace(t, "/home/alice/project")

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-alive",
		AgentNick: "alive_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
		Workspace: "/home/alice/project",
	})
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-stale-old",
		AgentNick: "migrated_agent",
		Endpoint:  "http://localhost:3",
		Online:    true,
		Workspace: "/home/alice/project",
	})
	// HandlePresence forces Online=true, so flip via the test-support seam.
	hub.SetPeerOfflineForTest("node-stale-old", time.Now().Add(-2*time.Hour))

	ctx := t.Context()
	r, err := tool.doSend(ctx, "ping", []string{"node-alive"}, false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(r.Content, "OFFLINE") {
		t.Errorf("online-only recipient must not trigger stale warning, got: %s", r.Content)
	}

	r2, err2 := tool.doSend(ctx, "ping", []string{"node-stale-old"}, false, "")
	if err2 != nil {
		t.Fatalf("unexpected error on stale-recipient send: %v", err2)
	}
	if !strings.Contains(r2.Content, "OFFLINE") {
		t.Errorf("send to offline recipient must carry stale warning, got: %s", r2.Content)
	}
	if !strings.Contains(r2.Content, "node-stale-old") {
		t.Errorf("stale warning should name the stale node_id, got: %s", r2.Content)
	}
	if !strings.Contains(r2.Content, "last seen") {
		t.Errorf("stale warning should surface last-seen age, got: %s", r2.Content)
	}
	if !strings.Contains(r2.Content, "action='list'") {
		t.Errorf("stale warning should direct caller to action=list refresh, got: %s", r2.Content)
	}

	// Direct helper-level checks: unknown recipient yields no hint.
	if hint := tool.staleRecipientHint([]string{"node-unknown"}); hint != "" {
		t.Errorf("unresolvable recipient must yield no stale hint, got: %s", hint)
	}
}
