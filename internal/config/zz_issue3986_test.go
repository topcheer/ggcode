package config

// Pins #3986: the gateway migration must redirect the top-level active
// selection (vendor+endpoint) when it points at a deleted legacy gateway
// vendor - otherwise cfg.Vendor dangles, Validate fails, and Load fail-hards
// the startup with no self-healing path.

import (
	"strings"
	"testing"
)

// TestGatewayMigrationRedirectsActiveVendor pins #3986 main case: yaml with
// a legacy gateway vendor section, a non-empty ai-gateway section, and a
// top-level vendor pointing at the legacy name. After mergeDefaultEndpoints
// the active selection must land on ai-gateway with a resolvable endpoint.
func TestGatewayMigrationRedirectsActiveVendor(t *testing.T) {
	cfg := DefaultConfig()
	def := DefaultConfig()
	cfg.Vendor = "openrouter"
	cfg.Endpoint = "main" // legacy endpoint name; ai-gateway keys it by vendor name
	cfg.Vendors["openrouter"] = VendorConfig{Endpoints: map[string]EndpointConfig{
		"main": {Protocol: "openai", BaseURL: "https://openrouter.ai/api/v1"},
	}}
	cfg.Vendors["ai-gateway"] = VendorConfig{Endpoints: map[string]EndpointConfig{}}

	mergeDefaultEndpoints(cfg, def)

	if cfg.Vendor != "ai-gateway" {
		t.Fatalf("active vendor must be redirected to ai-gateway, got %q", cfg.Vendor)
	}
	if _, ok := cfg.Vendors["openrouter"]; ok {
		t.Fatal("legacy vendor must be deleted from Vendors")
	}
	// The migrated endpoint is keyed by the legacy vendor name.
	if cfg.Endpoint != "openrouter" {
		t.Fatalf("active endpoint must fall back to the migrated endpoint, got %q", cfg.Endpoint)
	}
	// The redirected selection must survive Validate - this is the exact
	// call chain (Validate) that fail-hard-ed startup before the fix.
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate must pass after redirect: %v", err)
	}
}

// TestGatewayMigrationKeepsResolvableEndpoint pins the sub-case where the
// top-level endpoint name already exists under ai-gateway: the redirect must
// keep it instead of clobbering it.
func TestGatewayMigrationKeepsResolvableEndpoint(t *testing.T) {
	cfg := DefaultConfig()
	def := DefaultConfig()
	cfg.Vendor = "novita"
	cfg.Endpoint = "shared" // happens to exist under ai-gateway already
	cfg.Vendors["novita"] = VendorConfig{Endpoints: map[string]EndpointConfig{
		"main": {Protocol: "openai"},
	}}
	cfg.Vendors["ai-gateway"] = VendorConfig{Endpoints: map[string]EndpointConfig{
		"shared": {Protocol: "openai"},
	}}

	mergeDefaultEndpoints(cfg, def)

	if cfg.Vendor != "ai-gateway" {
		t.Fatalf("active vendor must be redirected, got %q", cfg.Vendor)
	}
	if cfg.Endpoint != "shared" {
		t.Fatalf("resolvable endpoint must be kept, got %q", cfg.Endpoint)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate must pass: %v", err)
	}
}

// TestGatewayMigrationInactiveVendorUntouched pins the no-op direction: when
// the active vendor does NOT point at a migrating legacy vendor, migration
// must leave the top-level selection alone.
func TestGatewayMigrationInactiveVendorUntouched(t *testing.T) {
	cfg := DefaultConfig()
	def := DefaultConfig()
	cfg.Vendor = "zai"
	cfg.Endpoint = "bigmodel"
	cfg.Vendors["openrouter"] = VendorConfig{Endpoints: map[string]EndpointConfig{
		"main": {Protocol: "openai"},
	}}
	cfg.Vendors["ai-gateway"] = VendorConfig{Endpoints: map[string]EndpointConfig{}}

	mergeDefaultEndpoints(cfg, def)

	if cfg.Vendor != "zai" || cfg.Endpoint != "bigmodel" {
		t.Fatalf("inactive selection must be untouched, got %s/%s", cfg.Vendor, cfg.Endpoint)
	}
	if _, ok := cfg.Vendors["openrouter"]; ok {
		t.Fatal("legacy vendor must still be migrated away")
	}
}

// TestGatewayMigrationDanglingVendorWouldFail guards the test's own premise:
// without redirection, Validate fails on the dangling vendor (this documents
// the failure mode the fix removes - if Validate ever stops catching it, this
// pin alerts the reviewer that the guard has moved elsewhere).
func TestGatewayMigrationDanglingVendorWouldFail(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendor = "openrouter"
	cfg.Endpoint = "main"
	cfg.Vendors["openrouter"] = VendorConfig{Endpoints: map[string]EndpointConfig{
		"main": {Protocol: "openai"},
	}}
	delete(cfg.Vendors, "openrouter") // simulate the pre-fix dangling state
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("dangling vendor must fail Validate, got: %v", err)
	}
}
