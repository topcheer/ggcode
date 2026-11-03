//go:build goolm

package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/session"
)

func TestIssue3918_SetSessionLimitsAutoResetsAgent(t *testing.T) {
	b := &ChatBridge{currentSes: &session.Session{}}
	ag := agent.NewAgent(nil, nil, "sys", 5)
	cm := ag.ContextManager()
	b.mu.Lock()
	b.agent = ag
	b.resolved = &config.ResolvedEndpoint{ContextWindow: 128000, MaxTokens: 4096}
	b.mu.Unlock()

	if err := b.SetSessionLimits(20000, 1000); err != nil {
		t.Fatal(err)
	}
	if cm.ContextWindow() != 20000 || cm.OutputReserve() != 1000 {
		t.Fatalf("explicit limits must apply, got cw=%d mt=%d", cm.ContextWindow(), cm.OutputReserve())
	}

	if err := b.SetSessionLimits(0, 0); err != nil {
		t.Fatal(err)
	}
	if cm.ContextWindow() != 128000 || cm.OutputReserve() != 4096 {
		t.Fatalf("auto (0) must fall back to endpoint resolution, got cw=%d mt=%d", cm.ContextWindow(), cm.OutputReserve())
	}
}
