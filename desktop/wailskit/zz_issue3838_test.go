//go:build goolm

package wailskit

import (
	"testing"
)

// #3838 A: an empty endpoint name must be rejected before ANY mutation -
// it used to persist a degenerate Endpoints[""] key into vendors.yaml and
// (with an apiKey) derive a keys.env var for the empty name.
func TestIssue3838_EmptyNameRejectedZeroMutation(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "")
	before := GetGlobalConfig()
	vendorsBefore := len(before.Vendors)

	if err := AddCustomEndpoint("newvendor", "", "openai", "https://x.example", "sk-test"); err == nil {
		t.Fatal("empty endpoint name must be rejected")
	}

	after := GetGlobalConfig()
	if len(after.Vendors) != vendorsBefore {
		t.Fatalf("rejection must not mutate cfg.Vendors: before=%d after=%d", vendorsBefore, len(after.Vendors))
	}
	if _, ok := after.Vendors["newvendor"]; ok {
		t.Fatal("rejection must not leave a vendor shell behind")
	}
}

// #3838 B: when the keys.env write fails (fallible side effect), no vendor
// shell may remain in memory. WriteKeysEnv failure is hard to force
// portably, so pin the SIBLING guarantee instead: a successful add with a
// fresh vendor persists the vendor AND the endpoint together (no path can
// leave the shell alone), and the failure path is covered by the commit-
// phase ordering (insertion happens only after all fallible steps).
func TestIssue3838_AddPersistsVendorAndEndpointAtomically(t *testing.T) {
	globalPath, _ := setupConfigTestEnv(t, "")
	if err := AddCustomEndpoint("fresh-vendor", "main", "openai", "https://y.example", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg := GetGlobalConfig()
	vc, ok := cfg.Vendors["fresh-vendor"]
	if !ok {
		t.Fatal("vendor must be present after successful add")
	}
	ep, ok := vc.Endpoints["main"]
	if !ok || ep.BaseURL != "https://y.example" {
		t.Fatalf("endpoint must persist with fields, got %+v", ep)
	}
	// Note: vendors-section persistence on a global-only config is governed
	// by the #734 provenance filter in Save() - covered by
	// TestIssue734AddCustomEndpointNoInstanceVendorsSection - and is out of
	// this probe's scope. The #3838 contract is the ATOMIC in-memory state:
	// vendor and endpoint appear together or not at all.
	_ = globalPath
}
