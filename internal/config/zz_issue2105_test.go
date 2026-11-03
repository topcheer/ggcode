package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVendorKeyClearPurgesBothEnvNames(t *testing.T) {
	dir := t.TempDir()
	keysEnvPathOverride = filepath.Join(dir, "keys.env")
	defer func() { keysEnvPathOverride = "" }()

	c := testConfig2105(t, "zai")

	if err := c.SetEndpointAPIKey("zai", "default", "sk-OLD", true); err != nil {
		t.Fatalf("SetEndpointAPIKey: %v", err)
	}

	if err := c.SetVendorAPIKey("zai", "sk-NEW"); err != nil {
		t.Fatalf("SetVendorAPIKey: %v", err)
	}

	if err := c.SetVendorAPIKey("zai", ""); err != nil {
		t.Fatalf("SetVendorAPIKey clear: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "keys.env"))
	if err == nil {
		body := string(raw)
		if strings.Contains(body, "sk-OLD") {
			t.Fatalf("cleared vendor key still present in keys.env:\n%s", body)
		}
		if strings.Contains(body, "sk-NEW") {
			t.Fatalf("cleared vendor key still present in keys.env:\n%s", body)
		}
	}
	for _, name := range []string{"ZAI_API_KEY", "ZAI_DEFAULT_API_KEY"} {
		if v, ok := os.LookupEnv(name); ok && v != "" {
			t.Fatalf("%s still set after clear: %q", name, v)
		}
	}
}

func TestVendorKeySetKeepsSiblingInLockstep(t *testing.T) {
	dir := t.TempDir()
	keysEnvPathOverride = filepath.Join(dir, "keys.env")
	defer func() { keysEnvPathOverride = "" }()

	c := testConfig2105(t, "zai")
	if err := c.SetVendorAPIKey("zai", "sk-VIA-VENDOR"); err != nil {
		t.Fatalf("SetVendorAPIKey: %v", err)
	}

	if v := os.Getenv("ZAI_API_KEY"); v != "sk-VIA-VENDOR" {
		t.Fatalf("sibling ZAI_API_KEY not in lockstep: %q", v)
	}
}

func testConfig2105(t *testing.T, vendor string) *Config {
	t.Helper()
	return &Config{Vendors: map[string]VendorConfig{
		vendor: {Endpoints: map[string]EndpointConfig{}},
	}}
}
