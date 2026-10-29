//go:build goolm

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4506: the "A value of 0 means 'unset' and will clear the field" contract
// (SetEndpointModelLimits) never reached disk - the merge overlay strips
// zeroes and deep-merge preserves absent keys, so a cleared limit resurrected
// from the file after every save. End-to-end: disk carries 8192, memory
// carries 0, Save must remove the key.
func TestIssue4506_ZeroClearsPersistedLimit(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")

	// Disk state: an endpoint with a persisted context_window.
	seed := "vendor: testv\nendpoint: main\nmodel: m1\n" +
		"vendors:\n  testv:\n    endpoints:\n      main:\n        protocol: openai\n        context_window: 8192\n"
	if err := os.WriteFile(cfgPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := &Config{
		FilePath: cfgPath,
		Vendor:   "testv",
		Endpoint: "main",
		Model:    "m1",
		Vendors: map[string]VendorConfig{
			"testv": {Endpoints: map[string]EndpointConfig{
				"main": {Protocol: "openai", ContextWindow: 0}, // cleared to 0
			}},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if strings.Contains(string(out), "context_window") {
		t.Fatalf("#4506 regression: context_window survived a 0 clear:\n%s", out)
	}
	// The untouched sibling field must survive the zero-prune.
	if !strings.Contains(string(out), "protocol: openai") {
		t.Fatalf("protocol was collateral damage:\n%s", out)
	}
}

// #4507 pin: both Add-paths must roll back the process env var when the
// keys.env persistence fails - a read-only dir used to leave a plaintext
// ghost key while the vendor never entered c.Vendors.
func TestIssue4507_SetenvRolledBackOnWriteFailure(t *testing.T) {
	b, err := os.ReadFile("config_vendor.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	n := strings.Count(s, "os.Unsetenv(envVarName)")
	if n < 2 {
		t.Fatalf("#4507 regression: expected os.Unsetenv rollback on both Add paths, found %d", n)
	}
}
