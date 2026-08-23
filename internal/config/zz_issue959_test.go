package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestGatewayVendorMigrationPreservesVendorAPIKey (#959 problem 1):
// merging a standalone gateway vendor (e.g. vendors.openrouter with a
// vendor-level api_key) into ai-gateway must carry the key onto the migrated
// endpoint. Previously the key was silently dropped and the vendor deleted,
// then persisted by Load's unconditional Save: irreversible data loss.
func TestGatewayVendorMigrationPreservesVendorAPIKey(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
vendor: ai-gateway
endpoint: openrouter
model: test-model
vendors:
  ai-gateway:
    display_name: AI Gateway
    endpoints:
      novita:
        protocol: openai
        base_url: https://api.novita.ai/openai/v1
  openrouter:
    api_key: sk-or-legacy-123
    endpoints:
      openrouter:
        protocol: openai
        base_url: https://openrouter.ai/api/v1
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, still := cfg.Vendors["openrouter"]; still {
		t.Fatal("expected legacy openrouter vendor to be migrated away")
	}
	gw := cfg.Vendors["ai-gateway"]
	if got := gw.Endpoints["openrouter"].APIKey; got != "sk-or-legacy-123" {
		t.Fatalf("migrated endpoint lost vendor-level API key: got %q", got)
	}

	// Load performs an unconditional compact Save; reloading must still see
	// the key (plaintext or as a ${VAR} reference resolved from keys.env
	// after MigratePlaintextAPIKeys moved it).
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if got := reloaded.Vendors["ai-gateway"].Endpoints["openrouter"].APIKey; got != "sk-or-legacy-123" {
		t.Fatalf("API key not durable across Load+Save+Load: got %q", got)
	}
}

// TestGatewayVendorMigrationNoEndpointsKeepsVendorKey (#959 problem 1 edge):
// a gateway vendor with a key but no endpoints falls back to the ai-gateway
// vendor-level key instead of dropping the credential entirely.
func TestGatewayVendorMigrationNoEndpointsKeepsVendorKey(t *testing.T) {
	withTestHome(t)
	cfg := DefaultConfig()
	cfg.Vendors["openrouter"] = VendorConfig{
		APIKey:    "sk-or-orphan-456",
		Endpoints: nil,
	}

	mergeDefaultEndpoints(cfg, DefaultConfig())

	if _, still := cfg.Vendors["openrouter"]; still {
		t.Fatal("expected legacy openrouter vendor to be migrated away")
	}
	// DefaultConfig seeds ai-gateway.endpoints.openrouter, so the orphan key
	// lands on that default endpoint (endpoint-level carry); vendor-level
	// fallback only applies when no such endpoint exists at all.
	if got := cfg.Vendors["ai-gateway"].Endpoints["openrouter"].APIKey; got != "sk-or-orphan-456" {
		t.Fatalf("orphan vendor key not carried to migrated endpoint: got %q", got)
	}
}

// TestGatewayVendorMigrationDoesNotOverwriteEndpointKey (#959 problem 1):
// an existing endpoint-level key wins over the incoming vendor-level key.
func TestGatewayVendorMigrationDoesNotOverwriteEndpointKey(t *testing.T) {
	cfg := DefaultConfig()
	gw := cfg.Vendors["ai-gateway"]
	gw.Endpoints["openrouter"] = EndpointConfig{
		DisplayName: "OpenRouter",
		Protocol:    "openai",
		BaseURL:     "https://openrouter.ai/api/v1",
		APIKey:      "sk-endpoint-keep",
	}
	cfg.Vendors["ai-gateway"] = gw
	cfg.Vendors["openrouter"] = VendorConfig{
		APIKey: "sk-or-lose",
		Endpoints: map[string]EndpointConfig{
			"openrouter": {Protocol: "openai", BaseURL: "https://openrouter.ai/api/v1"},
		},
	}

	mergeDefaultEndpoints(cfg, DefaultConfig())

	if got := cfg.Vendors["ai-gateway"].Endpoints["openrouter"].APIKey; got != "sk-endpoint-keep" {
		t.Fatalf("existing endpoint key clobbered: got %q", got)
	}
}

// TestShouldBellExplicitFalseDisablesBell (#959 problem 2):
// bell:false must disable the bell even when desktop notifications are also
// off; nil (unset) keeps the backward-compatible default of on.
func TestShouldBellExplicitFalseDisablesBell(t *testing.T) {
	cases := []struct {
		cfg  NotificationConfig
		want bool
	}{
		{NotificationConfig{}, true},                                      // nil = default on
		{NotificationConfig{Bell: boolPtr(true)}, true},                   // explicit on
		{NotificationConfig{Bell: boolPtr(false)}, false},                 // explicit off (#959)
		{NotificationConfig{Desktop: true}, true},                         // bell unset -> default on
		{NotificationConfig{Bell: boolPtr(false), Desktop: false}, false}, // the exact #959 trap
		{NotificationConfig{Bell: boolPtr(true), Desktop: true}, true},    // explicit on with desktop
		{NotificationConfig{Bell: boolPtr(false), Desktop: true}, false},  // explicit off wins
	}
	for _, tc := range cases {
		if got := tc.cfg.ShouldBell(); got != tc.want {
			t.Errorf("ShouldBell(%+v) = %v, want %v", tc.cfg, got, tc.want)
		}
	}
}

// TestLoadBellExplicitFalseSurvivesSaveRoundtrip (#959 problem 2):
// notifications.bell: false written to YAML must round-trip through
// Load+Save without the zero-value merge resurrecting it as unset.
func TestLoadBellExplicitFalseSurvivesSaveRoundtrip(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
vendor: zai
endpoint: cn-coding-openai
model: test
notifications:
  bell: false
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Notifications.ShouldBell() {
		t.Fatal("explicit bell:false should disable the bell right after Load")
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if reloaded.Notifications.ShouldBell() {
		t.Fatal("explicit bell:false did not survive Load+Save+Load (merge resurrected it)")
	}
	if reloaded.Notifications.Bell == nil {
		t.Fatal("Bell pointer nil after roundtrip - explicit false was dropped on save")
	}
}

// TestLoadSystemPromptRemovalConcurrent (#959 problem 3):
// the system_prompt strip rewrites the file under lockConfigFile; several
// concurrent Loads of the same legacy file must all succeed and converge on
// a file without system_prompt.
func TestLoadSystemPromptRemovalConcurrent(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
vendor: zai
endpoint: cn-coding-openai
model: test
system_prompt: legacy prompt
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Load(path); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Load failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "system_prompt:") {
			t.Fatalf("system_prompt still present after concurrent migration:\n%s", data)
		}
	}
}
