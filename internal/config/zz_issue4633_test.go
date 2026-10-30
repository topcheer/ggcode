package config

import "testing"

// #4633: the legacy gateway-vendor migration must carry EVERY endpoint
// (endpoint-level API keys included) into ai-gateway - previously only the
// sort-lowest endpoint survived and the rest were silently deleted,
// violating the "cannot irreversibly drop credentials" contract.
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
	// Primary endpoint carries the vendor-level key per #959 placement;
	// endpoint-level keys survive via the namespaced copies below.
	if got := ag.Endpoints["openrouter"].APIKey; got != "VENDOR-LEVEL-KEY" {
		t.Fatalf("primary endpoint key = %q, want VENDOR-LEVEL-KEY (#959 placement)", got)
	}
	// Every endpoint-level credential must survive, namespaced.
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

// #4633 case B: target endpoint name already exists - the legacy endpoint
// must still be carried (namespaced), not dropped wholesale.
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
