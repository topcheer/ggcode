package config

import "testing"

func TestIssue4633_MigrationCarriesAllEndpoints(t *testing.T) {
	cfg := &Config{
		Vendors: map[string]VendorConfig{
			"ai-gateway": {Endpoints: map[string]EndpointConfig{}},
			"openrouter": {
				APIKey: "VENDOR-LEVEL-KEY",
				Endpoints: map[string]EndpointConfig{
					"a-first": {APIKey: "SECRET-A", BaseURL: "https://a.example"},
					"b-mid":   {APIKey: "SECRET-B", BaseURL: "https://b.example"},
					"c-last":  {APIKey: "SECRET-C", BaseURL: "https://c.example"},
				},
			},
		},
	}
	mergeDefaultEndpoints(cfg, DefaultConfig())

	if _, ok := cfg.Vendors["openrouter"]; ok {
		t.Fatal("legacy vendor must be migrated away")
	}
	ag := cfg.Vendors["ai-gateway"]

	if got := ag.Endpoints["openrouter"].APIKey; got != "VENDOR-LEVEL-KEY" {
		t.Fatalf("primary endpoint key = %q, want VENDOR-LEVEL-KEY (#959 placement)", got)
	}

	for name, want := range map[string]string{
		"openrouter-a-first": "SECRET-A",
		"openrouter-b-mid":   "SECRET-B",
		"openrouter-c-last":  "SECRET-C",
	} {
		ep, ok := ag.Endpoints[name]
		if !ok {
			t.Fatalf("namespaced endpoint %s missing - credential irreversibly dropped (#4633)", name)
		}
		if ep.APIKey != want {
			t.Fatalf("endpoint %s key = %q, want %q", name, ep.APIKey, want)
		}
	}
}

func TestIssue4633_ExistingTargetStillCarriesLegacy(t *testing.T) {
	cfg := &Config{
		Vendors: map[string]VendorConfig{
			"ai-gateway": {Endpoints: map[string]EndpointConfig{
				"openrouter": {APIKey: "GATEWAY-KEY"},
			}},
			"openrouter": {
				Endpoints: map[string]EndpointConfig{
					"custom-ep": {APIKey: "LEGACY-EP-KEY"},
				},
			},
		},
	}
	mergeDefaultEndpoints(cfg, DefaultConfig())

	ag := cfg.Vendors["ai-gateway"]
	ep, ok := ag.Endpoints["openrouter-custom-ep"]
	if !ok {
		t.Fatal("legacy custom endpoint dropped when target name existed (#4633 case B)")
	}
	if ep.APIKey != "LEGACY-EP-KEY" {
		t.Fatalf("legacy endpoint key = %q, want LEGACY-EP-KEY", ep.APIKey)
	}
	if got := ag.Endpoints["openrouter"].APIKey; got != "GATEWAY-KEY" {
		t.Fatalf("existing gateway key must never be overwritten, got %q", got)
	}
}
