package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func unmarshalYAMLForTest(data []byte, out *map[string]interface{}) error {
	return yaml.Unmarshal(data, out)
}

func yamlMarshalForTest(v interface{}) ([]byte, error) {
	return yaml.Marshal(v)
}

// --- config_plugin.go (was 0% across the file) ---

func TestPluginCRUD(t *testing.T) {
	cfg := &Config{}

	if cfg.FindPlugin("x") != nil {
		t.Error("expected nil for empty config")
	}
	if cfg.ListPlugins() != nil {
		t.Error("expected nil list for empty config")
	}

	// Nil receiver must be a no-op, never panic.
	var nilCfg *Config
	nilCfg.AddGRPCPlugin("g", []string{"run"}, nil)
	nilCfg.AddCommandPlugin("c", nil)
	if nilCfg.RemovePlugin("g") {
		t.Error("nil RemovePlugin should return false")
	}
	if nilCfg.FindPlugin("g") != nil || nilCfg.ListPlugins() != nil {
		t.Error("nil FindPlugin/ListPlugins should be empty")
	}
	if err := nilCfg.SavePlugins(); err == nil {
		t.Error("nil SavePlugins should error")
	}

	cfg.AddGRPCPlugin("srv", []string{"plugin", "-m"}, map[string]string{"K": "V"})
	cfg.AddCommandPlugin("cmd", []PluginCommandConfig{{}})
	if len(cfg.ListPlugins()) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(cfg.ListPlugins()))
	}

	// Replace-by-name: adding same name must not duplicate.
	cfg.AddGRPCPlugin("srv", []string{"plugin2"}, nil)
	p := cfg.FindPlugin("srv")
	if p == nil || p.Type != "grpc" || len(p.Command) != 1 || p.Command[0] != "plugin2" {
		t.Fatalf("expected replaced grpc plugin, got %+v", p)
	}
	if len(cfg.ListPlugins()) != 2 {
		t.Errorf("replace should keep list at 2, got %d", len(cfg.ListPlugins()))
	}

	if !cfg.RemovePlugin("cmd") {
		t.Error("RemovePlugin should report found")
	}
	if cfg.RemovePlugin("cmd") {
		t.Error("second RemovePlugin should report not found")
	}
	if cfg.FindPlugin("cmd") != nil {
		t.Error("removed plugin still findable")
	}
}

func TestValidateGRPCPlugin(t *testing.T) {
	e := &PluginConfigEntry{}
	if err := e.ValidateGRPCPlugin(); err == nil {
		t.Error("empty name should error")
	}
	e.Name = "p"
	e.Type = "command"
	if err := e.ValidateGRPCPlugin(); err == nil {
		t.Error("non-grpc type should error")
	}
	e.Type = "GRPC" // case-insensitive
	if err := e.ValidateGRPCPlugin(); err == nil {
		t.Error("grpc without command should error")
	}
	e.Command = []string{"run"}
	if err := e.ValidateGRPCPlugin(); err != nil {
		t.Errorf("valid plugin errored: %v", err)
	}
}

func TestSavePlugins_RoundTripAndRemoval(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	fp := filepath.Join(dir, "config.yaml")

	cfg := &Config{}
	// Empty file path must be rejected, never write to a default location.
	if err := cfg.SavePlugins(); err == nil {
		t.Error("empty file path should error")
	}
	cfg.FilePath = fp

	cfg.AddGRPCPlugin("srv", []string{"run"}, nil)
	cfg.AddCommandPlugin("cmd", nil)
	if err := cfg.SavePlugins(); err != nil {
		t.Fatalf("SavePlugins: %v", err)
	}

	// Persisted file must contain both entries.
	raw := map[string]interface{}{}
	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	if err := unmarshalYAMLForTest(data, &raw); err != nil {
		t.Fatalf("persisted yaml invalid: %v", err)
	}
	list, ok := raw["plugins"].([]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("expected 2 persisted plugins, got %v", raw["plugins"])
	}

	// Remove all plugins → SavePlugins must delete the key, not write [].
	cfg.RemovePlugin("srv")
	cfg.RemovePlugin("cmd")
	if err := cfg.SavePlugins(); err != nil {
		t.Fatalf("SavePlugins (empty): %v", err)
	}
	data, _ = os.ReadFile(fp)
	if strings.Contains(string(data), "plugins") {
		t.Errorf("expected plugins key removed, got:\n%s", data)
	}
}

// --- config_exposed.go wrappers (was 0%) ---

func TestIsEnvReferenceWrapper(t *testing.T) {
	if name, ok := IsEnvReference("${MY_TOKEN}"); !ok || name != "MY_TOKEN" {
		t.Errorf("expected MY_TOKEN/true, got %q/%v", name, ok)
	}
	if _, ok := IsEnvReference("plain"); ok {
		t.Error("plain value should not be an env reference")
	}
	if _, ok := IsEnvReference(""); ok {
		t.Error("empty value should not be an env reference")
	}
}

func TestIsPlaintextSecretWrapper(t *testing.T) {
	if !IsPlaintextSecret("sk-abc123") {
		t.Error("plaintext should be secret")
	}
	if IsPlaintextSecret("") || IsPlaintextSecret("   ") {
		t.Error("empty should not be secret")
	}
	if IsPlaintextSecret("${VAR}") {
		t.Error("env reference should not be plaintext")
	}
}

func TestLooksLikeSecretFieldWrapper(t *testing.T) {
	if !LooksLikeSecretField("api_password") {
		t.Error("'password' key should be secret-like")
	}
	if LooksLikeSecretField("model") {
		t.Error("'model' should not be secret-like")
	}
}

func TestEnvVarNameBuilders(t *testing.T) {
	if got := IMAdapterSecretEnvVar("My Adapter", "api key"); got != "GGCODE_IM_MY_ADAPTER_API_KEY" {
		t.Errorf("IMAdapterSecretEnvVar = %q", got)
	}
	if got := MCPServerEnvVar("github", "token"); got != "GGCODE_MCP_GITHUB_TOKEN" {
		t.Errorf("MCPServerEnvVar = %q", got)
	}
	if got := MCPServerHeaderEnvVar("github", "auth"); got != "GGCODE_MCP_GITHUB_HEADER_AUTH" {
		t.Errorf("MCPServerHeaderEnvVar = %q", got)
	}
	if got := A2ASecretEnvVar("api key"); got != "GGCODE_A2A_API_KEY" {
		t.Errorf("A2ASecretEnvVar = %q", got)
	}
}

func TestGetSaveScope(t *testing.T) {
	var nilCfg *Config
	if got := nilCfg.GetSaveScope(); got != "global" {
		t.Errorf("nil receiver scope = %q", got)
	}
	if got := (&Config{}).GetSaveScope(); got != "global" {
		t.Errorf("empty scope = %q", got)
	}
	c := &Config{}
	c.saveScope = "instance"
	if got := c.GetSaveScope(); got != "instance" {
		t.Errorf("instance scope = %q", got)
	}
}

func TestWriteKeysEnvMerges(t *testing.T) {
	withTestHome(t)
	if err := WriteKeysEnv(map[string]string{"SA143_A": "v1"}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteKeysEnv(map[string]string{"SA143_B": "v2"}); err != nil {
		t.Fatalf("merge write: %v", err)
	}
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".ggcode", "keys.env"))
	if err != nil {
		t.Fatalf("keys.env missing: %v", err)
	}
	for _, want := range []string{"SA143_A", "v1", "SA143_B", "v2"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("keys.env missing %q after merge, got:\n%s", want, data)
		}
	}
}

// --- config.go Effective* + output style (was 0%) ---

func TestLanChatEffectiveValues(t *testing.T) {
	if got := (LanChatConfig{}).EffectiveDMCooldown(); got != 150*time.Second {
		t.Errorf("default cooldown = %v", got)
	}
	if got := (LanChatConfig{DMCooldown: 30 * time.Second}).EffectiveDMCooldown(); got != 30*time.Second {
		t.Errorf("custom cooldown = %v", got)
	}
	if got := (LanChatConfig{DMCooldown: -1}).EffectiveDMCooldown(); got != 150*time.Second {
		t.Errorf("negative cooldown should default, got %v", got)
	}
	if got := (LanChatConfig{}).EffectiveAPIKey(); got != DefaultA2AAPIKey {
		t.Errorf("default lanchat key = %q", got)
	}
	if got := (LanChatConfig{APIKey: "custom"}).EffectiveAPIKey(); got != "custom" {
		t.Errorf("custom key = %q", got)
	}
}

func TestNotificationEffectiveValues(t *testing.T) {
	if got := (NotificationConfig{}).EffectiveMode(); got != "long" {
		t.Errorf("default mode = %q", got)
	}
	for _, m := range []string{"all", "long", "errors", "off"} {
		if got := (NotificationConfig{Mode: m}).EffectiveMode(); got != m {
			t.Errorf("mode %q got %q", m, got)
		}
	}
	if got := (NotificationConfig{Mode: "bogus"}).EffectiveMode(); got != "long" {
		t.Errorf("bogus mode should default to long, got %q", got)
	}

	if got := (NotificationConfig{}).EffectiveMinDuration(); got != 3 {
		t.Errorf("default min duration = %d", got)
	}
	if got := (NotificationConfig{MinDuration: 10}).EffectiveMinDuration(); got != 10 {
		t.Errorf("custom min duration = %d", got)
	}

	cases := []struct{ in, want int }{{-1, 0}, {0, 5}, {12, 12}}
	for _, c := range cases {
		if got := (NotificationConfig{InputBellDelay: c.in}).EffectiveInputBellDelay(); got != c.want {
			t.Errorf("InputBellDelay %d → %d, want %d", c.in, got, c.want)
		}
	}
}

func TestA2AEffectiveAPIKeyWrapper(t *testing.T) {
	if got := (A2AConfig{}).EffectiveAPIKey(); got != DefaultA2AAPIKey {
		t.Errorf("default a2a key = %q", got)
	}
	if got := (A2AConfig{Auth: A2AAuthConfig{APIKey: "k"}}).EffectiveAPIKey(); got != "k" {
		t.Errorf("custom a2a key = %q", got)
	}
	// Whitespace-only key must fall back to the well-known default.
	if got := (A2AConfig{Auth: A2AAuthConfig{APIKey: "   "}}).EffectiveAPIKey(); got != DefaultA2AAPIKey {
		t.Errorf("blank a2a key should default, got %q", got)
	}
}

func TestOutputStyleCycleReturnsCopy(t *testing.T) {
	cycle := OutputStyleCycle()
	if len(cycle) == 0 || cycle[0] != "" {
		t.Fatalf("unexpected cycle %v", cycle)
	}
	cycle[0] = "mutated" // mutating the returned slice must not affect source
	if again := OutputStyleCycle(); again[0] != "" {
		t.Errorf("OutputStyleCycle leaked internal state: %v", again)
	}
}

func TestExpandAllowedDirs(t *testing.T) {
	cfg := &Config{AllowedDirs: []string{".", "/abs/path", "sub/dir"}}
	got := cfg.ExpandAllowedDirs("/base")
	if len(got) != 3 {
		t.Fatalf("expected 3 dirs, got %v", got)
	}
	if got[0] != "/base" {
		t.Errorf("'.': got %q", got[0])
	}
	if got[1] != "/abs/path" {
		t.Errorf("abs: got %q", got[1])
	}
	if got[2] != filepath.Join("/base", "sub/dir") {
		t.Errorf("relative: got %q", got[2])
	}
	if empty := (&Config{}).ExpandAllowedDirs("/base"); len(empty) != 0 {
		t.Errorf("empty config should yield empty slice, got %v", empty)
	}
}

// --- instance.go SetSaveScope (was 0%) ---

func TestSetSaveScope(t *testing.T) {
	var nilCfg *Config
	if err := nilCfg.SetSaveScope("global"); err == nil {
		t.Error("nil receiver should error")
	}

	c := &Config{}
	if err := c.SetSaveScope(""); err != nil || c.GetSaveScope() != "global" {
		t.Errorf("empty scope: err=%v scope=%q", err, c.GetSaveScope())
	}
	if err := c.SetSaveScope("  GLOBAL "); err != nil || c.GetSaveScope() != "global" {
		t.Errorf("global scope: err=%v scope=%q", err, c.GetSaveScope())
	}
	if err := c.SetSaveScope("instance"); err == nil {
		t.Error("instance scope without workspace should error")
	}
	c.instanceWS = "/tmp/ws"
	if err := c.SetSaveScope("Instance"); err != nil || c.GetSaveScope() != "instance" {
		t.Errorf("instance scope: err=%v scope=%q", err, c.GetSaveScope())
	}
	if err := c.SetSaveScope("bogus"); err == nil {
		t.Error("unknown scope should error")
	}
}

// --- config_save.go SaveKnightEnabled / SaveA2AEnabled / recompact (was 0%) ---

func TestSaveKnightAndA2AEnabled(t *testing.T) {
	withTestHome(t)
	fp := filepath.Join(t.TempDir(), "config.yaml")

	cfg := &Config{FilePath: fp}
	if err := cfg.SaveKnightEnabled(true); err != nil {
		t.Fatalf("SaveKnightEnabled: %v", err)
	}
	if !cfg.KnightConfig.Enabled || !cfg.KnightConfig.HasExplicitEnabled() {
		t.Errorf("in-memory knight state not updated: %+v", cfg.KnightConfig)
	}
	if err := cfg.SaveA2AEnabled(false); err != nil {
		t.Fatalf("SaveA2AEnabled: %v", err)
	}
	if !cfg.A2A.Disabled {
		t.Error("in-memory a2a state not updated")
	}

	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("config file missing: %v", err)
	}
	raw := map[string]interface{}{}
	if err := unmarshalYAMLForTest(data, &raw); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	knight, _ := raw["knight"].(map[string]interface{})
	if knight == nil || knight["enabled"] != true {
		t.Errorf("knight.enabled not persisted: %v", raw["knight"])
	}
	a2a, _ := raw["a2a"].(map[string]interface{})
	if a2a == nil || a2a["disabled"] != true {
		t.Errorf("a2a.disabled not persisted: %v", raw["a2a"])
	}

	// Patch must merge into existing content, preserving unrelated keys.
	cfg2 := &Config{FilePath: fp}
	if err := cfg2.SaveA2AEnabled(true); err != nil {
		t.Fatalf("second save: %v", err)
	}
	data, _ = os.ReadFile(fp)
	raw2 := map[string]interface{}{}
	if err := unmarshalYAMLForTest(data, &raw2); err != nil {
		t.Fatalf("yaml2: %v", err)
	}
	if raw2["knight"] == nil {
		t.Error("re-patch dropped unrelated knight section")
	}
	a2a2, _ := raw2["a2a"].(map[string]interface{})
	if a2a2 == nil || a2a2["disabled"] != false {
		t.Errorf("a2a.disabled should be flipped to false: %v", raw2["a2a"])
	}
}

func TestRecompactConfigFile(t *testing.T) {
	withTestHome(t)
	if err := recompactConfigFile(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("missing file should error")
	}

	// A config file whose content equals the built-in defaults must be
	// stripped down: the default-only vendors map disappears entirely.
	fp := filepath.Join(t.TempDir(), "config.yaml")
	defaultsData, err := yamlMarshalForTest(DefaultConfig())
	if err != nil {
		t.Fatalf("marshal defaults: %v", err)
	}
	if err := os.WriteFile(fp, defaultsData, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := recompactConfigFile(fp); err != nil {
		t.Fatalf("recompact: %v", err)
	}
	data, _ := os.ReadFile(fp)
	raw := map[string]interface{}{}
	if err := unmarshalYAMLForTest(data, &raw); err != nil {
		t.Fatalf("recompacted yaml invalid: %v", err)
	}
	if _, exists := raw["vendors"]; exists {
		t.Error("default-only vendors should have been stripped")
	}

	// Custom (non-default) content must survive recompaction.
	fp2 := filepath.Join(t.TempDir(), "config.yaml")
	custom := []byte("default_mode: bypass\nknight:\n  enabled: true\n")
	if err := os.WriteFile(fp2, custom, 0o600); err != nil {
		t.Fatalf("write custom: %v", err)
	}
	if err := recompactConfigFile(fp2); err != nil {
		t.Fatalf("recompact custom: %v", err)
	}
	data2, _ := os.ReadFile(fp2)
	raw2 := map[string]interface{}{}
	if err := unmarshalYAMLForTest(data2, &raw2); err != nil {
		t.Fatalf("custom yaml invalid: %v", err)
	}
	if raw2["default_mode"] != "bypass" {
		t.Errorf("custom default_mode lost: %v", raw2["default_mode"])
	}
	if raw2["knight"] == nil {
		t.Error("custom knight section lost")
	}
}

// --- vendor_display_i18n.go wrappers (was 0%) ---

func TestLocalizedDisplayWrappers(t *testing.T) {
	// Unknown vendor/endpoint must fall back to the provided English name
	// regardless of language — no invented display names.
	for _, lang := range []string{"en", "zh-CN", "", "fr"} {
		if got := LocalizedVendorDisplay("sa143-vendor", "Sa143 Vendor", lang); got != "Sa143 Vendor" {
			t.Errorf("LocalizedVendorDisplay(%q) = %q", lang, got)
		}
		if got := LocalizedEndpointDisplay("sa143-vendor", "sa143-ep", "Sa143 EP", lang); got != "Sa143 EP" {
			t.Errorf("LocalizedEndpointDisplay(%q) = %q", lang, got)
		}
	}
}

// --- external_files.go LoadMCPServersPublic (was 0%) ---

func TestLoadMCPServersPublic(t *testing.T) {
	// Missing file must not panic; zero servers is the contract.
	if servers := LoadMCPServersPublic(filepath.Join(t.TempDir(), "none.yaml")); len(servers) != 0 {
		t.Errorf("missing file should yield 0 servers, got %d", len(servers))
	}
}

// --- config_vendor.go (was 0%: SetActiveSelection/SaveMCPServers/SetEndpointModelLimits) ---

func testVendorConfig() *Config {
	return &Config{
		Vendor:   "zai",
		Endpoint: "default",
		Model:    "glm-4",
		Vendors: map[string]VendorConfig{
			"zai": {Endpoints: map[string]EndpointConfig{
				"default": {
					Protocol:      "openai",
					DefaultModel:  "glm-4",
					SelectedModel: "glm-4",
				},
			}},
		},
	}
}

func TestActiveEndpointConfig(t *testing.T) {
	var nilCfg *Config
	if nilCfg.ActiveEndpointConfig() != nil {
		t.Error("nil receiver should return nil")
	}
	if (&Config{}).ActiveEndpointConfig() != nil {
		t.Error("missing vendor should return nil")
	}
	cfg := &Config{Vendor: "zai", Vendors: map[string]VendorConfig{
		"zai": {Endpoints: map[string]EndpointConfig{}},
	}}
	if cfg.ActiveEndpointConfig() != nil {
		t.Error("missing endpoint should return nil")
	}

	full := testVendorConfig()
	ep := full.ActiveEndpointConfig()
	if ep == nil || ep.DefaultModel != "glm-4" {
		t.Fatalf("expected resolved endpoint, got %+v", ep)
	}
	// Returned value must be a copy: mutating it must not affect the config.
	ep.DefaultModel = "mutated"
	if full.Vendors["zai"].Endpoints["default"].DefaultModel != "glm-4" {
		t.Error("ActiveEndpointConfig must return a copy, not a reference")
	}
}

func TestSetActiveSelection(t *testing.T) {
	var nilCfg *Config
	if err := nilCfg.SetActiveSelection("zai", "default", "m"); err == nil {
		t.Error("nil receiver should error")
	}

	cfg := testVendorConfig()
	if err := cfg.SetActiveSelection("nope", "default", "m"); err == nil {
		t.Error("unknown vendor should error")
	}
	if err := cfg.SetActiveSelection("zai", "nope", "m"); err == nil {
		t.Error("unknown endpoint should error")
	}
	if err := cfg.SetActiveSelection("zai", "default", ""); err != nil {
		t.Errorf("empty model should fall back to configured model: %v", err)
	}
	if cfg.Model != "glm-4" {
		t.Errorf("fallback model = %q, want glm-4", cfg.Model)
	}

	if err := cfg.SetActiveSelection("zai", "default", "glm-4.6"); err != nil {
		t.Fatalf("explicit model: %v", err)
	}
	if cfg.Model != "glm-4.6" || cfg.Vendor != "zai" || cfg.Endpoint != "default" {
		t.Errorf("selection not applied: %s/%s/%s", cfg.Vendor, cfg.Endpoint, cfg.Model)
	}
	if got := cfg.Vendors["zai"].Endpoints["default"].SelectedModel; got != "glm-4.6" {
		t.Errorf("endpoint SelectedModel = %q", got)
	}

	// Endpoint without any model must reject an empty selection.
	bare := &Config{Vendors: map[string]VendorConfig{
		"v": {Endpoints: map[string]EndpointConfig{"e": {}}},
	}}
	if err := bare.SetActiveSelection("v", "e", ""); err == nil {
		t.Error("endpoint with no model should error on empty selection")
	}
}

func TestSaveMCPServersWrappers(t *testing.T) {
	withTestHome(t)

	var nilCfg *Config
	if err := nilCfg.SaveMCPServers(); err == nil {
		t.Error("nil receiver should error")
	}

	cfg := &Config{}
	cfg.MCPServers = []MCPServerConfig{{Name: "sa143-srv", Type: "stdio", Command: "echo"}}
	if err := cfg.SaveMCPServers(); err != nil {
		t.Fatalf("SaveMCPServers: %v", err)
	}
	path := MCPServersPath(ConfigDir())
	reloaded := LoadMCPServersPublic(path)
	if len(reloaded) != 1 || reloaded[0].Name != "sa143-srv" {
		t.Fatalf("persisted servers = %+v, want [sa143-srv]", reloaded)
	}

	// Scoped wrapper must behave identically for the global scope.
	cfg.MCPServers = append(cfg.MCPServers, MCPServerConfig{Name: "sa143-srv2", Type: "stdio", Command: "true"})
	if err := cfg.SaveMCPServersScoped("global"); err != nil {
		t.Fatalf("SaveMCPServersScoped: %v", err)
	}
	if got := LoadMCPServersPublic(MCPServersPath(ConfigDir())); len(got) != 2 {
		t.Fatalf("scoped persist yielded %d servers, want 2", len(got))
	}

	// Zero servers removes the external file entirely.
	cfg.MCPServers = nil
	if err := cfg.SaveMCPServers(); err != nil {
		t.Fatalf("empty SaveMCPServers: %v", err)
	}
	if fileExists(MCPServersPath(ConfigDir())) {
		t.Error("empty server list should delete mcp_servers.yaml")
	}
}

func TestSetEndpointModelLimits(t *testing.T) {
	withTestHome(t)
	fp := filepath.Join(t.TempDir(), "config.yaml")

	var nilCfg *Config
	if err := nilCfg.SetEndpointModelLimits("zai", "default", 1, 2); err == nil {
		t.Error("nil receiver should error")
	}

	cfg := testVendorConfig()
	cfg.FilePath = fp
	if err := cfg.SetEndpointModelLimits("nope", "default", 1, 2); err == nil {
		t.Error("unknown vendor should error")
	}
	if err := cfg.SetEndpointModelLimits("zai", "nope", 1, 2); err == nil {
		t.Error("unknown endpoint should error")
	}
	if err := cfg.SetEndpointModelLimits("zai", "default", 128000, 8192); err != nil {
		t.Fatalf("SetEndpointModelLimits: %v", err)
	}

	// Limits must be persisted to the external vendors.yaml that sits next
	// to the main config file (Save() strips vendors from the main file).
	data, err := os.ReadFile(filepath.Join(filepath.Dir(fp), "vendors.yaml"))
	if err != nil {
		t.Fatalf("vendors.yaml missing: %v", err)
	}
	raw := map[string]interface{}{}
	if err := unmarshalYAMLForTest(data, &raw); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	zai, _ := raw["zai"].(map[string]interface{})
	eps, _ := zai["endpoints"].(map[string]interface{})
	ep, _ := eps["default"].(map[string]interface{})
	if ep == nil || ep["context_window"] != 128000 || ep["max_tokens"] != 8192 {
		t.Fatalf("limits not persisted: %v\nyaml:\n%s", ep, data)
	}
}
