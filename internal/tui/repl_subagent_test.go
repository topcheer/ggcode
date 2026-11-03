package tui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestSubAgentCancelAllDoesNotBlockOnProgramSend(t *testing.T) {
	prov := &testStreamProvider{}
	repl := NewREPL(agent.NewAgent(prov, tool.NewRegistry(), "", 1), nil, nil)
	mgr := subagent.NewManager(config.SubAgentConfig{})
	defer mgr.Shutdown()
	repl.SetSubAgentManager(mgr, prov, tool.NewRegistry())

	id := mgr.Spawn("worker", "task", "task", nil, context.Background())

	msgs := make(chan tea.Msg, 256)
	repl.programSend = func(msg tea.Msg) {
		select {
		case msgs <- msg:
		default:
		}
	}

	done := make(chan struct{})
	go func() {
		mgr.CancelAll()
		close(done)
	}()

	select {
	case <-done:

	case <-time.After(2 * time.Second):
		t.Fatal("CancelAll blocked on program send")
	}

	select {
	case <-msgs:
	case <-time.After(2 * time.Second):
		t.Fatal("expected cancel callback to attempt a program send")
	}
	_ = id
}
