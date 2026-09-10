package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1706 case 1: a PARTIAL update (base_url only, protocol omitted) must
// keep the existing protocol - the pre-merge default silently flipped an
// anthropic endpoint to openai.
func TestAddEndpointPartialUpdateKeepsProtocol1706(t *testing.T) {
	c := testConfigWithVendor()
	if err := c.AddEndpoint("zai", "ep1", "anthropic", "https://a.example", "sk-x"); err != nil {
		t.Fatal(err)
	}
	// Update base_url only.
	if err := c.AddEndpoint("zai", "ep1", "", "https://b.example", ""); err != nil {
		t.Fatal(err)
	}
	got := c.Vendors["zai"].Endpoints["ep1"]
	if got.Protocol != "anthropic" {
		t.Fatalf("protocol silently flipped: got %q, want anthropic", got.Protocol)
	}
	if got.BaseURL != "https://b.example" {
		t.Fatalf("base_url not updated: %q", got.BaseURL)
	}
	// NEW endpoint without protocol still defaults to openai.
	if err := c.AddEndpoint("zai", "ep2", "", "https://c.example", ""); err != nil {
		t.Fatal(err)
	}
	if p := c.Vendors["zai"].Endpoints["ep2"].Protocol; p != "openai" {
		t.Fatalf("new endpoint default = %q, want openai", p)
	}
}

// #1706 case 3: clearing a vendor key removes the keys.env entry.
func TestSetVendorAPIKeyClearRemovesKeysEnv1706(t *testing.T) {
	keysPath := filepath.Join(t.TempDir(), "keys.env")
	keysEnvPathOverride = keysPath
	defer func() { keysEnvPathOverride = "" }()

	c := testConfigWithVendor()
	if err := c.SetVendorAPIKey("zai", "sk-secret"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(keysPath)
	if !strings.Contains(string(data), "sk-secret") {
		t.Fatal("key should be persisted for the set-up phase")
	}
	if err := c.SetVendorAPIKey("zai", ""); err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(keysPath)
	if strings.Contains(string(data2), "sk-secret") {
		t.Fatal("cleared key must not linger in keys.env")
	}
	if c.Vendors["zai"].APIKey != "" {
		t.Fatal("vendor key must be empty after clear")
	}
}
