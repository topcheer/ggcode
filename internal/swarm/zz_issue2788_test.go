package swarm

// #2788 regression: SendToTeammate used to drop m.mu right after fetching
// the team pointer, then look up the teammate and push into its Inbox with
// NO mutual exclusion against DeleteTeam/ShutdownTeammate. A send that got
// descheduled in that window delivered into the inbox of an already
// cancelled teammate (runner exited, nobody consumes) and still returned
// nil - a fake delivery. The #2121 SpawnTeammate fix established the
// pattern (hold both locks across check+act); the send path never got it.

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// Guard probe: a teammate that is already cancelled (mid-shutdown, still
// in the team) must NOT accept a delivery - the old code happily pushed
// into the dead inbox and reported success.
func TestIssue2788_CancelledTeammateRejectsDelivery(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	team := m.CreateTeam("team-2788", "leader-1")
	tm, err := m.SpawnTeammate(team.ID, "worker-1", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let the idle loop start

	// Model the mid-ShutdownTeammate state: cancel + ShuttingDown status,
	// teammate not yet removed from the team (removeTeammate pending).
	m.mu.Lock()
	t0 := m.teams[team.ID]
	m.mu.Unlock()
	t0.mu.RLock()
	tmLive := t0.Teammates[tm.ID]
	t0.mu.RUnlock()
	tmLive.mu.Lock()
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	tmLive.ctx = cancelledCtx
	tmLive.Status = TeammateShuttingDown
	tmLive.mu.Unlock()

	err = m.SendToTeammate(team.ID, tm.ID, MailMessage{Content: "work", Type: "task"})
	if err == nil {
		t.Fatal("#2788: delivery to a cancelled/shutting-down teammate must fail - the runner has exited, the message would be silently lost while SendToTeammate reports success")
	}

	// Drain guard: the dead inbox must actually be empty.
	tmLive.mu.Lock()
	inboxLen := len(tmLive.Inbox)
	tmLive.mu.Unlock()
	if inboxLen != 0 {
		t.Fatalf("#2788: message landed in dead inbox (len=%d)", inboxLen)
	}
}

// Guard probe: the normal live-team delivery path must keep working.
func TestIssue2788_LiveTeammateStillReceives(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	team := m.CreateTeam("team-2788b", "leader-1")
	tm, err := m.SpawnTeammate(team.ID, "worker-2", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := m.SendToTeammate(team.ID, tm.ID, MailMessage{Content: "ping", Type: "task"}); err != nil {
		t.Fatalf("live teammate delivery must keep working, got: %v", err)
	}
}

// Guard probe: error paths unchanged - missing team and missing teammate
// still return their distinct errors.
func TestIssue2788_ErrorPathsPreserved(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	team := m.CreateTeam("team-2788c", "leader-1")
	tm, err := m.SpawnTeammate(team.ID, "worker-3", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := m.SendToTeammate("no-such-team", tm.ID, MailMessage{}); err == nil {
		t.Fatal("missing team must error")
	}
	if err := m.SendToTeammate(team.ID, "no-such-teammate", MailMessage{}); err == nil {
		t.Fatal("missing teammate must error")
	}
}
