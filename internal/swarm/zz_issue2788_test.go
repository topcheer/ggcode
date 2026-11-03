package swarm

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue2788_CancelledTeammateRejectsDelivery(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	team := m.CreateTeam("team-2788", "leader-1")
	tm, err := m.SpawnTeammate(team.ID, "worker-1", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

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

	tmLive.mu.Lock()
	inboxLen := len(tmLive.Inbox)
	tmLive.mu.Unlock()
	if inboxLen != 0 {
		t.Fatalf("#2788: message landed in dead inbox (len=%d)", inboxLen)
	}
}

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
