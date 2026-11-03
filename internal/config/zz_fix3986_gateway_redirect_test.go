package config

import (
	"sort"
	"testing"
)

func TestFix3986MigrationRedirectsActiveVendor(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendors["openrouter"] = VendorConfig{
		Endpoints: map[string]EndpointConfig{
			"main": {Protocol: "openai", BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "m"},
		},
	}
	cfg.Vendor = "openrouter"
	cfg.Endpoint = "main"
	cfg.Model = "some-model"

	mergeDefaultEndpoints(cfg, DefaultConfig())

	if cfg.Vendor != "ai-gateway" {
		t.Fatalf("active vendor must be redirected to ai-gateway, got %q", cfg.Vendor)
	}
	if cfg.Endpoint == "main" {
		t.Fatalf("dangling legacy endpoint name must be replaced, got %q", cfg.Endpoint)
	}

	gw := cfg.Vendors["ai-gateway"]
	eps := make([]string, 0, len(gw.Endpoints))
	for k := range gw.Endpoints {
		eps = append(eps, k)
	}
	sort.Strings(eps)
	if len(eps) > 0 && cfg.Endpoint != eps[0] {
		t.Fatalf("endpoint fallback must be the lowest ai-gateway endpoint %q, got %q", eps[0], cfg.Endpoint)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate must pass after redirect: %v", err)
	}
	if _, still := cfg.Vendors["openrouter"]; still {
		t.Fatal("legacy openrouter vendor must be deleted")
	}
}

func TestFix3986NonLegacyActiveVendorUntouched(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendors["openrouter"] = VendorConfig{
		Endpoints: map[string]EndpointConfig{
			"main": {Protocol: "openai", BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "m"},
		},
	}
	cfg.Vendor = "zai"
	cfg.Endpoint = "cn-coding-openai"
	cfg.Model = "glm-5"

	mergeDefaultEndpoints(cfg, DefaultConfig())

	if cfg.Vendor != "zai" || cfg.Endpoint != "cn-coding-openai" {
		t.Fatalf("non-legacy active selection must be untouched, got %q/%q", cfg.Vendor, cfg.Endpoint)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate must still pass: %v", err)
	}
}
