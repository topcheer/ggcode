package swarm

// #3104 probe: BroadcastToTeam lacked the #2788 SendToTeammate shutdown
// guard - a teammate mid-shutdown (Status flipped OR ctx already cancelled)
// still counted as delivered while its dead inbox silently swallowed the
// message. Construction mirrors zz_issue2788_test.go.

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// injectTeammate inserts a hand-built teammate (no runner goroutine) into
// the live team so the broadcast path can be probed in isolation.
func injectTeammate(m *Manager, teamID string, tm *Teammate) {
	m.mu.Lock()
	team := m.teams[teamID]
	m.mu.Unlock()
	team.mu.Lock()
	team.Teammates[tm.ID] = tm
	team.mu.Unlock()
}

func TestIssue3104BroadcastSkipsShuttingDownTeammate(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	team := m.CreateTeam("t3104a", "leader-1")
	healthy := &Teammate{ID: "tm-ok", Name: "ok", Status: TeammateIdle, Inbox: make(chan MailMessage, 4)}
	dying := &Teammate{ID: "tm-dying", Name: "dying", Status: TeammateShuttingDown, Inbox: make(chan MailMessage, 4)}
	injectTeammate(m, team.ID, healthy)
	injectTeammate(m, team.ID, dying)

	sent := m.BroadcastToTeam(team.ID, MailMessage{})
	if len(sent) != 1 || sent[0] != "tm-ok" {
		t.Fatalf("sent = %v, want [tm-ok] only", sent)
	}
	select {
	case <-dying.Inbox:
		t.Fatal("#3104: shutting-down teammate received a broadcast message")
	default:
	}
	select {
	case <-healthy.Inbox:
	default:
		t.Fatal("healthy teammate did not receive the broadcast")
	}
}

func TestIssue3104BroadcastSkipsCancelledCtxTeammate(t *testing.T) {
	// Root-cancel propagation window: ctx is dead but Status has not been
	// flipped yet - the ctx.Err() probe must still refuse the delivery.
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	team := m.CreateTeam("t3104b", "leader-1")
	ctx, cancel := context.WithCancel(context.Background())
	zombie := &Teammate{ID: "tm-zombie", Name: "zombie", Status: TeammateWorking, Inbox: make(chan MailMessage, 4)}
	live := &Teammate{ID: "tm-live", Name: "live", Status: TeammateWorking, Inbox: make(chan MailMessage, 4)}
	zombie.mu.Lock()
	zombie.ctx = ctx
	zombie.mu.Unlock()
	injectTeammate(m, team.ID, zombie)
	injectTeammate(m, team.ID, live)
	cancel()

	sent := m.BroadcastToTeam(team.ID, MailMessage{})
	if len(sent) != 1 || sent[0] != "tm-live" {
		t.Fatalf("sent = %v, want [tm-live] only", sent)
	}
	select {
	case <-zombie.Inbox:
		t.Fatal("#3104: ctx-cancelled teammate received a broadcast message")
	default:
	}
}

func TestIssue3104BroadcastStillDeliversToWorkingAndIdle(t *testing.T) {
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	team := m.CreateTeam("t3104c", "leader-1")
	idle := &Teammate{ID: "tm-idle", Name: "i", Status: TeammateIdle, Inbox: make(chan MailMessage, 4)}
	working := &Teammate{ID: "tm-work", Name: "w", Status: TeammateWorking, Inbox: make(chan MailMessage, 4)}
	offline := &Teammate{ID: "tm-off", Name: "o", Status: TeammateShuttingDown, Inbox: make(chan MailMessage, 4)}
	injectTeammate(m, team.ID, idle)
	injectTeammate(m, team.ID, working)
	injectTeammate(m, team.ID, offline)

	sent := m.BroadcastToTeam(team.ID, MailMessage{})
	got := map[string]bool{}
	for _, id := range sent {
		got[id] = true
	}
	if len(sent) != 2 || !got["tm-idle"] || !got["tm-work"] {
		t.Fatalf("sent = %v, want exactly [tm-idle tm-work]", sent)
	}
}
