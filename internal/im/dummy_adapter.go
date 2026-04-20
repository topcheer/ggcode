package im

import (
	"context"
	"fmt"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// dummyAdapter is a no-op IM adapter used for automated Knight evaluation.
// It accepts configuration, auto-binds a channel, and records outbound events
// for metrics collection — but does not connect to any real IM platform.
type dummyAdapter struct {
	name    string
	manager *Manager

	mu      sync.RWMutex
	started bool
}

func newDummyAdapter(name string, _ config.IMConfig, _ config.IMAdapterConfig, mgr *Manager) *dummyAdapter {
	return &dummyAdapter{
		name:    name,
		manager: mgr,
	}
}

func (a *dummyAdapter) Name() string { return a.name }

func (a *dummyAdapter) Start(ctx context.Context) {
	debug.Log("dummy", "adapter=%s start", a.name)
	a.mu.Lock()
	a.started = true
	a.mu.Unlock()
	// TODO: auto-bind channel, start HTTP server, SSE broker (later tasks)
}

func (a *dummyAdapter) Send(_ context.Context, _ ChannelBinding, _ OutboundEvent) error {
	a.mu.RLock()
	started := a.started
	a.mu.RUnlock()
	if !started {
		return fmt.Errorf("dummy adapter %q not started", a.name)
	}
	// TODO: record metrics, push SSE events (later tasks)
	return nil
}
