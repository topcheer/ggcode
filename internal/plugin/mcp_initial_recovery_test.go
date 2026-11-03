package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func newRecoveryTestManager(t *testing.T, cfg config.MCPServerConfig) (*MCPManager, *MCPPlugin) {
	t.Helper()
	m := NewMCPManager(nil, nil, "")
	p := NewMCPPlugin(cfg)
	m.mu.Lock()
	m.plugins = append(m.plugins, p)
	m.mu.Unlock()
	return m, p
}

func TestInitialRecoveryStopsWhenConnected(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.connected = true
	p.mu.Unlock()
	if m.initialRecoveryShouldProbe(p) {
		t.Fatal("connected plugin must not be probed")
	}
}

func TestInitialRecoveryStopsWhenClosed(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	if m.initialRecoveryShouldProbe(p) {
		t.Fatal("closed plugin must not be probed (no resurrection)")
	}
}

func TestInitialRecoveryStopsWhenAwaitingOAuth(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.awaitingOAuth = true
	p.mu.Unlock()
	if m.initialRecoveryShouldProbe(p) {
		t.Fatal("awaitingOAuth plugin must not be probed")
	}
}

func TestInitialRecoveryStopsWhenReplaced(t *testing.T) {
	m, old := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	fresh := NewMCPPlugin(config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	m.mu.Lock()
	m.plugins[0] = fresh
	m.mu.Unlock()
	if m.initialRecoveryShouldProbe(old) {
		t.Fatal("replaced plugin instance must not be probed")
	}
	if !m.initialRecoveryShouldProbe(fresh) {
		t.Fatal("current plugin instance should be probed while failed")
	}
}

func TestInitialRecoveryProbesFailedPlugin(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.status = MCPStatusFailed
	p.mu.Unlock()
	if !m.initialRecoveryShouldProbe(p) {
		t.Fatal("failed-but-current plugin should be probed")
	}
}

func TestInitialRecoveryStopsWhenDisabled(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv-recovery-disabled", Type: "stdio", Command: "true"})

	mcpDisabledMu.Lock()
	prevG, prevW, prevOK := mcpDisabledGlobal, mcpDisabledWS, mcpDisabledCacheOK
	mcpDisabledGlobal = map[string]bool{"srv-recovery-disabled": true}
	mcpDisabledWS = map[string]map[string]bool{}
	mcpDisabledCacheOK = true
	mcpDisabledMu.Unlock()
	defer func() {
		mcpDisabledMu.Lock()
		mcpDisabledGlobal, mcpDisabledWS, mcpDisabledCacheOK = prevG, prevW, prevOK
		mcpDisabledMu.Unlock()
	}()
	if m.initialRecoveryShouldProbe(p) {
		t.Fatal("disabled plugin must not be probed")
	}
}

func TestMaybeStartInitialRecoveryNoOp(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.connected = true
	p.mu.Unlock()

	done := make(chan struct{})
	go func() { m.maybeStartInitialRecovery(context.Background(), p); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("maybeStartInitialRecovery blocked on connected plugin")
	}

	p.mu.Lock()
	p.connected = false
	p.awaitingOAuth = true
	p.mu.Unlock()
	done2 := make(chan struct{})
	go func() { m.maybeStartInitialRecovery(context.Background(), p); close(done2) }()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("maybeStartInitialRecovery blocked on oauth-waiting plugin")
	}
}

func TestInitialRecoveryLoopExitsOnCtxCancel(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})

	p.mu.Lock()
	p.status = MCPStatusFailed
	p.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.startInitialRecoveryLoop(ctx, p)
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		p.mu.RLock()
		st := p.status
		p.mu.RUnlock()
		if st == MCPStatusPending {
			t.Fatal("recovery loop probed after ctx cancel")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
