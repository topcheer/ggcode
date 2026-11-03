//go:build goolm

package config

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4504_ValidateWritesAuthTypeBackToEndpointMap(t *testing.T) {
	cfg := &Config{
		Vendor:   "testvendor",
		Endpoint: "main",
		Model:    "m1",
		Vendors: map[string]VendorConfig{
			"testvendor": {
				Endpoints: map[string]EndpointConfig{
					"main": {Protocol: "openai"},
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	ep, ok := cfg.Vendors["testvendor"].Endpoints["main"]
	if !ok {
		t.Fatal("endpoint vanished")
	}
	if ep.AuthType != "api_key" {
		t.Fatalf("#4504 regression: map endpoint AuthType still %q after Validate", ep.AuthType)
	}
}

func TestIssue4505_SaveSurfacesMigrationFailure(t *testing.T) {
	b, err := os.ReadFile("config_save.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	if !strings.Contains(s, "migrating plaintext API keys out of") {
		t.Fatal("#4505 regression: migration failures no longer surface in Save's error")
	}

	if strings.Contains(s, `debug.Log("config", "Save: post-save migration error`) ||
		strings.Contains(s, `debug.Log("config", "Save: vendors.yaml migration error`) {
		t.Fatal("#4505 regression: migration errors are back to debug.Log-only")
	}
}
