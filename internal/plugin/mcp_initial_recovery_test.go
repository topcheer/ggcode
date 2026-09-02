package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// newRecoveryTestManager builds a manager with one plugin registered under
// the given config, for guard-logic tests.
func newRecoveryTestManager(t *testing.T, cfg config.MCPServerConfig) (*MCPManager, *MCPPlugin) {
	t.Helper()
	m := NewMCPManager(nil, nil)
	p := NewMCPPlugin(cfg)
	m.mu.Lock()
	m.plugins = append(m.plugins, p)
	m.mu.Unlock()
	return m, p
}

// The recovery loop must not probe a plugin that is already connected.
func TestInitialRecoveryStopsWhenConnected(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.connected = true
	p.mu.Unlock()
	if m.initialRecoveryShouldProbe(p) {
		t.Fatal("connected plugin must not be probed")
	}
}

// #1285 semantics: a plugin the user explicitly closed (Disconnect, Reload
// removal, Install replacement) must not be resurrected by the recovery loop.
func TestInitialRecoveryStopsWhenClosed(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	if m.initialRecoveryShouldProbe(p) {
		t.Fatal("closed plugin must not be probed (no resurrection)")
	}
}

// OAuth-waiting plugins are owned by the TUI auth flow, not the loop.
func TestInitialRecoveryStopsWhenAwaitingOAuth(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.awaitingOAuth = true
	p.mu.Unlock()
	if m.initialRecoveryShouldProbe(p) {
		t.Fatal("awaitingOAuth plugin must not be probed")
	}
}

// A plugin replaced in the manager (same name, new pointer) must not be
// probed - that would resurrect an outdated server config.
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

// A plain failed plugin that is still current must keep being probed —
// this is the user's "network came back minutes later" scenario.
func TestInitialRecoveryProbesFailedPlugin(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.status = MCPStatusFailed
	p.mu.Unlock()
	if !m.initialRecoveryShouldProbe(p) {
		t.Fatal("failed-but-current plugin should be probed")
	}
}

// maybeStartInitialRecovery must be a no-op for connected plugins (no
// goroutine spawn) and for OAuth-waiting ones.
func TestMaybeStartInitialRecoveryNoOp(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.connected = true
	p.mu.Unlock()
	// Should return without blocking or spawning.
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

// The recovery loop must exit promptly when its context is cancelled --
// otherwise shutdown would leak a goroutine probing forever.
func TestInitialRecoveryLoopExitsOnCtxCancel(t *testing.T) {
	m, p := newRecoveryTestManager(t, config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	// Pre-set Failed: a probe calls connectOne -> markPending, flipping the
	// status to Pending. NewMCPPlugin starts at Pending, so without this the
	// assertion below could not distinguish "probed" from "fresh".
	p.mu.Lock()
	p.status = MCPStatusFailed
	p.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before first tick: loop must never dial
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
