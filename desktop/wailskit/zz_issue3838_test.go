//go:build goolm

package wailskit

import (
	"testing"
)

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

	_ = globalPath
}
