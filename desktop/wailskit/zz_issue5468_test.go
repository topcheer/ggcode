package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/lanchat"
)

func newBridgeForNickTest(dir, workspace string) *ChatBridge {
	store := lanchat.NewStore(dir)
	hub := lanchat.NewHub("node-a", "desktop", "http://localhost:1234", "", store,
		lanchat.WorkspaceMeta{Workspace: workspace})
	return &ChatBridge{lanchatHub: hub}
}

// TestLanChatSetNickWorkspaceTeamFallback pins #5468: a bare nick (no team
// segment) must keep the workspace-derived default team instead of silently
// pinning the fixed "dev-team"; an explicit team segment still wins.
func TestLanChatSetNickWorkspaceTeamFallback(t *testing.T) {
	b := newBridgeForNickTest(t.TempDir(), "/tmp/ws-myproject")

	// Bare nick: team must be the workspace-derived "myproject", not
	// lanchat.DefaultTeam ("dev-team").
	if err := b.LanChatSetNick("alice"); err != nil {
		t.Fatalf("LanChatSetNick: %v", err)
	}
	if got := b.lanchatHub.Team(); got != "ws-myproject" {
		t.Errorf("bare nick: team = %q, want workspace-derived %q (filepath.Base of workspace)", got, "ws-myproject")
	}
}

// TestLanChatSetNickExplicitSegmentWins pins the counterpart: an explicit
// team segment is honored AND its explicit marker is persisted (set_identity
// semantics) - the fix must not regress intentional team choices.
func TestLanChatSetNickExplicitSegmentWins(t *testing.T) {
	dir := t.TempDir()
	b := newBridgeForNickTest(dir, "/tmp/ws-myproject")

	if err := b.LanChatSetNick("bob@dev@customteam"); err != nil {
		t.Fatalf("LanChatSetNick explicit: %v", err)
	}
	if got := b.lanchatHub.Team(); got != "customteam" {
		t.Errorf("explicit segment: team = %q, want %q", got, "customteam")
	}
	if !lanchat.LoadTeamExplicit(dir) {
		t.Errorf("explicit-team marker not persisted after intentional team choice")
	}
}
