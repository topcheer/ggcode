package config

import (
	"sort"
	"testing"
)

// #3986: when the gateway migration deletes a legacy vendor that the
// top-level active selection points at, the selection must be redirected to
// ai-gateway (with a deterministic endpoint fallback) - otherwise cfg.Vendor
// dangles and the next Load fails Validate() with no self-healing path.
func TestFix3986MigrationRedirectsActiveVendor(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendors["openrouter"] = VendorConfig{
		Endpoints: map[string]EndpointConfig{
			"main": {Protocol: "openai", BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "m"},
		},
	}
	cfg.Vendor = "openrouter"
	cfg.Endpoint = "main" // legacy endpoint name, absent under ai-gateway
	cfg.Model = "some-model"

	mergeDefaultEndpoints(cfg, DefaultConfig())

	if cfg.Vendor != "ai-gateway" {
		t.Fatalf("active vendor must be redirected to ai-gateway, got %q", cfg.Vendor)
	}
	if cfg.Endpoint == "main" {
		t.Fatalf("dangling legacy endpoint name must be replaced, got %q", cfg.Endpoint)
	}
	// Deterministic fallback (#1422-B2): lowest endpoint name in ai-gateway.
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

// Control: an active vendor that is NOT a legacy gateway vendor must be left
// untouched by the migration.
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
