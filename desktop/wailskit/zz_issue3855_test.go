//go:build goolm

package wailskit

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
				"noprotocol": {},
			},
		},
	}
	cfg.Vendor = "goodvendor"
	cfg.Endpoint = "ep1"
	SetConfig(cfg)
}

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

func TestIssue3855_ValidPairNoBaseURLStillSucceeds(t *testing.T) {
	issue3855Env(t)
	cfg := GetGlobalConfig()
	cfg.Vendor, cfg.Endpoint = "", ""
	SetConfig(cfg)

	if err := UpdateConfig(map[string]interface{}{"vendor": "goodvendor", "endpoint": "ep1"}); err != nil {
		t.Fatalf("valid pair without baseURL must pass: %v", err)
	}
	if cfg.Vendor != "goodvendor" || cfg.Endpoint != "ep1" {
		t.Fatalf("valid pair not applied: vendor=%q endpoint=%q", cfg.Vendor, cfg.Endpoint)
	}
}

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
