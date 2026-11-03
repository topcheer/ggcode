package swarm

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

func TestNotifyIdleRunnersDoesNotPanic(t *testing.T) {
	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 2,
		InboxSize:           16,
		TeammateTimeout:     5 * time.Second,
	}
	m := NewManager(cfg, nil,
		func(_ provider.Provider, _ interface{}, _ string, _ int) AgentRunner {
			return &mockAgentRunner{
				runStreamFn: func(ctx context.Context, _ string, _ func(provider.StreamEvent)) error {
					<-ctx.Done()
					return nil
				},
			}
		},
		func(_ []string) interface{} { return nil },
	)
	defer m.Shutdown()

	team := m.CreateTeam("notify-team", "leader-1")
	m.SpawnTeammate(team.ID, "idle-worker", "", nil)
	time.Sleep(50 * time.Millisecond)

	m.NotifyIdleRunners(team.ID)

	m.NotifyIdleRunners("nonexistent")

	time.Sleep(100 * time.Millisecond)
}

func TestBroadcastToTeamDropsOnFullInbox(t *testing.T) {
	cfg := config.SwarmConfig{
		MaxTeammatesPerTeam: 2,
		InboxSize:           2,
		TeammateTimeout:     5 * time.Second,
	}
	m := NewManager(cfg, nil,
		func(_ provider.Provider, _ interface{}, _ string, _ int) AgentRunner {
			return &mockAgentRunner{

				runStreamFn: func(ctx context.Context, _ string, _ func(provider.StreamEvent)) error {
					<-ctx.Done()
					return nil
				},
			}
		},
		func(_ []string) interface{} { return nil },
	)
	defer m.Shutdown()

	team := m.CreateTeam("drop-team", "leader-1")
	tmSnap, _ := m.SpawnTeammate(team.ID, "worker", "", nil)
	time.Sleep(50 * time.Millisecond)

	m.SendToTeammate(team.ID, tmSnap.ID, MailMessage{Type: "task", Content: "block-me"})
	time.Sleep(50 * time.Millisecond)

	m.mu.Lock()
	internalTeam := m.teams[team.ID]
	m.mu.Unlock()
	tm := internalTeam.Teammates[tmSnap.ID]
	tm.Inbox <- MailMessage{Type: "task", Content: "fill-1"}
	tm.Inbox <- MailMessage{Type: "task", Content: "fill-2"}

	sent := m.BroadcastToTeam(team.ID, MailMessage{Type: "message", Content: "should-drop"})
	if len(sent) != 0 {
		t.Errorf("expected 0 sent (inbox full), got %d", len(sent))
	}
}
