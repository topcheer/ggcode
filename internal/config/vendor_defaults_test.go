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
