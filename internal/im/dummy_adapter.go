package im

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// dummyAdapter implements Sink and startableSink for automated evaluation.
// It starts a localhost HTTP server and auto-binds a ChannelBinding so that
// an external orchestrator can drive multi-turn conversations via HTTP+SSE.
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

// Send implements Sink. It records metrics and pushes the event to the SSE broker.
func (a *dummyAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	a.metrics.RecordEvent(event)
	if a.server != nil {
		a.server.pushEvent(event)
	}
	return nil
}

// Start implements startableSink. It auto-binds a channel and starts the HTTP server.
func (a *dummyAdapter) Start(ctx context.Context) {
	if err := a.autoBind(); err != nil {
		debug.Log("dummy", "auto-bind failed: %v", err)
		return
	}

	a.server = newHTTPServer(a)

	listenAddr := dummyStringValue(a.cfg.Extra, "listen_addr", "127.0.0.1:0")
	portFile := dummyStringValue(a.cfg.Extra, "port_file", "")

	a.server.start(ctx, listenAddr, portFile)

	<-ctx.Done()
	if portFile != "" {
		os.Remove(portFile)
	}
}

// autoBind creates a ChannelBinding for the dummy adapter.
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

// --- config helpers ---
// Named with dummy- prefix to avoid conflict with stringValue (qq_adapter.go:1380)
// and intValue (qq_adapter.go:1435).

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
