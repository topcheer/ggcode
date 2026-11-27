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
			want: true,
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
			want: true,
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
	withTestHome(t)

	cfg := DefaultConfig()
	if cfg.NeedsOnboard() {

		t.Log("DefaultConfig needs onboard (expected — ZAI_API_KEY not set)")
	}

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

func TestNeedsOnboardEmptyHome(t *testing.T) {
	withTestHome(t)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	for _, key := range []string{
		"ZAI_API_KEY", "GGCODE_ZAI_API_KEY", "OPENAI_API_KEY",
		"ANTHROPIC_API_KEY", "GEMINI_API_KEY", "DEEPSEEK_API_KEY",
		"OPENROUTER_API_KEY", "GROQ_API_KEY", "MISTRAL_API_KEY",
	} {
		os.Unsetenv(key)
	}

	cfg := DefaultConfig()
	cfg.FilePath = filepath.Join(tmpHome, ".ggcode", "ggcode.yaml")

	if !cfg.NeedsOnboard() {
		t.Error("DefaultConfig with all env vars cleared should need onboard")
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	if cfg.NeedsOnboard() {
		t.Error("saved config file on disk must never re-onboard (owner invariant #1865)")
	}
}

func TestNeedsOnboardPartialConfig(t *testing.T) {

	tmpHome := t.TempDir()
	cfgPath := filepath.Join(tmpHome, ".ggcode", "ggcode.yaml")
	os.Unsetenv("ANTHROPIC_API_KEY")

	cfg := &Config{
		FilePath: cfgPath,
		Vendor:   "anthropic",
		Endpoint: "api",
		Vendors: map[string]VendorConfig{
			"anthropic": {
				DisplayName: "Anthropic",
				APIKey:      "${ANTHROPIC_API_KEY}",
				Endpoints: map[string]EndpointConfig{
					"api": {
						DisplayName:  "Anthropic API",
						Protocol:     "anthropic",
						BaseURL:      "https://api.anthropic.com",
						DefaultModel: "claude-sonnet-4-20250514",
					},
				},
			},
		},
	}
	if !cfg.NeedsOnboard() {
		t.Error("config with unset env var should need onboard")
	}

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-123")
	if cfg.NeedsOnboard() {
		t.Error("config with set env var should NOT need onboard")
	}
}

func TestVendorPresetsZaiNeedsKey(t *testing.T) {
	presets := VendorPresets()
	for _, p := range presets {
		if p.ID == "zai" {
			if !p.NeedsAPIKey {
				t.Errorf("zai vendor NeedsAPIKey=false, want true")
			}
			return
		}
	}
	t.Fatal("zai vendor not found in presets")
}

func TestDefaultConfigZaiAPIKey(t *testing.T) {
	dc := DefaultConfig()
	vc, ok := dc.Vendors["zai"]
	if !ok {
		t.Fatal("zai vendor not in DefaultConfig")
	}
	if vc.APIKey == "" {
		t.Error("zai vendor APIKey is empty")
	}
	t.Logf("zai APIKey=%q", vc.APIKey)
}

func TestVendorPresetsNeedsAPIKey(t *testing.T) {
	presets := VendorPresets()
	known := map[string]bool{
		"openai":    true,
		"anthropic": true,
		"deepseek":  true,
		"zai":       true,
		"groq":      true,
	}
	for _, p := range presets {
		if expected, ok := known[p.ID]; ok {
			if p.NeedsAPIKey != expected {
				t.Errorf("vendor %q NeedsAPIKey=%v, want %v", p.ID, p.NeedsAPIKey, expected)
			}
		}
	}

	for _, p := range presets {
		if p.ID == "github-copilot" {
			if p.NeedsAPIKey {
				t.Error("github-copilot should not need API key")
			}
		}
	}
}

func TestVendorPresetDefaultEndpointOfficial1712(t *testing.T) {
	presets := VendorPresets()
	var copilot *VendorPreset
	for i := range presets {
		if presets[i].ID == "github-copilot" {
			copilot = &presets[i]
		}
	}
	if copilot == nil {
		t.Fatal("github-copilot preset missing")
	}
	if copilot.DefaultEndpoint != "github.com" {
		t.Fatalf("DefaultEndpoint = %q, want github.com (not the enterprise placeholder)", copilot.DefaultEndpoint)
	}

	for i := 0; i < 20; i++ {
		if again := VendorPresets(); again != nil {
			for _, vp := range again {
				if vp.ID == "github-copilot" && vp.DefaultEndpoint != "github.com" {
					t.Fatalf("DefaultEndpoint unstable: %q", vp.DefaultEndpoint)
				}
			}
		}
	}
}

// Owner invariant #1865 keeps a config file on disk from re-triggering
// onboarding automatically - but an onboarding run that never persisted the key
// leaves a marker, and that marker is what makes the reported failure (401
// "API 密钥无效或未配置" with no way back) recoverable.
func TestNeedsOnboard_IncompleteMarkerReopensOnboarding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	os.Unsetenv("DEEPSEEK_API_KEY")

	cfgPath := filepath.Join(home, ".ggcode", "ggcode.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// What the broken onboarding left behind: selection saved, key missing.
	body := "vendor: deepseek\nendpoint: api\nmodel: deepseek-chat\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// #1865: a plain config file never re-onboards on its own.
	if cfg.NeedsOnboard() {
		t.Fatal("a config file on disk must not re-onboard automatically (owner invariant #1865)")
	}
	if cfg.OnboardIncomplete {
		t.Fatal("NeedsOnboard must not mark a user-written config as an incomplete onboarding")
	}

	// The marker an onboarding run leaves when it could not persist the key.
	cfg.OnboardIncomplete = true
	if !cfg.NeedsOnboard() {
		t.Fatal("an incomplete onboarding must re-open onboarding - otherwise the user is stuck with 401")
	}

	// Once a key is available the marker self-heals (no nagging).
	t.Setenv("DEEPSEEK_API_KEY", "sk-from-env")
	cfg.OnboardIncomplete = true
	if cfg.NeedsOnboard() {
		t.Fatal("a usable key must end the onboarding nag")
	}
	if cfg.OnboardIncomplete {
		t.Error("the marker must be cleared once the endpoint is usable")
	}
}

// The escape hatches: none of these may push a working setup into onboarding.
func TestNeedsOnboard_EscapesAndKeylessEndpoints(t *testing.T) {
	base := func(vendor, baseURL, authType string) *Config {
		return &Config{
			Vendor:   "v",
			Endpoint: "api",
			Vendors: map[string]VendorConfig{
				"v": {
					DisplayName: "V",
					Endpoints: map[string]EndpointConfig{
						"api": {DisplayName: "V API", Protocol: "openai", BaseURL: baseURL, DefaultModel: "m", AuthType: authType},
					},
				},
			},
		}
	}

	t.Run("loopback base url needs no key", func(t *testing.T) {
		cfg := base("v", "http://127.0.0.1:11434/v1", "")
		if cfg.NeedsOnboard() {
			t.Error("a loopback endpoint (local llama.cpp / Ollama) must not force onboarding")
		}
	})
	t.Run("keyless auth type needs no key", func(t *testing.T) {
		cfg := base("v", "https://proxy.internal/v1", "none")
		if cfg.NeedsOnboard() {
			t.Error("auth_type none must not force onboarding")
		}
	})
	t.Run("oauth endpoint needs no api key", func(t *testing.T) {
		cfg := base("v", "https://api.example.com/v1", "oauth")
		if cfg.NeedsOnboard() {
			t.Error("oauth endpoints carry a token, not an API key")
		}
	})
	t.Run("skip autoconfig wins", func(t *testing.T) {
		t.Setenv("GGCODE_SKIP_AUTOCONFIG", "1")
		cfg := base("v", "https://api.example.com/v1", "")
		if cfg.NeedsOnboard() {
			t.Error("GGCODE_SKIP_AUTOCONFIG must disable the onboarding prompt")
		}
	})
	t.Run("usable key clears the incomplete marker", func(t *testing.T) {
		cfg := base("v", "https://api.example.com/v1", "")
		vc := cfg.Vendors["v"]
		vc.APIKey = "sk-present"
		cfg.Vendors["v"] = vc
		if cfg.NeedsOnboard() {
			t.Fatal("a usable key must not onboard")
		}
		cfg.OnboardIncomplete = true
		if cfg.NeedsOnboard() {
			t.Error("with a usable key the marker must self-heal instead of nagging")
		}
		if cfg.OnboardIncomplete {
			t.Error("the marker must be cleared when the endpoint is usable")
		}
	})
}

// The good path: an onboarding that did persist the key must not re-onboard on
// the next start (that would be an infinite onboarding loop).
func TestOnboardThenReloadDoesNotReonboard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	os.Unsetenv("DEEPSEEK_API_KEY")

	cfgPath := filepath.Join(home, ".ggcode", "ggcode.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.FilePath = cfgPath
	vc, ok := cfg.Vendors["deepseek"]
	if !ok {
		t.Fatal("deepseek preset missing from DefaultConfig")
	}
	vc.APIKey = "sk-onboarded-key"
	cfg.Vendors["deepseek"] = vc
	ep := vc.Endpoints["api"]
	ep.SelectedModel = "deepseek-chat"
	vc.Endpoints["api"] = ep
	cfg.Vendors["deepseek"] = vc
	cfg.Vendor, cfg.Endpoint, cfg.Model = "deepseek", "api", "deepseek-chat"
	cfg.FirstRun = false
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.NeedsOnboard() {
		t.Fatal("a persisted key must not trigger onboarding again on the next start")
	}
	if got := reloaded.Vendors["deepseek"].APIKey; got != "sk-onboarded-key" {
		t.Fatalf("key lost across save+load: got %q", got)
	}
}
