package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/swarm"
)

// #5851: the sidebar must surface active persistent-team swarm members
// (name / status / current task, one line each), refresh from lifecycle
// events, and appear in sync for teams restored before the TUI program
// ever started.

func newSwarmMgr5851(t *testing.T, boardRoot string, factory swarm.AgentFactory) *swarm.Manager {
	t.Helper()
	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 16,
		InboxSize:           8,
		PollInterval:        100 * time.Millisecond,
	}
	mgr := swarm.NewManager(cfg, nil, factory, nil)
	mgr.BoardRoot = boardRoot
	t.Cleanup(func() { mgr.Shutdown() })
	return mgr
}

func sidebarSwarmSection5851(m Model) string {
	return stripAnsi(m.renderSidebarSwarmSection())
}

func waitUntil5851(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSidebarSwarmSectionHiddenWithoutActiveTeams(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	if got := m.renderSidebarSwarmSection(); got != "" {
		t.Fatalf("expected empty section with nil swarm manager, got %q", got)
	}

	mgr := newSwarmMgr5851(t, t.TempDir(), nil)
	m.swarmMgr = mgr
	if got := m.renderSidebarSwarmSection(); got != "" {
		t.Fatalf("expected empty section with no active teams, got %q", got)
	}
}

func TestSidebarSwarmSectionSurfacesTeamAndMembers(t *testing.T) {
	mgr := newSwarmMgr5851(t, t.TempDir(), nil)
	snap := mgr.CreateTeam("alpha", "leader-1")
	if _, err := mgr.SpawnTeammate(snap.ID, "alice", "red", nil); err != nil {
		t.Fatalf("SpawnTeammate(alice) error = %v", err)
	}
	if _, err := mgr.SpawnTeammate(snap.ID, "bob", "blue", nil); err != nil {
		t.Fatalf("SpawnTeammate(bob) error = %v", err)
	}

	m := newTestModel()
	m.handleResize(120, 40)
	m.swarmMgr = mgr

	section := sidebarSwarmSection5851(m)
	for _, want := range []string{"alpha", "alice", "bob", "[idle]"} {
		if !strings.Contains(section, want) {
			t.Fatalf("sidebar swarm section missing %q; got: %s", want, section)
		}
	}

	// The panel must ride inside the real sidebar, not just its own
	// section renderer.
	sidebar := stripAnsi(m.renderSidebar())
	for _, want := range []string{"Swarm Teams", "alice", "bob"} {
		if !strings.Contains(sidebar, want) {
			t.Fatalf("rendered sidebar missing %q", want)
		}
	}
}

func TestSidebarSwarmMemberRowStatusAndTask(t *testing.T) {
	m := newTestModel()
	m.handleResize(180, 50)

	working := swarm.TeammateStatusInfo{ID: "tm-1", Name: "wally", Status: swarm.TeammateWorking, CurrentTask: "sort mail"}
	row := stripAnsi(m.renderSidebarSwarmMemberRow(working, 34))
	for _, want := range []string{"wally", "[working]", "sort mail"} {
		if !strings.Contains(row, want) {
			t.Fatalf("working member row missing %q: %q", want, row)
		}
	}

	idle := swarm.TeammateStatusInfo{ID: "tm-2", Name: "nina", Status: swarm.TeammateIdle}
	if row := stripAnsi(m.renderSidebarSwarmMemberRow(idle, 34)); !strings.Contains(row, "[idle]") {
		t.Fatalf("idle member row missing [idle]: %q", row)
	}

	stopping := swarm.TeammateStatusInfo{ID: "tm-3", Name: "sam", Status: swarm.TeammateShuttingDown}
	if row := stripAnsi(m.renderSidebarSwarmMemberRow(stopping, 34)); !strings.Contains(row, "[stopping]") {
		t.Fatalf("shutting-down member row missing [stopping]: %q", row)
	}
}

// blockingRunner5851 parks RunStream until the context dies so a claimed
// board task keeps its teammate in the working state for the whole test.
type blockingRunner5851 struct{}

func (blockingRunner5851) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestSidebarSwarmWorkingMemberShowsCurrentTask(t *testing.T) {
	factory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		return blockingRunner5851{}
	}
	mgr := newSwarmMgr5851(t, t.TempDir(), factory)

	snap := mgr.CreateTeam("beta", "leader-2")
	if _, err := mgr.SpawnTeammate(snap.ID, "wally", "green", nil); err != nil {
		t.Fatalf("SpawnTeammate(wally) error = %v", err)
	}
	tmMgr, err := mgr.EnsureTaskManager(snap.ID)
	if err != nil {
		t.Fatalf("EnsureTaskManager error = %v", err)
	}
	tmMgr.Create("sort mail", "", "", nil)

	waitUntil5851(t, "teammate to claim the board task", func() bool {
		for _, ts := range mgr.ListTeamStatuses() {
			for _, tm := range ts.Teammates {
				if tm.Name == "wally" {
					return tm.Status == swarm.TeammateWorking && tm.CurrentTask == "sort mail"
				}
			}
		}
		return false
	})

	m := newTestModel()
	m.handleResize(180, 50)
	m.swarmMgr = mgr

	section := sidebarSwarmSection5851(m)
	for _, want := range []string{"beta", "wally", "[working]", "sort mail"} {
		if !strings.Contains(section, want) {
			t.Fatalf("sidebar swarm section missing %q; got: %s", want, section)
		}
	}
}

func TestSidebarSwarmShutdownRemovesMember(t *testing.T) {
	mgr := newSwarmMgr5851(t, t.TempDir(), nil)
	snap := mgr.CreateTeam("gamma", "leader-3")
	if _, err := mgr.SpawnTeammate(snap.ID, "alice", "red", nil); err != nil {
		t.Fatalf("SpawnTeammate(alice) error = %v", err)
	}
	aliceID := "tm-1"
	if err := mgr.ShutdownTeammate(snap.ID, aliceID); err != nil {
		t.Fatalf("ShutdownTeammate error = %v", err)
	}

	m := newTestModel()
	m.handleResize(120, 40)
	m.swarmMgr = mgr

	section := sidebarSwarmSection5851(m)
	if strings.Contains(section, "alice") {
		t.Fatalf("shut-down teammate still surfaced; got: %s", section)
	}
	if !strings.Contains(section, "gamma") {
		t.Fatalf("team header disappeared after member shutdown; got: %s", section)
	}

	if err := mgr.DeleteTeam(snap.ID); err != nil {
		t.Fatalf("DeleteTeam error = %v", err)
	}
	if got := m.renderSidebarSwarmSection(); got != "" {
		t.Fatalf("expected empty section after team deletion, got %q", stripAnsi(got))
	}
}

func TestSidebarSwarmRestoredTeamAppearsOnFirstRender(t *testing.T) {
	root := t.TempDir()

	// Instance A creates a persistent team and a member, then dies.
	mgrA := newSwarmMgr5851(t, root, nil)
	mgrA.SetOwnerSession("sess-5851")
	snap := mgrA.CreateTeam("coldstart", "leader-4")
	if _, err := mgrA.SpawnTeammate(snap.ID, "carol", "cyan", nil); err != nil {
		t.Fatalf("SpawnTeammate(carol) error = %v", err)
	}
	mgrA.Shutdown()

	// Instance B restores this session's teams. AutoRestore runs in
	// cmd/ggcode BEFORE the swarm events gain a listener, so the panel
	// must reflect the restored team on its very first render.
	mgrB := newSwarmMgr5851(t, root, nil)
	mgrB.SetOwnerSession("sess-5851")
	restored := mgrB.AutoRestoreSessionTeams()
	if len(restored) != 1 {
		t.Fatalf("expected 1 restored team, got %d", len(restored))
	}

	m := newTestModel()
	m.handleResize(120, 40)
	m.swarmMgr = mgrB

	section := sidebarSwarmSection5851(m)
	for _, want := range []string{"coldstart", "carol"} {
		if !strings.Contains(section, want) {
			t.Fatalf("restored team missing %q on first render; got: %s", want, section)
		}
	}
}

func TestSidebarSwarmCapsRowsWithMoreLinks(t *testing.T) {
	mgr := newSwarmMgr5851(t, t.TempDir(), nil)
	snap := mgr.CreateTeam("wide", "leader-5")
	for _, name := range []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7"} {
		if _, err := mgr.SpawnTeammate(snap.ID, name, "", nil); err != nil {
			t.Fatalf("SpawnTeammate(%s) error = %v", name, err)
		}
	}

	m := newTestModel()
	m.handleResize(120, 40)
	m.swarmMgr = mgr

	section := sidebarSwarmSection5851(m)
	for _, want := range []string{"m1", "m5", "+2 more members"} {
		if !strings.Contains(section, want) {
			t.Fatalf("capped section missing %q; got: %s", want, section)
		}
	}
	if strings.Contains(section, "m6") || strings.Contains(section, "m7") {
		t.Fatalf("section shows members beyond the cap; got: %s", section)
	}
}

// TestSidebarSwarmSectionRidesInSidebar is intentionally written against
// pre-existing symbols only (renderSidebar + swarm manager wiring) so it
// can be spliced into the pre-fix parent tree as a behavioral red pin:
// before #5851 the sidebar rendered no swarm content at all.
func TestSidebarSwarmSectionRidesInSidebar(t *testing.T) {
	mgr := newSwarmMgr5851(t, t.TempDir(), nil)
	snap := mgr.CreateTeam("alpha-red", "leader-1")
	if _, err := mgr.SpawnTeammate(snap.ID, "alice", "red", nil); err != nil {
		t.Fatalf("SpawnTeammate(alice) error = %v", err)
	}

	m := newTestModel()
	m.handleResize(120, 40)
	m.swarmMgr = mgr

	sidebar := stripAnsi(m.renderSidebar())
	if !strings.Contains(sidebar, "alice") {
		t.Fatalf("sidebar does not surface swarm teammate alice; got: %s", sidebar)
	}
	if !strings.Contains(sidebar, "alpha-red") {
		t.Fatalf("sidebar does not surface swarm team alpha-red; got: %s", sidebar)
	}
}

func TestSidebarSwarmEventsDriveRerender(t *testing.T) {
	mgr := newSwarmMgr5851(t, t.TempDir(), nil)
	snap := mgr.CreateTeam("delta", "leader-6")
	if _, err := mgr.SpawnTeammate(snap.ID, "dave", "", nil); err != nil {
		t.Fatalf("SpawnTeammate(dave) error = %v", err)
	}

	m := newTestModel()
	m.handleResize(120, 40)
	m.swarmMgr = mgr

	// The lifecycle events the REPL forwards as swarmTunnelEventMsg must
	// pass through the update dispatch without breaking the sidebar.
	updated, _ := m.Update(swarmTunnelEventMsg{Event: swarm.Event{
		Type: "teammate_working", TeamID: snap.ID, TeammateID: "tm-1", TeammateName: "dave",
	}})
	mm, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	section := sidebarSwarmSection5851(mm)
	if !strings.Contains(section, "dave") {
		t.Fatalf("section lost its member after event dispatch; got: %s", section)
	}
}
