package plugin

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
)

// #3967: notifyStateChange must be nil-safe and must dispatch to the
// injected callback exactly once per call.
func TestIssue3967NotifyStateChangeDispatch(t *testing.T) {
	p := NewMCPPlugin(config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	// nil callback: must not panic (plugins built outside the manager
	// factory, e.g. direct NewMCPPlugin in tests or ACP bridges).
	p.notifyStateChange()

	var n int32
	p.mu.Lock()
	p.notifyState = func() { atomic.AddInt32(&n, 1) }
	p.mu.Unlock()
	p.notifyStateChange()
	p.notifyStateChange()
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Fatalf("expected 2 notifications, got %d", got)
	}
	// Callback swap while "running" must be picked up (RLock read per call).
	p.mu.Lock()
	p.notifyState = func() { atomic.AddInt32(&n, 100) }
	p.mu.Unlock()
	p.notifyStateChange()
	if got := atomic.LoadInt32(&n); got != 102 {
		t.Fatalf("expected callback swap visible (102), got %d", got)
	}
}

// #3967 (user contract): a manual manager Refresh must push an update to
// listeners REGARDLESS of outcome - here RefreshThrottled, the "nothing
// happened" branch that previously left the panel untouched forever.
func TestIssue3967ManualRefreshEmitsOnEveryOutcome(t *testing.T) {
	p := NewMCPPlugin(config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.connected = true
	p.client = mcp.NewClientFromConfig(p.cfg)
	p.lastRefreshAt = time.Now() // force the throttled branch
	p.mu.Unlock()

	mgr := &MCPManager{}
	mgr.mu.Lock()
	mgr.plugins = []*MCPPlugin{p}
	mgr.mu.Unlock()

	var emissions int32
	var sawName string
	mgr.mu.Lock()
	mgr.onUpdate = func(servers []MCPServerInfo) {
		atomic.AddInt32(&emissions, 1)
		if len(servers) == 1 {
			sawName = servers[0].Name
		}
	}
	mgr.mu.Unlock()

	found, outcome, _ := mgr.Refresh("srv")
	if !found || outcome != RefreshThrottled {
		t.Fatalf("expected found+RefreshThrottled, got found=%v outcome=%v", found, outcome)
	}
	if got := atomic.LoadInt32(&emissions); got < 1 {
		t.Fatal("manual refresh must emitUpdate even when throttled (panel convergence, #3967)")
	}
	if sawName != "srv" {
		t.Fatalf("emitted snapshot must carry the server, got %q", sawName)
	}
}

// #3967: attemptReconnect must notify on both the pending flip and the
// terminal failure - a dead server (unspawnable command) goes through both,
// and previously the panel never learned about either.
func TestIssue3967AttemptReconnectNotifiesFailure(t *testing.T) {
	p := NewMCPPlugin(config.MCPServerConfig{Name: "dead", Type: "stdio", Command: "/nonexistent-ggcode-test-cmd-3967"})
	var n int32
	p.mu.Lock()
	p.notifyState = func() { atomic.AddInt32(&n, 1) }
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if p.attemptReconnect(ctx) {
		t.Fatal("unspawnable command must not report a successful reconnect")
	}
	if got := atomic.LoadInt32(&n); got < 2 {
		t.Fatalf("expected >=2 notifications (pending flip + failed terminal), got %d", got)
	}
	if p.Status() != MCPStatusFailed {
		t.Fatalf("expected MCPStatusFailed after failed attempt, got %v", p.Status())
	}
}
