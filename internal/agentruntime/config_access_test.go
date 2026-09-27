package agentruntime

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// ============================================================================
// #958: fallbacks.append must not be shadowed by the fallbacks.<N> prefix case
// ============================================================================

func TestConfigAccess_FallbacksAppendSucceeds(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")
	access := newTestAccess(t, cfgPath)

	access.cfg.Fallbacks = []config.FallbackConfig{
		{Enabled: true, Vendor: "anthropic", Endpoint: "default", Model: "m1"},
	}

	entry := `{"enabled":true,"vendor":"anthropic","endpoint":"default","model":"m2"}`
	if err := access.Set("fallbacks.append", entry); err != nil {
		t.Fatalf("fallbacks.append failed (shadowed by fallbacks.<N> case?): %v", err)
	}

	if got := len(access.cfg.Fallbacks); got != 2 {
		t.Fatalf("want 2 fallbacks after append, got %d", got)
	}
	appended := access.cfg.Fallbacks[1]
	if appended.Model != "m2" || appended.Vendor != "anthropic" {
		t.Fatalf("appended entry wrong: %+v", appended)
	}

	// The appended entry must be readable through the indexed key path.
	v, err := access.Get("fallbacks.1.model")
	if err != nil {
		t.Fatalf("read fallbacks.1.model: %v", err)
	}
	if v != "m2" {
		t.Fatalf("fallbacks.1.model = %q, want m2", v)
	}
}

// ============================================================================
// #956: pure Get must not mutate the live config (redact deep copy)
// ============================================================================

func TestConfigAccess_GetMCPServerDoesNotPolluteConfig(t *testing.T) {
	t.Setenv("GGCODE_TEST_MCP_TOKEN", "sk-test-secret-value-1234")
	cfg := &config.Config{
		MCPServers: []config.MCPServerConfig{{
			Name: "test-server",
			Type: "stdio",
			Env: map[string]string{
				"API_TOKEN": "${GGCODE_TEST_MCP_TOKEN}",
			},
			Headers: map[string]string{
				"Authorization": "${GGCODE_TEST_MCP_TOKEN}",
			},
		}},
	}
	access := NewConfigAccess(cfg, t.TempDir())

	out, err := access.Get("mcp_servers.test-server")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(out, "****") {
		t.Fatalf("output should contain masked secret, got: %s", out)
	}

	// The live config must keep the original ${VAR} reference untouched.
	if got := cfg.MCPServers[0].Env["API_TOKEN"]; got != "${GGCODE_TEST_MCP_TOKEN}" {
		t.Fatalf("Env[API_TOKEN] polluted by Get: %q", got)
	}
	if got := cfg.MCPServers[0].Headers["Authorization"]; got != "${GGCODE_TEST_MCP_TOKEN}" {
		t.Fatalf("Headers[Authorization] polluted by Get: %q", got)
	}
}

func TestConfigAccess_GetIMAdapterDoesNotPolluteConfig(t *testing.T) {
	t.Setenv("GGCODE_TEST_IM_SECRET", "im-secret-value-abcdef")
	cfg := &config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"tg": {
					Enabled:  true,
					Platform: "telegram",
					Extra: map[string]interface{}{
						"app_secret": "${GGCODE_TEST_IM_SECRET}",
						"plain":      "not-a-secret",
					},
				},
			},
		},
	}
	access := NewConfigAccess(cfg, t.TempDir())

	out, err := access.Get("im.adapters.tg")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(out, "****") {
		t.Fatalf("output should contain masked secret, got: %s", out)
	}

	if got := cfg.IM.Adapters["tg"].Extra["app_secret"]; got != "${GGCODE_TEST_IM_SECRET}" {
		t.Fatalf("Extra[app_secret] polluted by Get: %v", got)
	}
	if got := cfg.IM.Adapters["tg"].Extra["plain"]; got != "not-a-secret" {
		t.Fatalf("Extra[plain] unexpectedly changed: %v", got)
	}
}

// ============================================================================
// #957 problem 1: List/Delete must hold cfgMu while hot-reload refreshes fields
// ============================================================================

func TestConfigAccess_ListConcurrentHotReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")
	access := newTestAccess(t, cfgPath)
	w := NewConfigHotReload(cfgPath, access)

	fresh, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	fresh.MaxIterations = 42 // scalar field applyFreshConfig writes under lock

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				_, _ = access.List("all")
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 300; j++ {
			w.applyFreshConfig(fresh)
		}
	}()
	wg.Wait()
}

func TestConfigAccess_DeleteConcurrentHotReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")
	access := newTestAccess(t, cfgPath)
	w := NewConfigHotReload(cfgPath, access)

	fresh, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	fresh.MaxIterations = 42

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 300; j++ {
			// Delete of a non-existent server exercises the unlocked config
			// reads (MCPServers, save scope) against the locked hot-reload
			// field writes.
			_ = access.Delete("mcp_servers.no-such-server")
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 300; j++ {
			w.applyFreshConfig(fresh)
		}
	}()
	wg.Wait()
}

// ============================================================================
// #957 problem 2: provider probe (network) must run outside the config lock
// ============================================================================

func TestConfigAccess_SetModelProbeDoesNotBlockGet(t *testing.T) {
	cfg := &config.Config{
		Vendor:   "v",
		Endpoint: "e",
		Model:    "m1",
		FilePath: filepath.Join(t.TempDir(), "ggcode.yaml"),
		Vendors: map[string]config.VendorConfig{
			"v": {Endpoints: map[string]config.EndpointConfig{
				"e": {Protocol: "openai", BaseURL: "http://127.0.0.1:1/v1", DefaultModel: "m1", APIKey: "k"},
			}},
		},
	}
	access := NewConfigAccess(cfg, t.TempDir())

	probeStarted := make(chan struct{})
	release := make(chan struct{})
	orig := probeProviderFn
	probeProviderFn = func(r *config.ResolvedEndpoint) error {
		close(probeStarted)
		<-release // simulate a slow network probe
		return nil
	}
	t.Cleanup(func() { probeProviderFn = orig })

	setDone := make(chan error, 1)
	go func() {
		setDone <- access.Set("model", "m2")
	}()

	select {
	case <-probeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("probe never started")
	}

	// While the probe is in flight, Get must not block on cfgMu.
	getDone := make(chan struct{})
	go func() {
		_, _ = access.Get("vendor")
		close(getDone)
	}()
	select {
	case <-getDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Get blocked while probe in flight - probe runs under cfgMu")
	}

	close(release)
	if err := <-setDone; err != nil {
		t.Fatalf("Set(model) failed: %v", err)
	}
}

// ============================================================================
// Empty api_key value = clear intent: must commit probe-free (TUI/webui
// parity) instead of "refusing to set" on a probe that cannot pass.
// ============================================================================

func newAPIKeyProbeTestConfig(t *testing.T) (*config.Config, *configAccess) {
	cfg := &config.Config{
		Vendor:   "v",
		Endpoint: "e",
		Model:    "m1",
		FilePath: filepath.Join(t.TempDir(), "ggcode.yaml"),
		Vendors: map[string]config.VendorConfig{
			"v": {
				APIKey: "vendor-key",
				Endpoints: map[string]config.EndpointConfig{
					"e": {Protocol: "openai", BaseURL: "http://127.0.0.1:1/v1", DefaultModel: "m1", APIKey: "sk-old"},
				},
			},
		},
	}
	return cfg, NewConfigAccess(cfg, t.TempDir())
}

func TestConfigAccess_SetAPIKeyEmptyClearsProbeFree(t *testing.T) {
	cfg, access := newAPIKeyProbeTestConfig(t)

	probeCalls := 0
	orig := probeProviderFn
	probeProviderFn = func(r *config.ResolvedEndpoint) error {
		probeCalls++
		return nil
	}
	t.Cleanup(func() { probeProviderFn = orig })

	cases := []struct {
		name string
		key  string
	}{
		{"active endpoint key", "api_key"},
		{"vendor key", "api_key.v"},
		{"endpoint key", "api_key.v.e"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := access.Set(tc.key, ""); err != nil {
				t.Fatalf("Set(%q, \"\") = %v, want nil", tc.key, err)
			}
			// Whitespace-only matches the config layer's TrimSpace clear
			// semantics and must also take the probe-free clear path.
			if err := access.Set(tc.key, "   "); err != nil {
				t.Fatalf("Set(%q, whitespace) = %v, want nil", tc.key, err)
			}
		})
	}
	if probeCalls != 0 {
		t.Fatalf("clear path invoked the provider probe %d times, want 0", probeCalls)
	}
	if got := cfg.Vendors["v"].Endpoints["e"].APIKey; got != "" {
		t.Fatalf("endpoint key = %q, want cleared", got)
	}
	if got := cfg.Vendors["v"].APIKey; got != "" {
		t.Fatalf("vendor key = %q, want cleared", got)
	}
}

func TestConfigAccess_SetAPIKeyNonEmptyStillProbes(t *testing.T) {
	cfg, access := newAPIKeyProbeTestConfig(t)

	orig := probeProviderFn
	probeProviderFn = func(r *config.ResolvedEndpoint) error {
		return errors.New("401 unauthorized")
	}
	t.Cleanup(func() { probeProviderFn = orig })

	if err := access.Set("api_key.v.e", "sk-bad"); err == nil {
		t.Fatal("Set(api_key.v.e, bad key) should fail the probe and error")
	}
	if got := cfg.Vendors["v"].Endpoints["e"].APIKey; got != "sk-old" {
		t.Fatalf("endpoint key = %q after refused set, want unchanged \"sk-old\"", got)
	}
}
