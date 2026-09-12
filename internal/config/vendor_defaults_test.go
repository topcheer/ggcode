package config

import (
	"strings"
	"testing"
)

func TestLookupVendorModels(t *testing.T) {
	// Known provider should return models.
	models := lookupVendorModels("openai")
	if len(models) == 0 {
		t.Error("expected models for openai, got empty")
	}
	// Should contain at least one GPT model.
	found := false
	for _, m := range models {
		if strings.HasPrefix(m, "gpt-") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one gpt-* model in openai models list")
	}

	// Another known provider.
	models = lookupVendorModels("anthropic")
	if len(models) == 0 {
		t.Error("expected models for anthropic, got empty")
	}

	models = lookupVendorModels("xiaomi-mimo")
	if len(models) == 0 {
		t.Error("expected models for xiaomi-mimo, got empty")
	}
	if models[0] != "MiMo-V2.5-Pro" {
		t.Fatalf("expected MiMo-V2.5-Pro as first xiaomi-mimo model, got %q", models[0])
	}

	// Unknown provider should return nil.
	models = lookupVendorModels("nonexistent-provider-xyz")
	if models != nil {
		t.Errorf("expected nil for unknown provider, got %v", models)
	}
}

func TestLookupVendorDefaultModel(t *testing.T) {
	// Known provider should return a non-empty default model.
	model := lookupVendorDefaultModel("openai")
	if model == "" {
		t.Error("expected non-empty default model for openai")
	}

	model = lookupVendorDefaultModel("anthropic")
	if model == "" {
		t.Error("expected non-empty default model for anthropic")
	}

	model = lookupVendorDefaultModel("xiaomi-mimo")
	if model != "MiMo-V2.5-Pro" {
		t.Fatalf("expected MiMo-V2.5-Pro for xiaomi-mimo, got %q", model)
	}

	// Unknown provider should return empty string.
	model = lookupVendorDefaultModel("nonexistent-provider-xyz")
	if model != "" {
		t.Errorf("expected empty string for unknown provider, got %q", model)
	}
}

func TestPopulateDefaultModels_FillsEmptyEndpoints(t *testing.T) {
	cfg := DefaultConfig()

	// After populateDefaultModels, known vendors should have models.
	// (DefaultConfig already calls populateDefaultModels internally.)

	// Check openai endpoint
	if ep, ok := cfg.Vendors["openai"].Endpoints["api"]; ok {
		if len(ep.Models) == 0 {
			t.Error("openai/api endpoint should have models after populate")
		}
	}

	// Check anthropic endpoint
	if ep, ok := cfg.Vendors["anthropic"].Endpoints["api"]; ok {
		if len(ep.Models) == 0 {
			t.Error("anthropic/api endpoint should have models after populate")
		}
	}
}

func TestPopulateDefaultModels_DoesNotOverwriteUserModels(t *testing.T) {
	cfg := DefaultConfig()

	// Simulate user setting custom models on an endpoint.
	userModels := []string{"my-custom-model-1", "my-custom-model-2"}
	ep := cfg.Vendors["openai"].Endpoints["api"]
	ep.Models = userModels
	cfg.Vendors["openai"].Endpoints["api"] = ep

	// Run populate again.
	populateDefaultModels(cfg)

	// Should still be user's models.
	got := cfg.Vendors["openai"].Endpoints["api"].Models
	if len(got) != len(userModels) || got[0] != userModels[0] {
		t.Errorf("user models should not be overwritten: got %v, want %v", got, userModels)
	}
}

func TestPopulateDefaultModels_UnknownVendorSkipped(t *testing.T) {
	cfg := &Config{
		Vendors: map[string]VendorConfig{
			"my-custom-vendor": {
				Endpoints: map[string]EndpointConfig{
					"api": {
						DisplayName: "My Custom Vendor",
						Protocol:    "openai",
						BaseURL:     "https://custom.example.com/v1",
					},
				},
			},
		},
	}

	populateDefaultModels(cfg)

	// Unknown vendor should not get models.
	ep := cfg.Vendors["my-custom-vendor"].Endpoints["api"]
	if len(ep.Models) != 0 {
		t.Errorf("unknown vendor should not get models, got %v", ep.Models)
	}
}

func TestPopulateDefaultModels_MergesMultipleCatwalkSources(t *testing.T) {
	// "zai" vendor maps to both "zai" and "zhipu-coding" catwalk providers.
	cfg := DefaultConfig()

	zaiModels := cfg.Vendors["zai"].Endpoints["cn-coding-openai"].Models
	if len(zaiModels) == 0 {
		t.Error("zai endpoint should have models merged from zai + zhipu-coding catwalk providers")
	}

	// Models from both sources should be present.
	t.Logf("zai has %d models", len(zaiModels))
}

func TestPopulateDefaultModels_AllKnownVendorsHaveModels(t *testing.T) {
	cfg := DefaultConfig()

	// Only check vendors that have catwalk or OpenRouter data.
	knownVendors := []string{"openai", "anthropic", "google", "deepseek", "groq", "xai", "mistral", "perplexity", "nvidia", "ark", "xiaomi-mimo"}
	for _, vendor := range knownVendors {
		vc, ok := cfg.Vendors[vendor]
		if !ok {
			continue
		}
		for epName, ep := range vc.Endpoints {
			if len(ep.Models) == 0 {
				t.Errorf("vendor %q endpoint %q should have models after populate", vendor, epName)
			}
		}
	}
}

func TestDefaultConfig_IncludesXiaoMiMIMOEndpoints(t *testing.T) {
	cfg := DefaultConfig()
	vc, ok := cfg.Vendors["xiaomi-mimo"]
	if !ok {
		t.Fatal("expected xiaomi-mimo vendor in default config")
	}
	if vc.DisplayName != "XiaoMi MIMO" {
		t.Fatalf("expected XiaoMi MIMO display name, got %q", vc.DisplayName)
	}

	openaiEP, ok := vc.Endpoints["cn-openai"]
	if !ok {
		t.Fatal("expected cn-openai endpoint")
	}
	if openaiEP.Protocol != "openai" || openaiEP.BaseURL != "https://token-plan-cn.xiaomimimo.com/v1" {
		t.Fatalf("unexpected openai endpoint: %+v", openaiEP)
	}
	if openaiEP.DefaultModel != "MiMo-V2.5-Pro" || len(openaiEP.Models) != 8 {
		t.Fatalf("unexpected openai models: default=%q models=%v", openaiEP.DefaultModel, openaiEP.Models)
	}

	anthropicEP, ok := vc.Endpoints["cn-anthropic"]
	if !ok {
		t.Fatal("expected cn-anthropic endpoint")
	}
	if anthropicEP.Protocol != "anthropic" || anthropicEP.BaseURL != "https://token-plan-cn.xiaomimimo.com/anthropic" {
		t.Fatalf("unexpected anthropic endpoint: %+v", anthropicEP)
	}
	if anthropicEP.DefaultModel != "MiMo-V2.5-Pro" || len(anthropicEP.Models) != 8 {
		t.Fatalf("unexpected anthropic models: default=%q models=%v", anthropicEP.DefaultModel, anthropicEP.Models)
	}
}

func TestMatchProviderByBaseURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		// Exact provider endpoints.
		{"https://api.z.ai/api/coding/paas/v4", "zai"},
		{"https://api.z.ai/api/paas/v4", "zai"}, // same host, different path
		{"https://token-plan-cn.xiaomimimo.com/v1", "xiaomi-mimo"},
		{"https://api.deepseek.com/v1", "deepseek"},
		// Upstream api_endpoint is an env placeholder; host filled from builtin URL.
		{"https://api.anthropic.com", "anthropic"},
		// Host shared by zhipuai + zhipuai-coding-plan: smallest ID wins deterministically.
		{"https://open.bigmodel.cn/api/paas/v4", "zhipuai"},
		// No match cases.
		{"https://custom.example.com/v1", ""},
		{"", ""},
		{"$ANTHROPIC_API_ENDPOINT", ""},
	}
	for _, tt := range tests {
		if got := matchProviderByBaseURL(tt.url); got != tt.want {
			t.Errorf("matchProviderByBaseURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestPopulateDefaultModels_UnknownVendorMatchedByURL(t *testing.T) {
	cfg := &Config{
		Vendors: map[string]VendorConfig{
			"my-gateway": {
				Endpoints: map[string]EndpointConfig{
					"api": {
						DisplayName: "My Gateway",
						Protocol:    "openai",
						BaseURL:     "https://api.z.ai/api/coding/paas/v4",
					},
				},
			},
		},
	}

	populateDefaultModels(cfg)

	models := cfg.Vendors["my-gateway"].Endpoints["api"].Models
	if len(models) == 0 {
		t.Fatal("expected zai model list to be filled via URL host match")
	}
	if models[0] == "" {
		t.Fatal("unexpected empty first model")
	}
}

// Regression for #1525: zai/zhipu-coding and minimax/minimax-china alias
// pairs carry byte-identical model lists - merge by name or every model
// shows twice in the model panel; and the "kimi" catwalk key never existed.
func TestPopulateDefaultModelsNoDuplicates(t *testing.T) {
	cfg := &Config{Vendors: map[string]VendorConfig{
		"zai": {Endpoints: map[string]EndpointConfig{
			"e1": {BaseURL: "https://api.z.ai"},
		}},
	}}
	populateDefaultModels(cfg)
	models := cfg.Vendors["zai"].Endpoints["e1"].Models
	seen := map[string]bool{}
	for _, m := range models {
		if seen[m] {
			t.Fatalf("duplicate model %q in populated list (len=%d)", m, len(models))
		}
		seen[m] = true
	}
	if len(models) == 0 {
		t.Fatal("expected populated models")
	}
}

// #2157: populateDefaultModels must hand out COPIES of the package-level
// registry lists. The ai-gateway and unknown-vendor branches used to assign
// lookupVendorModels(pid) directly, sharing the backing array across Configs -
// an in-place ep.Models[i] write (expandEnvWithLookup) then raced concurrent
// Loads and cross-contaminated the global model table.
func TestPopulateDefaultModels_DoesNotShareRegistryBackingArray(t *testing.T) {
	cfg := DefaultConfig()
	// Find a built-in endpoint URL whose host maps to a provider with a
	// registry model list (e.g. zai's coding endpoint).
	var probeURL string
	for _, ep := range cfg.Vendors["zai"].Endpoints {
		if len(lookupVendorModels(matchProviderByBaseURL(ep.BaseURL))) > 0 {
			probeURL = ep.BaseURL
			break
		}
	}
	if probeURL == "" {
		t.Fatal("no built-in endpoint URL maps to a provider with registry models")
	}
	// An UNKNOWN vendor name (not in vendorToProvider) with a provider-matched
	// URL drives the #1668 per-endpoint branch that used to alias the registry.
	cfg.Vendors["my-unknown-relay"] = defaultVendor("My Relay", "${MY_RELAY_KEY}", map[string]EndpointConfig{
		"gw": {BaseURL: probeURL, Protocol: "openai"},
	})
	populateDefaultModels(cfg)
	ep := cfg.Vendors["my-unknown-relay"].Endpoints["gw"]
	if len(ep.Models) == 0 {
		t.Fatal("unknown-vendor endpoint should be populated from its URL's provider")
	}
	pid := matchProviderByBaseURL(probeURL)
	ep.Models[0] = "zz-#2157-mutated"
	if lookupVendorModels(pid)[0] == "zz-#2157-mutated" {
		t.Fatal("endpoint Models shares backing array with the package registry (#2157 regression)")
	}
}
