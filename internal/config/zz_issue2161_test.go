package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveVendorPurgesKey(t *testing.T) {
	dir := t.TempDir()
	keysEnvPathOverride = filepath.Join(dir, "keys.env")
	defer func() { keysEnvPathOverride = "" }()

	c := testConfig2105(t, "zai")
	if err := c.SetVendorAPIKey("zai", "sk-REMOVE-ME"); err != nil {
		t.Fatalf("SetVendorAPIKey: %v", err)
	}
	if err := c.RemoveVendor("zai"); err != nil {
		t.Fatalf("RemoveVendor: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "keys.env"))
	if err == nil && strings.Contains(string(raw), "sk-REMOVE-ME") {
		t.Fatalf("removed vendor's key still in keys.env:\n%s", raw)
	}
	for _, name := range []string{"ZAI_API_KEY", "ZAI_DEFAULT_API_KEY"} {
		if v := os.Getenv(name); v != "" {
			t.Fatalf("%s still set after RemoveVendor: %q", name, v)
		}
	}

	c2 := testConfig2105(t, "zai2")
	if err := c2.SetEndpointAPIKey("zai2", "default", "sk-EP", true); err != nil {
		t.Fatalf("SetEndpointAPIKey: %v", err)
	}
	if err := c2.RemoveVendor("zai2"); err != nil {
		t.Fatalf("RemoveVendor: %v", err)
	}
	raw2, _ := os.ReadFile(filepath.Join(dir, "keys.env"))
	if strings.Contains(string(raw2), "sk-EP") {
		t.Fatalf("vendor-scoped flavor key survived RemoveVendor:\n%s", raw2)
	}
}
