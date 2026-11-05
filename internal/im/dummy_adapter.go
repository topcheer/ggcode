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
	if a.server != nil {
		a.server.pushEvent(event)
	}
	return nil
}

func (a *dummyAdapter) Start(ctx context.Context) {
	// #5078: create the server BEFORE autoBind publishes the channel binding.
	// Once the binding is visible, manager Emit paths may call Send from any
	// goroutine; assigning a.server first means every such Send already
	// observes a fully-set server (no write/read race, no dropped events).
	a.server = newHTTPServer(a)

	if err := a.autoBind(); err != nil {
		debug.Log("dummy", "auto-bind failed: %v", err)
		a.server = nil
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
