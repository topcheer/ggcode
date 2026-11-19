package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// ---- #5814 stdio helper server (re-run of this test binary) ----
// Modes (GGCODE_MCP5814_MODE_FILE, re-read at process start):
//
//	alive     - answers everything
//	stallping - answers discovery and tools/call, never answers ping (half-dead)
//	dieoncall - answers discovery and ping, exits on tools/call (dead on use)
func TestIssue5814PluginHelperProcess(t *testing.T) {
	if os.Getenv("GGCODE_MCP5814_HELPER") != "1" {
		return
	}
	mode := "alive"
	if path := os.Getenv("GGCODE_MCP5814_MODE_FILE"); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			mode = strings.TrimSpace(string(data))
		}
	}
	enc := json.NewEncoder(os.Stdout)
	dec := json.NewDecoder(os.Stdin)
	for {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		if req.Method == "notifications/initialized" {
			continue
		}
		base := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			base["result"] = map[string]interface{}{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{"listChanged": true}},
				"serverInfo":      map[string]interface{}{"name": "helper5814", "version": "1.0.0"},
			}
		case "ping":
			if mode == "stallping" {
				select {}
			}
			base["result"] = map[string]interface{}{}
		case "tools/list":
			base["result"] = map[string]interface{}{
				"tools": []interface{}{map[string]interface{}{
					"name":        "echo",
					"description": "echo tool",
					"inputSchema": map[string]interface{}{"type": "object"},
				}},
			}
		case "tools/call":
			if mode == "dieoncall" {
				os.Exit(3)
			}
			base["result"] = map[string]interface{}{
				"content": []interface{}{map[string]interface{}{"type": "text", "text": "echo-ok"}},
			}
		case "prompts/list":
			base["result"] = map[string]interface{}{"prompts": []interface{}{}}
		case "resources/list":
			base["result"] = map[string]interface{}{"resources": []interface{}{}}
		case "resources/templates/list":
			base["result"] = map[string]interface{}{"resourceTemplates": []interface{}{}}
		default:
			base["error"] = map[string]interface{}{"code": -32601, "message": "Method not found"}
		}
		_ = enc.Encode(base)
	}
}

func newIssue5814Plugin(t *testing.T, mode string) (*MCPPlugin, string) {
	t.Helper()
	modeFile := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(modeFile, []byte(mode), 0o644); err != nil {
		t.Fatalf("write mode file: %v", err)
	}
	cfg := config.MCPServerConfig{
		Name:    "helper5814",
		Type:    "stdio",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestIssue5814PluginHelperProcess"},
		Env: map[string]string{
			"GGCODE_MCP5814_HELPER":    "1",
			"GGCODE_MCP5814_MODE_FILE": modeFile,
		},
		SkipOSVCheck: true,
	}
	return NewMCPPlugin(cfg), modeFile
}

func setIssue5814Mode(t *testing.T, modeFile, mode string) {
	t.Helper()
	if err := os.WriteFile(modeFile, []byte(mode), 0o644); err != nil {
		t.Fatalf("flip mode file: %v", err)
	}
}

func issue5814WaitFor(t *testing.T, budget time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s: %s", budget, desc)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func issue5814CurrentClient(m *MCPPlugin) interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client
}

// issue5814Sink captures debug.Log output for audit-trail assertions. The
// live sink receives the resolved category (empty for mcp-health), so match
// on the [mcp-health] prefix in the message text instead.
type issue5814Sink struct {
	mu    sync.Mutex
	lines []string
}

func newIssue5814Sink(t *testing.T) *issue5814Sink {
	t.Helper()
	s := &issue5814Sink{}
	debug.SetLiveSink(func(category, msg string) {
		if !strings.HasPrefix(msg, "[mcp-health]") {
			return
		}
		s.mu.Lock()
		s.lines = append(s.lines, msg)
		s.mu.Unlock()
	})
	t.Cleanup(func() { debug.SetLiveSink(nil) })
	return s
}

func (s *issue5814Sink) joined() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.lines, "\n")
}

// shrinkIssue5814Timings shortens the probe/re-init schedules for tests and
// restores them (after a grace period so lingering goroutines have exited).
func shrinkIssue5814Timings(t *testing.T, probe time.Duration, backoff []time.Duration, connect time.Duration) {
	t.Helper()
	origProbe := stdioProbeTimeout
	origBackoff := stdioReinitBackoff
	origConnect := stdioReinitConnectTimeout
	stdioProbeTimeout = probe
	stdioReinitBackoff = backoff
	stdioReinitConnectTimeout = connect
	t.Cleanup(func() {
		time.Sleep(300 * time.Millisecond)
		stdioProbeTimeout = origProbe
		stdioReinitBackoff = origBackoff
		stdioReinitConnectTimeout = origConnect
	})
}

func TestIssue5814HealthProbePlan(t *testing.T) {
	// Transport defaults: stdio 10m, ws 60s, http/SSE none (notif-stream self-heals).
	m := NewMCPPlugin(config.MCPServerConfig{Name: "x", Command: "x"})
	if d, on := m.healthProbePlan("stdio"); !on || d != 10*time.Minute {
		t.Fatalf("stdio default = (%v, %v), want (10m, true)", d, on)
	}
	if d, on := m.healthProbePlan("ws"); !on || d != wsHealthProbeInterval {
		t.Fatalf("ws default = (%v, %v), want (%v, true)", d, on, wsHealthProbeInterval)
	}
	if _, on := m.healthProbePlan("http"); on {
		t.Fatal("http transport must not run the periodic probe")
	}

	// Per-server config wins over env; 0 disables.
	secs := 5
	zero := 0
	t.Setenv("GGCODE_MCP_HEALTH_PROBE_INTERVAL", "99")
	mc := NewMCPPlugin(config.MCPServerConfig{Name: "x", Command: "x", HealthProbeIntervalSeconds: &secs})
	if d, on := mc.healthProbePlan("stdio"); !on || d != 5*time.Second {
		t.Fatalf("config interval = (%v, %v), want (5s, true)", d, on)
	}
	mz := NewMCPPlugin(config.MCPServerConfig{Name: "x", Command: "x", HealthProbeIntervalSeconds: &zero})
	if _, on := mz.healthProbePlan("stdio"); on {
		t.Fatal("health_probe_interval: 0 must disable the probe")
	}

	// Env interval (seconds or duration) applies when config is unset.
	if d, on := m.healthProbePlan("stdio"); !on || d != 99*time.Second {
		t.Fatalf("env seconds interval = (%v, %v), want (99s, true)", d, on)
	}
	t.Setenv("GGCODE_MCP_HEALTH_PROBE_INTERVAL", "90s")
	if d, on := m.healthProbePlan("stdio"); !on || d != 90*time.Second {
		t.Fatalf("env duration interval = (%v, %v), want (90s, true)", d, on)
	}

	// GGCODE_MCP_HEALTH_PROBE=off disables unless config overrides.
	t.Setenv("GGCODE_MCP_HEALTH_PROBE", "off")
	if _, on := m.healthProbePlan("stdio"); on {
		t.Fatal("env kill switch must disable the probe")
	}
	if d, on := mc.healthProbePlan("stdio"); !on || d != 5*time.Second {
		t.Fatalf("config must beat the env kill switch: (%v, %v)", d, on)
	}
}

func TestIssue5814PeriodicProbeReportsHealthy(t *testing.T) {
	t.Setenv("GGCODE_MCP_HEALTH_PROBE_INTERVAL", "1")
	plugin, _ := newIssue5814Plugin(t, "alive")
	t.Cleanup(func() { _ = plugin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := plugin.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	issue5814WaitFor(t, 12*time.Second, "periodic probe reports healthy with a recorded probe time", func() bool {
		info := plugin.Info()
		return info.Health == MCPHealthHealthy && !info.LastProbeAt.IsZero() && info.LastProbeError == ""
	})
}

func TestIssue5814ProbeFailureAutoReinit(t *testing.T) {
	t.Setenv("GGCODE_MCP_HEALTH_PROBE_INTERVAL", "1")
	shrinkIssue5814Timings(t, 300*time.Millisecond, []time.Duration{10 * time.Millisecond}, 15*time.Second)
	sink := newIssue5814Sink(t)
	plugin, modeFile := newIssue5814Plugin(t, "stallping")
	t.Cleanup(func() { _ = plugin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := plugin.Connect(ctx); err != nil {
		t.Fatalf("connect against stallping discovery must succeed: %v", err)
	}
	firstClient := issue5814CurrentClient(plugin)

	// A stalled ping probe must record the failure. The dead state itself is
	// transient (auto re-init starts within ~10ms), so poll the probe error
	// and then assert the transition trail from the audit log.
	issue5814WaitFor(t, 20*time.Second, "stalled ping probe must record a failure", func() bool {
		return plugin.Info().LastProbeError != ""
	})

	// Flip the mode file: the auto re-init respawn must land on a healthy
	// helper and converge to healthy with a clean probe record.
	setIssue5814Mode(t, modeFile, "alive")
	issue5814WaitFor(t, 20*time.Second, "auto re-init respawns a healthy server", func() bool {
		info := plugin.Info()
		return info.Health == MCPHealthHealthy && info.LastProbeError == "" && !info.LastProbeAt.IsZero()
	})
	if cur := issue5814CurrentClient(plugin); cur == firstClient {
		t.Fatal("re-init must replace the dead client with a fresh one")
	}
	if !plugin.IsConnected() {
		t.Fatal("plugin must be connected after re-init")
	}
	trail := sink.joined()
	if !strings.Contains(trail, "probe FAILED (trigger=periodic)") {
		t.Fatalf("audit trail must record the failed probe; got: %s", trail)
	}
	if !strings.Contains(trail, "server considered dead, starting auto re-init") {
		t.Fatalf("audit trail must record the dead verdict; got: %s", trail)
	}
	if !strings.Contains(trail, "auto re-init SUCCEEDED") {
		t.Fatalf("audit trail must record the respawn outcome; got: %s", trail)
	}
}

func TestIssue5814BreakerOpenTriggersAutoReinit(t *testing.T) {
	t.Setenv("GGCODE_MCP_HEALTH_PROBE_INTERVAL", "1")
	shrinkIssue5814Timings(t, 5*time.Second, []time.Duration{10 * time.Millisecond}, 15*time.Second)
	sink := newIssue5814Sink(t)
	plugin, modeFile := newIssue5814Plugin(t, "dieoncall")
	t.Cleanup(func() { _ = plugin.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	adapter, err := plugin.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	echo, ok := registry.Get("mcp__helper5814__echo")
	if !ok {
		t.Fatal("echo tool not registered")
	}
	firstClient := issue5814CurrentClient(plugin)

	// Three consecutive infra failures (process death) open the breaker.
	for i := 0; i < 3; i++ {
		res, err := echo.Execute(ctx, json.RawMessage("{}"))
		if err != nil {
			t.Fatalf("call %d returned go-error: %v", i+1, err)
		}
		if !res.IsError {
			t.Fatalf("call %d against a dying server must fail, got %q", i+1, res.Content)
		}
	}
	// Breaker OPEN must fast-fail the next call instead of touching the pipe.
	res, err := echo.Execute(ctx, json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("post-open call returned go-error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "cooldown ends in") {
		t.Fatalf("post-open call must fast-fail on breaker cooldown, got %q", res.Content)
	}

	setIssue5814Mode(t, modeFile, "alive")
	issue5814WaitFor(t, 25*time.Second, "breaker-open auto re-init respawns the server", func() bool {
		return strings.Contains(sink.joined(), "auto re-init SUCCEEDED") && issue5814CurrentClient(plugin) != firstClient
	})
	joined := sink.joined()
	if !strings.Contains(joined, "breaker OPEN after consecutive infra failures -> stdio auto re-init") {
		t.Fatalf("audit trail must record the breaker-open re-init trigger; got:\n%s", joined)
	}
	if !plugin.IsConnected() || plugin.Info().Health != MCPHealthHealthy {
		t.Fatalf("server must be connected and healthy after re-init: connected=%v health=%q", plugin.IsConnected(), plugin.Info().Health)
	}
}

func TestIssue5814LeaseGuardFastFail(t *testing.T) {
	// 1h interval: wiring armed (lease = 2h), but no periodic tick interferes.
	t.Setenv("GGCODE_MCP_HEALTH_PROBE_INTERVAL", "3600")
	plugin, _ := newIssue5814Plugin(t, "alive")
	t.Cleanup(func() { _ = plugin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adapter, err := plugin.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	echo, ok := registry.Get("mcp__helper5814__echo")
	if !ok {
		t.Fatal("echo tool not registered")
	}

	// Shrink the liveness lease so the pre-call guard sees it expired.
	plugin.mu.RLock()
	client := plugin.client
	plugin.mu.RUnlock()
	if client == nil {
		t.Fatal("no client after connect")
	}
	client.SetLivenessLease(40 * time.Millisecond)
	time.Sleep(80 * time.Millisecond)

	start := time.Now()
	res, err := echo.Execute(ctx, json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("lease-guarded call returned go-error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "likely dead") {
		t.Fatalf("expired lease must fast-fail with a likely-dead notice, got %q", res.Content)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("lease guard must fail fast instead of hanging, took %v", elapsed)
	}
}

func TestIssue5814StaleHealthDerivation(t *testing.T) {
	t.Setenv("GGCODE_MCP_HEALTH_PROBE_INTERVAL", "3600")
	plugin, _ := newIssue5814Plugin(t, "alive")
	t.Cleanup(func() { _ = plugin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := plugin.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if got := plugin.Info().Health; got != MCPHealthHealthy {
		t.Fatalf("fresh connection must report healthy, got %q", got)
	}

	// Discovery round-trips armed LastHealthyAt; a probe interval shorter
	// than the age of that evidence must derive the transient stale state.
	plugin.mu.Lock()
	if got := plugin.healthForReport(); got != MCPHealthHealthy {
		plugin.mu.Unlock()
		t.Fatalf("pre-shrink health = %q, want healthy", got)
	}
	plugin.healthInterval = time.Nanosecond
	got := plugin.healthForReport()
	plugin.healthInterval = 3600 * time.Second
	plugin.mu.Unlock()
	if got != MCPHealthStale {
		t.Fatalf("stale derivation: got %q, want stale", got)
	}
}
