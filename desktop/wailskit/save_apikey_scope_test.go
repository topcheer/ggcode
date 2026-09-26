//go:build goolm

package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// r149 (cross-surface scope consistency): TUI provider panel, /config set
// apikey, the agent config tool and the webui all write the api_key for a
// vendor/endpoint pair at ENDPOINT scope. When such an endpoint-level
// binding already exists, the desktop SaveAPIKey must keep endpoint scope -
// a vendor-level write is silently shadowed at resolve time
// (resolveEffectiveAPIKeyRef prefers a resolvable endpoint ref), so the new
// key never takes effect.
func TestSaveAPIKey_ExistingEndpointKeyStaysEndpointScoped(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "")
	cfg := GetGlobalConfig()
	cfg.Vendor = "zai"
	cfg.Endpoint = "default"
	cfg.Vendors = map[string]config.VendorConfig{
		"zai": {
			Endpoints: map[string]config.EndpointConfig{
				"default": {BaseURL: "https://api.example.com", Protocol: "openai", APIKey: "${ZAI_DEFAULT_API_KEY}"},
			},
		},
	}

	if err := SaveAPIKey("zai", "default", "sk-new"); err != nil {
		t.Fatalf("SaveAPIKey: %v", err)
	}

	vc := cfg.Vendors["zai"]
	if vc.APIKey != "" {
		t.Errorf("vendor-level key written (%q); want endpoint-scoped update only", vc.APIKey)
	}
	ep := vc.Endpoints["default"]
	if ep.APIKey != "${ZAI_DEFAULT_API_KEY}" {
		t.Errorf("endpoint api_key = %q, want ${ZAI_DEFAULT_API_KEY}", ep.APIKey)
	}
}

// Fresh single-endpoint vendors keep the #115/#116/#117 vendor-level
// semantics: no endpoint binding exists yet, so the key lands at vendor
// scope and every endpoint falls back to it.
func TestSaveAPIKey_FreshSingleEndpointStaysVendorScoped(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "")
	cfg := GetGlobalConfig()
	cfg.Vendor = "zai"
	cfg.Endpoint = "default"
	cfg.Vendors = map[string]config.VendorConfig{
		"zai": {
			Endpoints: map[string]config.EndpointConfig{
				"default": {BaseURL: "https://api.example.com", Protocol: "openai"},
			},
		},
	}

	if err := SaveAPIKey("zai", "default", "sk-vendor"); err != nil {
		t.Fatalf("SaveAPIKey: %v", err)
	}

	vc := cfg.Vendors["zai"]
	if vc.APIKey != "${ZAI_API_KEY}" {
		t.Errorf("vendor api_key = %q, want ${ZAI_API_KEY}", vc.APIKey)
	}
	if ep := vc.Endpoints["default"]; ep.APIKey != "" {
		t.Errorf("endpoint api_key = %q, want empty", ep.APIKey)
	}
}

// Gateway vendors (multiple endpoints) already store per endpoint; the
// update must stay endpoint-scoped there too.
func TestSaveAPIKey_MultiEndpointStaysEndpointScoped(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "")
	cfg := GetGlobalConfig()
	cfg.Vendor = "gw"
	cfg.Endpoint = "prod"
	cfg.Vendors = map[string]config.VendorConfig{
		"gw": {
			Endpoints: map[string]config.EndpointConfig{
				"prod": {BaseURL: "https://prod.example.com", Protocol: "openai"},
				"dev":  {BaseURL: "https://dev.example.com", Protocol: "openai"},
			},
		},
	}

	if err := SaveAPIKey("gw", "prod", "sk-prod"); err != nil {
		t.Fatalf("SaveAPIKey: %v", err)
	}

	vc := cfg.Vendors["gw"]
	if vc.APIKey != "" {
		t.Errorf("vendor-level key written (%q); want endpoint-scoped update only", vc.APIKey)
	}
	if ep := vc.Endpoints["prod"]; ep.APIKey != "${GW_PROD_API_KEY}" {
		t.Errorf("prod endpoint api_key = %q, want ${GW_PROD_API_KEY}", ep.APIKey)
	}
}
