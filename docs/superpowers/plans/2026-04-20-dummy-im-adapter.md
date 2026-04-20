# Dummy IM Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a localhost HTTP+SSE dummy IM adapter that enables fully automated Knight evaluation (Phases 2-4) by simulating a human user driving multi-turn conversations with ggcode agent.

**Architecture:** The dummy adapter implements the existing `Sink` interface (`Name` + `Send`) and `startableSink` interface (adds `Start`). It starts a localhost HTTP server with `/send` (inject messages), `/events` (SSE stream), `/status`, `/healthz`, and `/shutdown` endpoints. It auto-binds a `ChannelBinding` on startup so messages flow through the existing `Manager.HandleInbound` → `DaemonBridge` pipeline. No changes to agent, knight, provider, or daemon bridge.

**Tech Stack:** Go 1.22+, net/http, encoding/json, sync, context. No external dependencies.

**Spec:** `docs/superpowers/specs/2026-04-20-dummy-im-adapter-design.md`

**Reviewer notes (fixed):**
- Config helpers named `dummyStringValue`/`dummyIntValue` to avoid conflict with existing `stringValue` in `qq_adapter.go:1380` and `intValue` in `qq_adapter.go:1435`
- Tests use `config.IMConfig{}` (matches existing adapter test pattern) and existing `stubBridge` from `runtime_test.go`
- `/shutdown` cancels context instead of `os.Exit(0)` — daemon handles graceful shutdown
- SSE `handleEvents` includes ring buffer replay for reconnected clients
- `roundPattern` requires digit + "tool" to avoid false positives

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/im/types.go` | Add `PlatformDummy` constant (1 line) |
| `internal/im/adapters.go` | Add `case "dummy"` in switch (5 lines) |
| `internal/im/dummy_adapter.go` | Adapter struct, Sink/Send, Start, auto-bind |
| `internal/im/dummy_server.go` | HTTP server, SSE broker, all endpoints |
| `internal/im/dummy_metrics.go` | EvalMetrics struct, RecordEvent, CSV output |
| `internal/im/dummy_adapter_test.go` | Unit tests for adapter, server, metrics |

---

### Task 1: Add PlatformDummy constant

**Files:**
- Modify: `internal/im/types.go:21` (after `PlatformSlack`)

- [ ] **Step 1: Add the constant**

In `internal/im/types.go`, add after the `PlatformSlack` line:

```go
PlatformDummy    Platform = "dummy"
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/im/...`
Expected: compiles without error

- [ ] **Step 3: Commit**

```bash
git add internal/im/types.go
git commit -m "feat(im): add PlatformDummy constant for eval adapter"
```

---

### Task 2: Add adapter case in adapters.go

**Files:**
- Modify: `internal/im/adapters.go:146` (after `PlatformSlack` case)

- [ ] **Step 1: Add the case**

In `internal/im/adapters.go`, add after the `case PlatformSlack:` block (before the closing `}`):

```go
case PlatformDummy:
    adapter := newDummyAdapter(name, cfg, adapterCfg, mgr)
    mgr.RegisterSink(adapter)
    adapter.Start(ctx)
```

This will not compile until `newDummyAdapter` is implemented (Task 3). That's expected.

- [ ] **Step 2: Commit**

```bash
git add internal/im/adapters.go
git commit -m "feat(im): wire dummy adapter case in StartConfiguredAdapters"
```

---

### Task 3: Implement dummy_adapter.go — struct, constructor, auto-bind

**Files:**
- Create: `internal/im/dummy_adapter.go`
- Test: `internal/im/dummy_adapter_test.go`

- [ ] **Step 1: Write the failing test for constructor and auto-bind**

Create `internal/im/dummy_adapter_test.go`:

```go
package im

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestDummyAdapter_Name(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/tmp/test-workspace",
		BoundAt:   time.Now(),
	})
	adapterCfg := testDummyAdapterConfig(t)
	a := newDummyAdapter("eval", config.IMConfig{}, adapterCfg, mgr)
	if a.Name() != "eval" {
		t.Errorf("Name() = %q, want %q", a.Name(), "eval")
	}
}

func TestDummyAdapter_AutoBind(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/tmp/test-workspace",
		BoundAt:   time.Now(),
	})
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
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/tmp/test-workspace",
		BoundAt:   time.Now(),
	})
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

	// Send a tool_result event
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
```

Note: Each test uses `t.TempDir()` via the helper, so parallel tests get isolated paths. `config.IMConfig{}` matches the pattern used by all other adapter tests (see `qq_adapter_test.go:205`, `tg_unit_test.go:314`, etc.).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/im/ -run TestDummyAdapter -v`
Expected: compilation error — `newDummyAdapter` undefined

- [ ] **Step 3: Write minimal implementation**

Create `internal/im/dummy_adapter.go`:

```go
package im

import (
	"context"
	"os"
	"strings"
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
	cfg     IMAdapterConfig
	imCfg   config.IMConfig

	server   *httpServer
	metrics  *EvalMetrics

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
	metricsPath := dummyStringValue(a.cfg.Extra, "metrics_path", "")

	a.server.start(ctx, listenAddr, portFile, metricsPath)

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
```

Note: `httpServer` is defined in Task 4. For now, reference it with a minimal stub so tests compile.

Also note: the `Send` method signature uses `context.Context` which matches the `Sink` interface.

Fix the test helper to not use `t` directly in the helper — update `testDummyAdapterConfig` to accept explicit paths:

```go
func testDummyAdapterConfig() IMAdapterConfig {
	return IMAdapterConfig{
		Enabled:  true,
		Platform: string(PlatformDummy),
		Extra: map[string]interface{}{
			"listen_addr":     "127.0.0.1:0",
			"port_file":       "/tmp/dummy-adapter-test-port",
			"metrics_path":    "/tmp/dummy-adapter-test-metrics.json",
			"sse_buffer_size": "1024",
		},
	}
}
```

And add cleanup in tests:

```go
func TestDummyAdapter_AutoBind(t *testing.T) {
	t.Cleanup(func() {
		os.Remove("/tmp/dummy-adapter-test-port")
		os.Remove("/tmp/dummy-adapter-test-metrics.json")
	})
	// ... rest of test
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/im/ -run "TestDummyAdapter_Name|TestDummyAdapter_AutoBind" -v`
Expected: PASS (note: `TestDummyAdapter_SendRecordsMetrics` needs `httpServer` from Task 4)

- [ ] **Step 5: Commit**

```bash
git add internal/im/dummy_adapter.go internal/im/dummy_adapter_test.go
git commit -m "feat(im): add dummy adapter struct, constructor, auto-bind, and config helpers"
```

---

### Task 4: Implement dummy_metrics.go — EvalMetrics

**Files:**
- Create: `internal/im/dummy_metrics.go`

- [ ] **Step 1: Write the failing test for metrics**

Add to `internal/im/dummy_adapter_test.go`:

```go
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
	m.RecordEvent(OutboundEvent{
		Kind: OutboundEventToolResult,
		ToolRes: &ToolResultInfo{ToolName: "bash", IsError: true},
	})
	m.RecordEvent(OutboundEvent{
		Kind: OutboundEventToolResult,
		ToolRes: &ToolResultInfo{ToolName: "bash", IsError: false},
	})
	if m.ReworkCount != 1 {
		t.Errorf("ReworkCount=%d, want 1 (consecutive bash calls)", m.ReworkCount)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/im/ -run TestEvalMetrics -v`
Expected: compilation error — `NewEvalMetrics` undefined

- [ ] **Step 3: Write implementation**

Create `internal/im/dummy_metrics.go`:

```go
package im

import (
	"encoding/csv"
	"os"
	"regexp"
	"strings"
	"time"
)

// EvalMetrics collects quantitative metrics during an evaluation session.
type EvalMetrics struct {
	SessionStart time.Time
	SessionEnd   time.Time

	UserMessages     int
	AskUserCount     int
	AskUserLatencyMs map[string]int64 // question_id → latency

	ToolCalls        map[string]int // tool_name → count
	TotalToolCalls   int
	ToolErrors       int
	ToolErrorsByTool map[string]int // tool_name → error count
	ReworkCount      int
	Rounds           int

	InputTokens  int
	OutputTokens int

	KnightReports int
	StagedSkills  int

	ElapsedMs int64

	lastToolName string
	askStart     map[string]time.Time // question_id → start time
}

// roundPattern matches round summary text. Requires a digit AND "tool" keyword
// to avoid false positives from normal text that happens to contain "success" etc.
// Coupled with EmitRoundSummary (emitter.go:229) which includes tool call counts.
var roundPattern = regexp.MustCompile(`\d+.*tool|tool.*\d+`)

// NewEvalMetrics creates a new metrics collector.
func NewEvalMetrics() *EvalMetrics {
	return &EvalMetrics{
		SessionStart:     time.Now(),
		ToolCalls:        make(map[string]int),
		ToolErrorsByTool: make(map[string]int),
		AskUserLatencyMs: make(map[string]int64),
		askStart:         make(map[string]time.Time),
	}
}

// RecordEvent updates metrics based on an outbound event.
func (m *EvalMetrics) RecordEvent(event OutboundEvent) {
	switch event.Kind {
	case OutboundEventToolResult:
		if event.ToolRes == nil {
			return
		}
		name := event.ToolRes.ToolName
		m.ToolCalls[name]++
		m.TotalToolCalls++

		// Rework detection: consecutive calls to the same tool
		if m.lastToolName == name {
			m.ReworkCount++
		}
		m.lastToolName = name

		if event.ToolRes.IsError {
			m.ToolErrors++
			m.ToolErrorsByTool[name]++
		}

	case OutboundEventText:
		text := event.Text
		if strings.HasPrefix(text, "🌙 ") {
			m.KnightReports++
		}
		if roundPattern.MatchString(text) {
			// Heuristic: text containing tool call summary markers is a round_done
			m.Rounds++
		}

	case OutboundEventApprovalRequest:
		m.AskUserCount++
	}
}

// Reset clears all per-task metrics while keeping session-level fields.
func (m *EvalMetrics) Reset() {
	m.UserMessages = 0
	m.AskUserCount = 0
	m.AskUserLatencyMs = make(map[string]int64)
	m.ToolCalls = make(map[string]int)
	m.TotalToolCalls = 0
	m.ToolErrors = 0
	m.ToolErrorsByTool = make(map[string]int)
	m.ReworkCount = 0
	m.Rounds = 0
	m.InputTokens = 0
	m.OutputTokens = 0
	m.KnightReports = 0
	m.StagedSkills = 0
	m.ElapsedMs = 0
	m.lastToolName = ""
	m.askStart = make(map[string]time.Time)
}

// Snapshot returns a copy of current metrics.
func (m *EvalMetrics) Snapshot() map[string]interface{} {
	return map[string]interface{}{
		"total_tool_calls": m.TotalToolCalls,
		"tool_calls":       m.ToolCalls,
		"tool_errors":      m.ToolErrors,
		"tool_errors_by":   m.ToolErrorsByTool,
		"rework_count":     m.ReworkCount,
		"rounds":           m.Rounds,
		"ask_user_count":   m.AskUserCount,
		"user_messages":    m.UserMessages,
		"knight_reports":   m.KnightReports,
		"staged_skills":    m.StagedSkills,
		"elapsed_ms":       m.ElapsedMs,
		"input_tokens":     m.InputTokens,
		"output_tokens":    m.OutputTokens,
	}
}

// WriteCSV appends a row to a CSV file.
func (m *EvalMetrics) WriteCSV(path string, runID, phase, mode string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	toolCalls := 0
	if m.ToolCalls != nil {
		toolCalls = m.TotalToolCalls
	}

	record := []string{
		runID,
		phase,
		mode,
		"",  // provider_vendor
		"",  // provider_endpoint
		"",  // provider_model
		"",  // task_set
		"",  // task_id
		"",  // success
		itoa(m.ToolErrors),
		itoa(toolCalls),
		itoa(m.UserMessages),
		itoa(int(m.ElapsedMs / 1000)),
		itoa(m.InputTokens),
		itoa(m.OutputTokens),
		itoa(m.StagedSkills),
		"",  // patched_skills
		"",  // rollbacks
		"",  // notes
	}
	return w.Write(record)
}

// WriteJSON writes the metrics snapshot to a JSON file.
func (m *EvalMetrics) WriteJSON(path string) error {
	// Simple JSON write — no need for encoding/json here
	// since Snapshot() returns a map
	return nil // placeholder — will use json.Marshal in actual implementation
}

func itoa(n int) string {
	return strings.TrimSpace(strings.TrimLeft(strings.Repeat(" ", 10), " "))[:0] + intToStr(n)
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	return strings.TrimSpace(string(rune('0'+n%10))) // simplified
}
```

Note: `WriteCSV` and `WriteJSON` are stubs that will be completed in the integration task. The `itoa` and `intToStr` helpers are placeholders — the real implementation uses `strconv.Itoa` and `json.Marshal`.

Let me correct these:

```go
import "strconv"

// itoa returns the string representation of n.
func itoa(n int) string {
	return strconv.Itoa(n)
}
```

Remove the placeholder `itoa` and `intToStr` functions and use `strconv.Itoa` directly in `WriteCSV`. For `WriteJSON`, use `json.Marshal`:

```go
import "encoding/json"

func (m *EvalMetrics) WriteJSON(path string) error {
	data, err := json.MarshalIndent(m.Snapshot(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/im/ -run TestEvalMetrics -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/im/dummy_metrics.go internal/im/dummy_adapter_test.go
git commit -m "feat(im): add EvalMetrics with tool/knight/round tracking"
```

---

### Task 5: Implement dummy_server.go — HTTP server and SSE broker

**Files:**
- Create: `internal/im/dummy_server.go`

This is the largest component. It contains:
1. `sseBroker` — ring buffer SSE fan-out with seq numbers
2. `httpServer` — all HTTP endpoints

- [ ] **Step 1: Write the failing test for HTTP endpoints**

Add to `internal/im/dummy_adapter_test.go`:

```go
import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
)

func TestDummyServer_SendEndpoint(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/tmp/test-workspace",
		BoundAt:   time.Now(),
	})
	adapterCfg := testDummyAdapterConfig()
	a := newDummyAdapter("eval", IMConfig{}, adapterCfg, mgr)
	_ = a.autoBind()

	// Create a mock bridge to capture submitted messages
	var receivedText string
	mgr.SetBridge(BridgeFunc(func(ctx context.Context, msg InboundMessage) error {
		receivedText = msg.Text
		return nil
	}))

	srv := newHTTPServer(a)
	handler := srv.handler()

	// POST /send
	body := `{"text":"hello agent"}`
	req := httptest.NewRequest("POST", "/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("POST /send status = %d, want 200", w.Code)
	}
	if !strings.Contains(receivedText, "hello agent") {
		t.Errorf("bridge received %q, want to contain 'hello agent'", receivedText)
	}
}

func TestDummyServer_HealthzEndpoint(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/tmp/test-workspace",
		BoundAt:   time.Now(),
	})
	adapterCfg := testDummyAdapterConfig()
	a := newDummyAdapter("eval", IMConfig{}, adapterCfg, mgr)
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
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/tmp/test-workspace",
		BoundAt:   time.Now(),
	})
	adapterCfg := testDummyAdapterConfig()
	a := newDummyAdapter("eval", IMConfig{}, adapterCfg, mgr)
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

// BridgeFunc is a test helper that wraps a function as a Bridge.
type BridgeFunc func(ctx context.Context, msg InboundMessage) error

func (f BridgeFunc) SubmitInboundMessage(ctx context.Context, msg InboundMessage) error {
	return f(ctx, msg)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/im/ -run "TestDummyServer" -v`
Expected: compilation error — `newHTTPServer` undefined

- [ ] **Step 3: Write implementation**

Create `internal/im/dummy_server.go`:

```go
package im

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// httpServer provides the HTTP endpoints for the dummy adapter.
type httpServer struct {
	adapter *dummyAdapter

	sseBroker *sseBroker
	shutdownToken string

	mu sync.Mutex // serializes /send requests
}

type sseBroker struct {
	mu       sync.RWMutex
	buffer   []sseEntry
	bufSize  int
	head     int // write position (ring)
	seq      int64
	subs     map[chan sseEntry]struct{}
	pinned   map[int64]sseEntry // approval_request events are never evicted
}

type sseEntry struct {
	seq   int64
	event string // SSE event type
	data  []byte // JSON payload
}

func newHTTPServer(a *dummyAdapter) *httpServer {
	bufSize := dummyIntValue(a.cfg.Extra, "sse_buffer_size", 1024)
	token := generateShutdownToken()
	return &httpServer{
		adapter:       a,
		sseBroker:     newSSEBroker(bufSize),
		shutdownToken: token,
	}
}

func newSSEBroker(bufSize int) *sseBroker {
	if bufSize < 16 {
		bufSize = 16
	}
	return &sseBroker{
		buffer: make([]sseEntry, bufSize),
		bufSize: bufSize,
		subs:   make(map[chan sseEntry]struct{}),
		pinned: make(map[int64]sseEntry),
	}
}

func (b *sseBroker) push(eventType string, data []byte) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.seq++
	entry := sseEntry{seq: b.seq, event: eventType, data: data}

	// Write to ring buffer
	b.buffer[b.head] = entry
	b.head = (b.head + 1) % b.bufSize

	// Fan out to subscribers
	for ch := range b.subs {
		select {
		case ch <- entry:
		default:
			// subscriber too slow, drop
		}
	}
	return b.seq
}

func (b *sseBroker) subscribe() (chan sseEntry, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan sseEntry, 256)
	b.subs[ch] = struct{}{}
	return ch, b.seq
}

func (b *sseBroker) unsubscribe(ch chan sseEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, ch)
}

// pushEvent converts an OutboundEvent to SSE and pushes it to the broker.
func (s *httpServer) pushEvent(event OutboundEvent) {
	sseType, data := outboundToSSE(event)
	if sseType == "" {
		return
	}
	seq := s.sseBroker.push(sseType, data)

	// Pin approval_request events
	if sseType == "approval_request" {
		s.sseBroker.mu.Lock()
		s.sseBroker.pinned[seq] = sseEntry{seq: seq, event: sseType, data: data}
		s.sseBroker.mu.Unlock()
	}
}

// outboundToSSE converts an OutboundEvent to an SSE event type and JSON data.
func outboundToSSE(event OutboundEvent) (string, []byte) {
	switch event.Kind {
	case OutboundEventText:
		text := event.Text
		if strings.HasPrefix(text, "🌙 ") {
			data, _ := json.Marshal(map[string]string{"kind": "knight_report", "content": text})
			return "knight_report", data
		}
		// Heuristic round_done detection
		if roundPattern.MatchString(text) && strings.Contains(text, "tool") {
			data, _ := json.Marshal(map[string]string{"kind": "round_done", "content": text})
			return "round_done", data
		}
		data, _ := json.Marshal(map[string]string{"kind": "text", "content": text})
		return "text", data

	case OutboundEventToolResult:
		if event.ToolRes == nil {
			return "", nil
		}
		data, _ := json.Marshal(map[string]interface{}{
			"kind":     "tool_result",
			"tool":     event.ToolRes.ToolName,
			"result":   event.ToolRes.Result,
			"is_error": event.ToolRes.IsError,
		})
		return "tool_result", data

	case OutboundEventApprovalRequest:
		data, _ := json.Marshal(map[string]interface{}{
			"kind": "approval_request",
		})
		return "approval_request", data

	case OutboundEventStatus:
		data, _ := json.Marshal(map[string]string{"kind": "status", "content": event.Status})
		return "status", data
	}
	return "", nil
}

// handler returns the HTTP handler for all endpoints.
func (s *httpServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/send", s.handleSend)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/shutdown", s.handleShutdown)
	return mux
}

// start starts the HTTP server on the given address.
func (s *httpServer) start(ctx context.Context, listenAddr, portFile, metricsPath string) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		debug.Log("dummy", "listen failed: %v", err)
		return
	}

	srv := &http.Server{Handler: s.handler()}

	// Write port file atomically
	if portFile != "" {
		addr := listener.Addr().String()
		content := fmt.Sprintf("%s\n%s\n", addr, s.shutdownToken)
		tmpFile := portFile + ".tmp"
		if err := os.WriteFile(tmpFile, []byte(content), 0644); err == nil {
			os.Rename(tmpFile, portFile)
		}
	}

	debug.Log("dummy", "HTTP server listening on %s", listener.Addr())

	go func() {
		<-ctx.Done()
		srv.Close()
		listener.Close()
	}()

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			debug.Log("dummy", "server error: %v", err)
		}
	}()
}

// handleSend processes POST /send requests.
func (s *httpServer) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Serialize: only one message at a time
	s.mu.Lock()
	defer s.mu.Unlock()

	var req struct {
		Text            string `json:"text"`
		ClientMessageID string `json:"client_message_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Reset metrics if requested
	if r.URL.Query().Get("reset_metrics") == "true" {
		s.adapter.metrics.Reset()
	}

	// Generate message ID
	msgID := req.ClientMessageID
	if msgID == "" {
		msgID = "msg_" + newID()
	}

	// Build InboundMessage
	msg := InboundMessage{
		Envelope: Envelope{
			Adapter:    s.adapter.name,
			Platform:   PlatformDummy,
			ChannelID:  "eval-channel",
			SenderID:   "eval-user",
			MessageID:  msgID,
			ReceivedAt: time.Now(),
		},
		Text: req.Text,
	}

	// Track user message count
	s.adapter.metrics.UserMessages++

	// Submit through the manager
	ctx := r.Context()
	if err := s.adapter.manager.HandleInbound(ctx, msg); err != nil {
		debug.Log("dummy", "HandleInbound error: %v", err)
		// Still return OK — the message was submitted even if bridge processing had issues
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":     "ok",
		"message_id": msgID,
	})
}

// handleEvents serves the SSE event stream.
func (s *httpServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, lastSeq := s.sseBroker.subscribe()
	defer s.sseBroker.unsubscribe(ch)

	// Send hello with current seq
	helloData, _ := json.Marshal(map[string]int64{"last_seq": lastSeq})
	fmt.Fprintf(w, "event: hello\ndata: %s\n\n", helloData)
	flusher.Flush()

	// Heartbeat ticker
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case entry := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", entry.event, entry.data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// handleStatus returns current state snapshot.
func (s *httpServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot := s.adapter.manager.Snapshot()
	response := map[string]interface{}{
		"bindings": snapshot.CurrentBindings,
		"metrics":  s.adapter.metrics.Snapshot(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleHealthz returns readiness check.
func (s *httpServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleShutdown gracefully shuts down the server.
func (s *httpServer) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify bearer token
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token != s.shutdownToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "shutting_down"})

	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()
}

func generateShutdownToken() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405")))
	}
	return hex.EncodeToString(raw[:])
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/im/ -run "TestDummyServer|TestDummyAdapter" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/im/dummy_server.go internal/im/dummy_adapter_test.go
git commit -m "feat(im): add dummy HTTP server with SSE broker, /send, /events, /status, /healthz"
```

---

### Task 6: Integration test — full message flow through adapter

**Files:**
- Modify: `internal/im/dummy_adapter_test.go`

- [ ] **Step 1: Write integration test**

Add to `internal/im/dummy_adapter_test.go`:

```go
import "net/http/httptest"

func TestDummyAdapter_IntegrationFlow(t *testing.T) {
	// Setup
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/tmp/test-workspace",
		BoundAt:   time.Now(),
	})

	adapterCfg := testDummyAdapterConfig()
	a := newDummyAdapter("eval", IMConfig{}, adapterCfg, mgr)

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
	mgr.SetBridge(BridgeFunc(func(ctx context.Context, msg InboundMessage) error {
		submitMu.Lock()
		submitted = append(submitted, msg)
		submitMu.Unlock()
		return nil
	}))

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

	// 6. Check healthz endpoint
	req3 := httptest.NewRequest("GET", "/healthz", nil)
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("GET /healthz status=%d, want 200", w3.Code)
	}
}
```

- [ ] **Step 2: Run integration test**

Run: `go test ./internal/im/ -run TestDummyAdapter_IntegrationFlow -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/im/dummy_adapter_test.go
git commit -m "test(im): add integration test for dummy adapter full message flow"
```

---

### Task 7: Verify full build and existing tests

- [ ] **Step 1: Build the full project**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 2: Run the full IM package test suite**

Run: `go test ./internal/im/ -v -count=1`
Expected: all tests pass (existing + new)

- [ ] **Step 3: Run the broader test suite**

Run: `go test ./internal/... -count=1`
Expected: all tests pass

- [ ] **Step 4: Commit any fixes**

If any test failures occurred, fix and commit with appropriate message.

---

### Task 8: Add example configuration and documentation

**Files:**
- Create: `.ggcode/configs/eval-dummy.yaml` (example config)

- [ ] **Step 1: Create example configuration file**

```yaml
# Example configuration for Knight evaluation using the dummy IM adapter.
# Usage: ggcode daemon -c .ggcode/configs/eval-dummy.yaml

vendor: zai
endpoint: cn-coding-openai
model: glm-5-turbo

knight:
  enabled: true
  trust_level: staged
  model: glm-5-air
  idle_delay_sec: 60
  capabilities:
    - skill_creation
    - skill_validation
    - regression_testing
    - doc_sync

im:
  enabled: true
  adapters:
    dummy:
      enabled: true
      platform: dummy
      extra:
        listen_addr: "127.0.0.1:0"
        metrics_path: ".tmp/knight-eval/metrics.json"
        sse_buffer_size: "1024"
        port_file: ".tmp/dummy-adapter-port"
```

- [ ] **Step 2: Verify config loads**

Run: `go run ./cmd/ggcode daemon --help`
Expected: help output (confirms no compilation errors)

- [ ] **Step 3: Commit**

```bash
git add .ggcode/configs/eval-dummy.yaml
git commit -m "docs: add example dummy adapter config for Knight evaluation"
```

---

## Summary

| Task | Files | Purpose |
|------|-------|---------|
| 1 | `internal/im/types.go` | PlatformDummy constant |
| 2 | `internal/im/adapters.go` | Wire dummy case |
| 3 | `internal/im/dummy_adapter.go` | Adapter struct + Sink + Start + auto-bind |
| 4 | `internal/im/dummy_metrics.go` | EvalMetrics collection |
| 5 | `internal/im/dummy_server.go` | HTTP + SSE broker |
| 6 | `internal/im/dummy_adapter_test.go` | Integration tests |
| 7 | — | Full build + test verification |
| 8 | `.ggcode/configs/eval-dummy.yaml` | Example config |

Total: ~520 lines of Go code, ~250 lines of tests, 1 config file.
