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
	repl := NewREPL(agent.NewAgent(prov, tool.NewRegistry(), "", 1), nil)
	mgr := subagent.NewManager(config.SubAgentConfig{})
	defer mgr.Shutdown()
	repl.SetSubAgentManager(mgr, prov, tool.NewRegistry())

	id := mgr.Spawn("worker", "task", "task", nil, context.Background())
	if ok := mgr.SetCancel(id, func() {}); !ok {
		t.Fatal("SetCancel returned false")
	}
	time.Sleep(120 * time.Millisecond)

	sendStarted := make(chan struct{}, 1)
	releaseSend := make(chan struct{})
	repl.programSend = func(msg tea.Msg) {
		select {
		case sendStarted <- struct{}{}:
		default:
		}
		<-releaseSend
	}

	done := make(chan struct{})
	go func() {
		mgr.CancelAll()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("CancelAll blocked on program send")
	}

	select {
	case <-sendStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("expected cancel callback to attempt a program send")
	}

	close(releaseSend)
}
