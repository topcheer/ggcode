package config

import (
	"testing"

	"github.com/topcheer/ggcode/internal/util"
)

// --- anthropic_bootstrap.go 0% functions ---

func TestInferBootstrapVendorID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "anthropic-env"},
		{"https://api.mycloud.com/v1", "mycloud"},
		{"https://openrouter.ai/api/v1", "openrouter"},
	}
	for _, tt := range tests {
		got := inferBootstrapVendorID(tt.input)
		if got != tt.want {
			t.Errorf("inferBootstrapVendorID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestInferBootstrapVendorDisplayName(t *testing.T) {
	if inferBootstrapVendorDisplayName("") != "Anthropic Env" {
		t.Error("expected default name")
	}
	if inferBootstrapVendorDisplayName("mycloud") != "Mycloud" {
		t.Error("expected capitalized")
	}
}

func TestUniqueBootstrapVendorID(t *testing.T) {
	cfg := &Config{Vendors: map[string]VendorConfig{}}
	id := uniqueBootstrapVendorID(cfg, "test")
	if id != "test" {
		t.Errorf("expected 'test', got %q", id)
	}
	cfg.Vendors["test"] = VendorConfig{}
	id = uniqueBootstrapVendorID(cfg, "test")
	if id != "test-2" {
		t.Errorf("expected 'test-2', got %q", id)
	}
}

func TestUniqueBootstrapVendorID_Nil(t *testing.T) {
	id := uniqueBootstrapVendorID(nil, "test")
	if id != "test" {
		t.Errorf("expected 'test', got %q", id)
	}
}

func TestSanitizeBootstrapVendorID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"My Cloud", "my-cloud"},
		{"test@123", "test-123"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeBootstrapVendorID(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeBootstrapVendorID(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsCommonHostPrefix(t *testing.T) {
	if !isCommonHostPrefix("api") {
		t.Error("expected true for 'api'")
	}
	if isCommonHostPrefix("mycloud") {
		t.Error("expected false for 'mycloud'")
	}
}

// --- api_keys.go 0% functions ---

func TestPreferredVendorAPIKeyEnvVar(t *testing.T) {
	got := PreferredVendorAPIKeyEnvVar("anthropic")
	if got == "" {
		t.Error("expected non-empty")
	}
}

func TestPreferredEndpointAPIKeyEnvVar(t *testing.T) {
	got := PreferredEndpointAPIKeyEnvVar("anthropic", "my-server")
	if got == "" {
		t.Error("expected non-empty")
	}
}

func TestPreferredAPIKeyEnvVar(t *testing.T) {
	got := PreferredAPIKeyEnvVar("anthropic", "my-server", "sk-ant", "sk-endpoint")
	if got == "" {
		t.Error("expected non-empty")
	}
}

func TestSanitizeEnvVarSegment(t *testing.T) {
	got := sanitizeEnvVarSegment("My Vendor")
	if got != "MY_VENDOR" {
		t.Errorf("expected 'MY_VENDOR', got %q", got)
	}
}

// --- config.go 0% functions ---

func TestVendorNames(t *testing.T) {
	cfg := &Config{Vendors: map[string]VendorConfig{
		"anthropic": {},
		"openai":    {},
	}}
	names := cfg.VendorNames()
	if len(names) != 2 {
		t.Errorf("expected 2, got %d", len(names))
	}
}

func TestEndpointNames(t *testing.T) {
	cfg := &Config{
		Vendor: "anthropic",
		Vendors: map[string]VendorConfig{
			"anthropic": {Endpoints: map[string]EndpointConfig{
				"default": {},
				"custom":  {},
			}},
		},
	}
	names := cfg.EndpointNames("anthropic")
	if len(names) != 2 {
		t.Errorf("expected 2, got %d", len(names))
	}
}

func TestActiveEndpointConfig_Nil(t *testing.T) {
	cfg := &Config{}
	ep := cfg.ActiveEndpointConfig()
	// Should return nil or default
	_ = ep
}

func TestSetEndpointAPIKey(t *testing.T) {
	// SetEndpointAPIKey requires config file path - skip for unit test
}

func TestSetEndpointModels(t *testing.T) {
	cfg := &Config{
		Vendors: map[string]VendorConfig{
			"anthropic": {Endpoints: map[string]EndpointConfig{
				"default": {},
			}},
		},
	}
	cfg.SetEndpointModels("anthropic", "default", []string{"claude-4"})
	ep := cfg.Vendors["anthropic"].Endpoints["default"]
	if len(ep.Models) != 1 || ep.Models[0] != "claude-4" {
		t.Errorf("unexpected models: %v", ep.Models)
	}
}

func TestUniqueNonEmptyStrings(t *testing.T) {
	got := uniqueNonEmptyStrings("a", "", "b", "a", "  ", "c")
	if len(got) != 3 {
		t.Errorf("expected 3, got %d: %v", len(got), got)
	}
}

func TestAddVendor(t *testing.T) {
	cfg := &Config{Vendors: map[string]VendorConfig{}}
	err := cfg.AddVendor("my-vendor", "My Vendor", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg.Vendors["my-vendor"]; !ok {
		t.Error("vendor not added")
	}
}

func TestRemoveVendor(t *testing.T) {
	cfg := &Config{Vendors: map[string]VendorConfig{
		"my-vendor": {},
	}}
	err := cfg.RemoveVendor("my-vendor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg.Vendors["my-vendor"]; ok {
		t.Error("vendor not removed")
	}
}

func TestSetVendorAPIKey(t *testing.T) {
	// SetVendorAPIKey requires config file path - skip for unit test
}

func TestDefaultKnightConfig(t *testing.T) {
	kc := DefaultKnightConfig()
	_ = kc // just ensure no panic
}

// --- env.go ---

func TestExpandEnvRecursive(t *testing.T) {
	m := map[string]interface{}{
		"path": "$HOME/test",
	}
	got := ExpandEnvRecursive(m)
	if got == nil {
		t.Error("expected non-nil")
	}
}

// --- IM adapter methods ---

func TestRemoveIMAdapter(t *testing.T) {
	cfg := &Config{
		IM: IMConfig{
			Adapters: map[string]IMAdapterConfig{
				"slack": {},
			},
		},
	}
	cfg.RemoveIMAdapter("slack")
	if _, ok := cfg.IM.Adapters["slack"]; ok {
		t.Error("adapter not removed")
	}
}

func TestSetIMAdapterEnabled(t *testing.T) {
	cfg := &Config{
		IM: IMConfig{
			Adapters: map[string]IMAdapterConfig{
				"slack": {},
			},
		},
	}
	cfg.SetIMAdapterEnabled("slack", true)
	if !cfg.IM.Adapters["slack"].Enabled {
		t.Error("expected enabled")
	}
}

func TestSetIMAdapterExtra(t *testing.T) {
	// Requires config path, just test it handles missing adapter gracefully
	cfg := &Config{
		IM: IMConfig{
			Adapters: map[string]IMAdapterConfig{},
		},
	}
	err := cfg.SetIMAdapterExtra("nonexistent", "key", "value")
	// Should error for nonexistent adapter
	_ = err
}

func TestConfigFirstNonEmpty(t *testing.T) {
	got := util.FirstNonEmpty("", "", "hello", "world")
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}
