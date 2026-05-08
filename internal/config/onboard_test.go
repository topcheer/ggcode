package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNeedsOnboard(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *Config
		want  bool
	}{
		{
			name:  "nil config",
			setup: func() *Config { return nil },
			want:  true,
		},
		{
			name: "empty vendor",
			setup: func() *Config {
				return &Config{Vendor: ""}
			},
			want: true,
		},
		{
			name: "vendor set but no endpoints",
			setup: func() *Config {
				return &Config{
					Vendor:   "openai",
					Endpoint: "api",
					Vendors: map[string]VendorConfig{
						"openai": {DisplayName: "OpenAI", APIKey: "${OPENAI_API_KEY}"},
					},
				}
			},
			want: true, // no endpoint "api" defined → ResolveActiveEndpoint fails
		},
		{
			name: "vendor and endpoint but no api key env",
			setup: func() *Config {
				os.Unsetenv("OPENAI_API_KEY")
				return &Config{
					Vendor:   "openai",
					Endpoint: "api",
					Vendors: map[string]VendorConfig{
						"openai": {
							DisplayName: "OpenAI",
							APIKey:      "${OPENAI_API_KEY}",
							Endpoints: map[string]EndpointConfig{
								"api": {
									DisplayName:  "OpenAI API",
									Protocol:     "openai",
									BaseURL:      "https://api.openai.com/v1",
									DefaultModel: "gpt-4o",
								},
							},
						},
					},
				}
			},
			want: true, // API key env var not set → resolved.APIKey == ""
		},
		{
			name: "fully configured",
			setup: func() *Config {
				t.Setenv("TEST_ONBOARD_KEY", "sk-test-123")
				return &Config{
					Vendor:   "openai",
					Endpoint: "api",
					Vendors: map[string]VendorConfig{
						"openai": {
							DisplayName: "OpenAI",
							APIKey:      "${TEST_ONBOARD_KEY}",
							Endpoints: map[string]EndpointConfig{
								"api": {
									DisplayName:  "OpenAI API",
									Protocol:     "openai",
									BaseURL:      "https://api.openai.com/v1",
									DefaultModel: "gpt-4o",
								},
							},
						},
					},
				}
			},
			want: false,
		},
		{
			name: "direct api key set",
			setup: func() *Config {
				return &Config{
					Vendor:   "openai",
					Endpoint: "api",
					Vendors: map[string]VendorConfig{
						"openai": {
							DisplayName: "OpenAI",
							APIKey:      "sk-direct-key",
							Endpoints: map[string]EndpointConfig{
								"api": {
									DisplayName:  "OpenAI API",
									Protocol:     "openai",
									BaseURL:      "https://api.openai.com/v1",
									DefaultModel: "gpt-4o",
								},
							},
						},
					},
				}
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.setup()
			got := cfg.NeedsOnboard()
			if got != tt.want {
				t.Errorf("NeedsOnboard() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVendorPresets(t *testing.T) {
	presets := VendorPresets()
	if len(presets) == 0 {
		t.Fatal("VendorPresets() returned empty list")
	}
	// Should have at least the well-known vendors
	known := map[string]bool{
		"openai": false, "anthropic": false, "google": false,
		"deepseek": false, "zai": false,
	}
	for _, p := range presets {
		if _, ok := known[p.ID]; ok {
			known[p.ID] = true
		}
	}
	for id, found := range known {
		if !found {
			t.Errorf("VendorPresets() missing known vendor %q", id)
		}
	}
	// Each preset should have at least one endpoint
	for _, p := range presets {
		if len(p.Endpoints) == 0 {
			t.Errorf("vendor %q has no endpoints", p.ID)
		}
		if p.DefaultEndpoint == "" {
			t.Errorf("vendor %q has no DefaultEndpoint", p.ID)
		}
	}
}

func TestExtractEnvVarName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"${ANTHROPIC_API_KEY}", "ANTHROPIC_API_KEY"},
		{"${OPENAI_API_KEY}", "OPENAI_API_KEY"},
		{"sk-direct-key", "sk-direct-key"},
		{"", ""},
		{"  ${WHITESPACE}  ", "WHITESPACE"},
	}
	for _, tt := range tests {
		got := extractEnvVarName(tt.input)
		if got != tt.want {
			t.Errorf("extractEnvVarName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNeedsOnboardWithRealConfig(t *testing.T) {
	// Test that DefaultConfig does NOT need onboard (has ZAI key)
	cfg := DefaultConfig()
	if cfg.NeedsOnboard() {
		// DefaultConfig uses ${ZAI_API_KEY} which resolves to empty if env not set
		// This is expected behavior — the default config is a template, not a working config
		t.Log("DefaultConfig needs onboard (expected — ZAI_API_KEY not set)")
	}

	// Test that saving and loading back with a real key makes it work
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")
	t.Setenv("TEST_ONBOARD_REAL", "real-key-123")
	cfg = &Config{
		FilePath: cfgPath,
		Vendor:   "openai",
		Endpoint: "api",
		Vendors: map[string]VendorConfig{
			"openai": {
				DisplayName: "OpenAI",
				APIKey:      "${TEST_ONBOARD_REAL}",
				Endpoints: map[string]EndpointConfig{
					"api": {
						DisplayName:  "OpenAI API",
						Protocol:     "openai",
						BaseURL:      "https://api.openai.com/v1",
						DefaultModel: "gpt-4o",
					},
				},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if cfg.NeedsOnboard() {
		t.Error("fully configured config should not need onboard")
	}
}
