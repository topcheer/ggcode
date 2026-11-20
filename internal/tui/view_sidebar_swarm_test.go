package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/swarm"
)

func TestRenderSidebarSwarmSection_NoTeams(t *testing.T) {
	m := Model{}
	if got := m.renderSidebarSwarmSection(); got != "" {
		t.Fatalf("expected empty swarm section when no teams, got %q", got)
	}
}

func TestRenderSidebarSwarmSection_WithTeams(t *testing.T) {
	m := Model{swarmTeams: []swarm.TeamSnapshot{{
		ID:   "team-1",
		Name: "devs",
		Teammates: []swarm.TeammateSnapshot{
			{ID: "tm-1", Name: "alice", Status: swarm.TeammateWorking, CurrentTask: "login"},
			{ID: "tm-2", Name: "bob", Status: swarm.TeammateIdle},
		},
	}}}

	out := m.renderSidebarSwarmSection()
	for _, want := range []string{
		"Swarm Teams",
		"devs (2)",
		"alice",
		"working",
		"— login",
		"bob",
		"idle",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("swarm section missing %q; got:\n%s", want, out)
		}
	}
}

func TestSwarmEventAffectsSidebar(t *testing.T) {
	for _, typ := range []string{
		"team_created", "team_restored", "team_deleted", "team_board_updated",
		"teammate_spawned", "teammate_shutdown", "teammate_working",
		"teammate_idle", "teammate_error",
	} {
		if !swarmEventAffectsSidebar(swarm.Event{Type: typ}) {
			t.Errorf("expected %q to affect the sidebar", typ)
		}
	}
	for _, typ := range []string{
		"teammate_text", "teammate_reasoning", "teammate_tool_call", "teammate_tool_result",
	} {
		if swarmEventAffectsSidebar(swarm.Event{Type: typ}) {
			t.Errorf("expected high-frequency event %q to be skipped", typ)
		}
	}
}

func TestHandleSwarmTunnelEventMsg_RefreshesSnapshot(t *testing.T) {
	mgr := swarm.NewManager(config.SwarmConfig{
		MaxTeammatesPerTeam: 4,
		InboxSize:           8,
	}, "sess-t", nil, nil, nil)
	mgr.BoardRoot = t.TempDir()
	defer mgr.Shutdown()

	team := mgr.CreateTeam("devs", "leader-1")
	if _, err := mgr.SpawnTeammate(team.ID, "alice", "cyan", nil); err != nil {
		t.Fatalf("SpawnTeammate: %v", err)
	}

	m := Model{swarmMgr: mgr}

	// High-frequency stream events must not touch the panel cache.
	streamNext, _ := m.handleSwarmTunnelEventMsg(swarmTunnelEventMsg{Event: swarm.Event{
		Type: "teammate_text", TeamID: team.ID, TeammateID: "tm-1",
	}})
	if len(streamNext.swarmTeams) != 0 {
		t.Fatalf("teammate_text should not refresh the snapshot, got %d team(s)", len(streamNext.swarmTeams))
	}

	// Lifecycle events must refresh it: one team, alice, idle after spawn.
	next, _ := streamNext.handleSwarmTunnelEventMsg(swarmTunnelEventMsg{Event: swarm.Event{
		Type: "teammate_spawned", TeamID: team.ID, TeammateID: "tm-1", TeammateName: "alice",
	}})
	if len(next.swarmTeams) != 1 {
		t.Fatalf("expected 1 team after teammate_spawned, got %d", len(next.swarmTeams))
	}
	got := next.swarmTeams[0]
	if got.Name != "devs" || len(got.Teammates) != 1 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	if tm := got.Teammates[0]; tm.Name != "alice" || tm.Status != swarm.TeammateIdle {
		t.Fatalf("unexpected teammate: %+v", tm)
	}

	// The refreshed cache must be renderable by the sidebar section.
	if out := next.renderSidebarSwarmSection(); !strings.Contains(out, "alice") {
		t.Fatalf("rendered swarm section missing teammate:\n%s", out)
	}
}

func TestRefreshSwarmTeamsSnapshot_NilManager(t *testing.T) {
	m := Model{}
	m.refreshSwarmTeamsSnapshot()
	if len(m.swarmTeams) != 0 {
		t.Fatalf("nil swarmMgr must leave the snapshot empty")
	}
}
