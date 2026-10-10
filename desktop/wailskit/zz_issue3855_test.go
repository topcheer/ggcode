//go:build goolm

package wailskit

// #3855 companions: UpdateConfig's vendor/endpoint existence check was
// gated on a non-empty baseURL in the SAME batch. The onboarding path
// (CompleteOnboard) commits {vendor,endpoint,model} WITHOUT baseURL, so a
// typo'd vendor skipped validation, was written into the resident cfg, and
// poisoned every in-session ResolveActiveEndpoint call until restart - even
// though the function contract says a failed update leaves the config
// exactly as it was (#740 family).

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func issue3855Env(t *testing.T) {
	t.Helper()
	_, _ = setupConfigTestEnv(t, "")
	cfg := GetGlobalConfig()
	cfg.Vendors = map[string]config.VendorConfig{
		"goodvendor": {
			Endpoints: map[string]config.EndpointConfig{
				"ep1": {Protocol: "openai"},
			},
		},
		"barevendor": {
			Endpoints: map[string]config.EndpointConfig{
				"noprotocol": {}, // exists but protocol-less: Validate would fail
			},
		},
	}
	cfg.Vendor = "goodvendor"
	cfg.Endpoint = "ep1"
	SetConfig(cfg)
}

// A typo'd vendor submitted WITHOUT baseURL (onboarding shape) must be
// rejected BEFORE mutation - previously the check was skipped and the
// resident cfg.Vendor was poisoned.
func TestIssue3855_TypoVendorNoBaseURLRejectedBeforeMutate(t *testing.T) {
	issue3855Env(t)
	cfg := GetGlobalConfig()

	err := UpdateConfig(map[string]interface{}{
		"vendor": "typo", "endpoint": "ep1", "model": "m",
	})
	if err == nil {
		t.Fatal("typo'd vendor must be rejected")
	}
	if cfg.Vendor != "goodvendor" {
		t.Fatalf("resident cfg.Vendor polluted = %q, want goodvendor (#3855: contract says failed update leaves config as it was)", cfg.Vendor)
	}
}

// The legit onboarding shape (valid vendor/endpoint, no baseURL) must still
// succeed - the unconditional check must not over-block the normal path.
func TestIssue3855_ValidPairNoBaseURLStillSucceeds(t *testing.T) {
	issue3855Env(t)
	cfg := GetGlobalConfig()
	cfg.Vendor, cfg.Endpoint = "", "" // simulate a mid-onboard resident state
	SetConfig(cfg)

	if err := UpdateConfig(map[string]interface{}{"vendor": "goodvendor", "endpoint": "ep1"}); err != nil {
		t.Fatalf("valid pair without baseURL must pass: %v", err)
	}
	if cfg.Vendor != "goodvendor" || cfg.Endpoint != "ep1" {
		t.Fatalf("valid pair not applied: vendor=%q endpoint=%q", cfg.Vendor, cfg.Endpoint)
	}
}

// An endpoint that exists but lacks a protocol passes the existence check
// yet fails Validate() inside Save() - now caught pre-mutation so the
// resident cfg is never left holding a Save-rejectable selection.
func TestIssue3855_ProtocollessEndpointRejectedBeforeMutate(t *testing.T) {
	issue3855Env(t)
	cfg := GetGlobalConfig()

	err := UpdateConfig(map[string]interface{}{"vendor": "barevendor", "endpoint": "noprotocol"})
	if err == nil {
		t.Fatal("protocol-less endpoint must be rejected pre-mutation")
	}
	if cfg.Vendor != "goodvendor" {
		t.Fatalf("resident cfg.Vendor polluted = %q, want goodvendor", cfg.Vendor)
	}
}
