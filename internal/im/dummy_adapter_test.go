package im

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestDummyAdapter_Name(t *testing.T) {
	mgr, _ := testDummyManager()
	adapterCfg := testDummyAdapterConfig(t)
	a := newDummyAdapter("eval", config.IMConfig{}, adapterCfg, mgr)
	if a.Name() != "eval" {
		t.Errorf("Name() = %q, want %q", a.Name(), "eval")
	}
}

func TestDummyAdapter_AutoBind(t *testing.T) {
	mgr, bridge := testDummyManager()
	_ = bridge
	adapterCfg := testDummyAdapterConfig(t)
	a := newDummyAdapter("eval", config.IMConfig{}, adapterCfg, mgr)

	if err := a.autoBind(); err != nil {
		t.Fatalf("autoBind() error: %v", err)
	}

	bindings := mgr.CurrentBindings()
	found := false
	for _, b := range bindings {
		if b.Adapter == "eval" && b.Platform == PlatformDummy && b.ChannelID == "eval-channel" {
			found = true
		}
	}
	if !found {
		t.Error("autoBind did not create expected binding")
	}
}

func TestDummyAdapter_SendRecordsMetrics(t *testing.T) {
	mgr, _ := testDummyManager()
	adapterCfg := testDummyAdapterConfig(t)
	a := newDummyAdapter("eval", config.IMConfig{}, adapterCfg, mgr)
	_ = a.autoBind()

	ctx := context.Background()
	binding := ChannelBinding{
		Workspace: "/tmp/test-workspace",
		Platform:  PlatformDummy,
		Adapter:   "eval",
		ChannelID: "eval-channel",
	}

	err := a.Send(ctx, binding, OutboundEvent{
		Kind: OutboundEventToolResult,
		ToolRes: &ToolResultInfo{
			ToolName: "bash",
			IsError:  false,
		},
	})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	m := a.metrics
	if m.TotalToolCalls != 1 {
		t.Errorf("TotalToolCalls = %d, want 1", m.TotalToolCalls)
	}
	if m.ToolCalls["bash"] != 1 {
		t.Errorf("ToolCalls[\"bash\"] = %d, want 1", m.ToolCalls["bash"])
	}
}

func TestEvalMetrics_RecordEvent(t *testing.T) {
	m := NewEvalMetrics()

	// Tool result — success
	m.RecordEvent(OutboundEvent{
		Kind: OutboundEventToolResult,
		ToolRes: &ToolResultInfo{
			ToolName: "bash",
			IsError:  false,
		},
	})
	if m.TotalToolCalls != 1 || m.ToolCalls["bash"] != 1 {
		t.Errorf("after bash success: TotalToolCalls=%d, bash=%d", m.TotalToolCalls, m.ToolCalls["bash"])
	}
	if m.ToolErrors != 0 {
		t.Errorf("ToolErrors=%d, want 0", m.ToolErrors)
	}

	// Tool result — error
	m.RecordEvent(OutboundEvent{
		Kind: OutboundEventToolResult,
		ToolRes: &ToolResultInfo{
			ToolName: "bash",
			IsError:  true,
		},
	})
	if m.ToolErrors != 1 {
		t.Errorf("ToolErrors=%d, want 1", m.ToolErrors)
	}
	if m.ToolErrorsByTool["bash"] != 1 {
		t.Errorf("ToolErrorsByTool[\"bash\"]=%d, want 1", m.ToolErrorsByTool["bash"])
	}

	// Knight report detection
	m.RecordEvent(OutboundEvent{
		Kind: OutboundEventText,
		Text: "🌙 Knight detected a pattern",
	})
	if m.KnightReports != 1 {
		t.Errorf("KnightReports=%d, want 1", m.KnightReports)
	}

	// Round done detection (text with tool counts)
	m.RecordEvent(OutboundEvent{
		Kind: OutboundEventText,
		Text: "Round complete: 5 tool calls (4 success, 1 failure)",
	})
	if m.Rounds != 1 {
		t.Errorf("Rounds=%d, want 1", m.Rounds)
	}

	// Rework detection — consecutive same tool
	// Up to this point, "bash" was called twice in a row (success then error),
	// which already added 1 rework. Now test with "edit" — two consecutive
	// calls will add 1 more rework.
	m.RecordEvent(OutboundEvent{
		Kind:    OutboundEventToolResult,
		ToolRes: &ToolResultInfo{ToolName: "edit", IsError: true},
	})
	m.RecordEvent(OutboundEvent{
		Kind:    OutboundEventToolResult,
		ToolRes: &ToolResultInfo{ToolName: "edit", IsError: false},
	})
	if m.ReworkCount != 2 {
		t.Errorf("ReworkCount=%d, want 2 (1 from bash + 1 from edit)", m.ReworkCount)
	}
}

func TestEvalMetrics_Reset(t *testing.T) {
	m := NewEvalMetrics()
	m.TotalToolCalls = 5
	m.KnightReports = 2
	m.Reset()
	if m.TotalToolCalls != 0 || m.KnightReports != 0 {
		t.Errorf("Reset did not clear metrics: TotalToolCalls=%d, KnightReports=%d", m.TotalToolCalls, m.KnightReports)
	}
}

func TestDummyServer_SendEndpoint(t *testing.T) {
	mgr, _ := testDummyManager()
	adapterCfg := testDummyAdapterConfig(t)
	a := newDummyAdapter("eval", config.IMConfig{}, adapterCfg, mgr)
	_ = a.autoBind()

	srv := newHTTPServer(a)
	handler := srv.handler()

	body := `{"text":"hello agent"}`
	req := httptest.NewRequest("POST", "/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("POST /send status = %d, want 200", w.Code)
	}
}

func TestDummyServer_HealthzEndpoint(t *testing.T) {
	mgr, _ := testDummyManager()
	adapterCfg := testDummyAdapterConfig(t)
	a := newDummyAdapter("eval", config.IMConfig{}, adapterCfg, mgr)
	_ = a.autoBind()

	srv := newHTTPServer(a)
	handler := srv.handler()

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Errorf("healthz body = %q, want to contain 'ok'", w.Body.String())
	}
}

func TestDummyServer_StatusEndpoint(t *testing.T) {
	mgr, _ := testDummyManager()
	adapterCfg := testDummyAdapterConfig(t)
	a := newDummyAdapter("eval", config.IMConfig{}, adapterCfg, mgr)
	_ = a.autoBind()

	srv := newHTTPServer(a)
	handler := srv.handler()

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /status status = %d, want 200", w.Code)
	}
}

func TestDummyAdapter_IntegrationFlow(t *testing.T) {
	// Setup
	mgr, _ := testDummyManager()
	adapterCfg := testDummyAdapterConfig(t)
	a := newDummyAdapter("eval", config.IMConfig{}, adapterCfg, mgr)

	// Auto-bind
	if err := a.autoBind(); err != nil {
		t.Fatalf("autoBind: %v", err)
	}

	// Create HTTP server
	srv := newHTTPServer(a)
	handler := srv.handler()

	// Set up a mock bridge that records inbound messages
	var submitted []InboundMessage
	var submitMu sync.Mutex
	mgr.SetBridge(&captureBridge{mu: &submitMu, msgs: &submitted})

	// Simulate agent sending events to the adapter (Sink.Send)
	binding := ChannelBinding{
		Workspace: "/tmp/test-workspace",
		Platform:  PlatformDummy,
		Adapter:   "eval",
		ChannelID: "eval-channel",
	}
	ctx := context.Background()

	// 1. Agent sends tool result
	a.Send(ctx, binding, OutboundEvent{
		Kind: OutboundEventToolResult,
		ToolRes: &ToolResultInfo{
			ToolName: "bash",
			Result:   "make test\nPASS",
			IsError:  false,
		},
	})

	// 2. Agent sends text (round summary)
	a.Send(ctx, binding, OutboundEvent{
		Kind: OutboundEventText,
		Text: "All tests passed. tool calls: 3",
	})

	// 3. Agent sends knight report
	a.Send(ctx, binding, OutboundEvent{
		Kind: OutboundEventText,
		Text: "🌙 Detected repeated build-test pattern, creating skill",
	})

	// Verify metrics
	m := a.metrics
	if m.TotalToolCalls != 1 {
		t.Errorf("TotalToolCalls=%d, want 1", m.TotalToolCalls)
	}
	if m.KnightReports != 1 {
		t.Errorf("KnightReports=%d, want 1", m.KnightReports)
	}
	if m.Rounds != 1 {
		t.Errorf("Rounds=%d, want 1", m.Rounds)
	}

	// 4. Orchestrator sends message via HTTP
	body := `{"text":"fix the failing test in internal/extract"}`
	req := httptest.NewRequest("POST", "/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /send status=%d, want 200", w.Code)
	}

	// Wait for the async HandleInbound goroutine to submit the message.
	// The HTTP handler spawns a goroutine via safego.Go, so we need to
	// give it time to complete.
	for i := 0; i < 50; i++ {
		submitMu.Lock()
		count := len(submitted)
		submitMu.Unlock()
		if count >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Verify message was submitted to bridge
	submitMu.Lock()
	if len(submitted) != 1 {
		t.Fatalf("submitted messages=%d, want 1", len(submitted))
	}
	if submitted[0].Text != "fix the failing test in internal/extract" {
		t.Errorf("submitted text=%q, want exact match", submitted[0].Text)
	}
	if submitted[0].Envelope.Adapter != "eval" {
		t.Errorf("envelope adapter=%q, want 'eval'", submitted[0].Envelope.Adapter)
	}
	if submitted[0].Envelope.Platform != PlatformDummy {
		t.Errorf("envelope platform=%q, want 'dummy'", submitted[0].Envelope.Platform)
	}
	submitMu.Unlock()

	// Verify metrics track user message
	if m.UserMessages != 1 {
		t.Errorf("UserMessages=%d, want 1", m.UserMessages)
	}

	// 5. Check status endpoint
	req2 := httptest.NewRequest("GET", "/status", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("GET /status status=%d, want 200", w2.Code)
	}
}

// --- test helpers ---

// captureBridge records submitted messages for test assertions.
type captureBridge struct {
	mu   *sync.Mutex
	msgs *[]InboundMessage
}

func (b *captureBridge) SubmitInboundMessage(ctx context.Context, msg InboundMessage) error {
	b.mu.Lock()
	*b.msgs = append(*b.msgs, msg)
	b.mu.Unlock()
	return nil
}

// testDummyManager creates a Manager with session and binding store ready for testing.
func testDummyManager() (*Manager, *stubBridge) {
	mgr := NewManager()
	bridge := &stubBridge{}
	mgr.SetBridge(bridge)
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/tmp/test-workspace",
		BoundAt:   time.Now(),
	})
	return mgr, bridge
}

// testDummyAdapterConfig creates an adapter config with isolated temp paths.
func testDummyAdapterConfig(t *testing.T) config.IMAdapterConfig {
	t.Helper()
	dir := t.TempDir()
	return config.IMAdapterConfig{
		Enabled:  true,
		Platform: string(PlatformDummy),
		Extra: map[string]interface{}{
			"listen_addr":     "127.0.0.1:0",
			"port_file":       dir + "/dummy-port",
			"metrics_path":    dir + "/metrics.json",
			"sse_buffer_size": "1024",
		},
	}
}
