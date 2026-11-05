package im

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

type dummyAdapter struct {
	name    string
	manager *Manager
	cfg     config.IMAdapterConfig
	imCfg   config.IMConfig

	server  *httpServer
	metrics *EvalMetrics

	mu sync.Mutex
}

func newDummyAdapter(name string, imCfg config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) *dummyAdapter {
	return &dummyAdapter{
		name:    name,
		manager: mgr,
		cfg:     adapterCfg,
		imCfg:   imCfg,
		metrics: NewEvalMetrics(),
	}
}

func (a *dummyAdapter) Name() string { return a.name }

func (a *dummyAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	a.metrics.RecordEvent(event)
	a.mu.Lock()
	srv := a.server
	a.mu.Unlock()
	if srv != nil {
		srv.pushEvent(event)
	}
	return nil
}

func (a *dummyAdapter) Start(ctx context.Context) {
	// #5078: assign the server BEFORE autoBind. Send only becomes reachable
	// once autoBind registers the channel in the manager, so assignment
	// before registration establishes the happens-before chain for the
	// emit path. The mu guard below additionally covers any unsynchronized
	// direct callers (the old code wrote/read a.server with no lock at all).
	a.mu.Lock()
	a.server = newHTTPServer(a)
	a.mu.Unlock()

	if err := a.autoBind(); err != nil {
		debug.Log("dummy", "auto-bind failed: %v", err)
		return
	}

	listenAddr := dummyStringValue(a.cfg.Extra, "listen_addr", "127.0.0.1:0")
	portFile := dummyStringValue(a.cfg.Extra, "port_file", "")

	a.server.start(ctx, listenAddr, portFile)

	<-ctx.Done()
	if portFile != "" {
		os.Remove(portFile)
	}
}

func (a *dummyAdapter) Close() error { return nil }

func (a *dummyAdapter) autoBind() error {
	session := a.manager.ActiveSession()
	if session == nil {
		return ErrNoSessionBound
	}
	binding := ChannelBinding{
		Workspace: session.Workspace,
		Platform:  PlatformDummy,
		Adapter:   a.name,
		TargetID:  "eval-user",
		ChannelID: "eval-channel",
		BoundAt:   time.Now(),
	}
	_, err := a.manager.BindChannel(binding)
	return err
}

func dummyStringValue(m map[string]interface{}, key, defaultVal string) string {
	if m == nil {
		return defaultVal
	}
	v, ok := m[key]
	if !ok {
		return defaultVal
	}
	s := stringFromAny(v)
	if s == "" {
		return defaultVal
	}
	return s
}

func dummyIntValue(m map[string]interface{}, key string, defaultVal int) int {
	if m == nil {
		return defaultVal
	}
	v, ok := m[key]
	if !ok {
		return defaultVal
	}
	n, ok := intValue(v)
	if !ok {
		return defaultVal
	}
	return n
}
