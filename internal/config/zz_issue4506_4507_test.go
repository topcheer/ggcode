//go:build goolm

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue4506_ZeroClearsPersistedLimit(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")

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
				"main": {Protocol: "openai", ContextWindow: 0},
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

	if !strings.Contains(string(out), "protocol: openai") {
		t.Fatalf("protocol was collateral damage:\n%s", out)
	}
}

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
