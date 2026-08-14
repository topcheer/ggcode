package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSave_ExcludesInstanceOnlyVendorsFromGlobalFiles is the regression test
// for #293: after LoadWithInstance merges an instance-only vendor (with a
// plaintext API key) into the in-memory config, Save() must NOT write that
// vendor to the global vendors.yaml, and the plaintext key must not be
// migrated into the global keys.env (cross-workspace leak).
func TestSave_ExcludesInstanceOnlyVendorsFromGlobalFiles(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	globalYAML := `
vendor: testv
endpoint: main
model: m1
vendors:
  testv:
    api_key: globalkey123
    endpoints:
      main:
        protocol: openai
        base_url: https://global.example.com/v1
`
	if err := os.WriteFile(cfgPath, []byte(globalYAML), 0600); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	// Instance config lives at ~/.ggcode/instances/{hash}/ggcode.yaml, not in
	// the workspace itself.
	instDir := InstanceDir(ws)
	if instDir == "" {
		t.Fatal("InstanceDir returned empty")
	}
	if err := os.MkdirAll(instDir, 0700); err != nil {
		t.Fatal(err)
	}
	// Instance-only vendor with a plaintext key (the leak vector from #293).
	instYAML := `
vendors:
  privatews:
    api_key: sk-instance-secret-456
    endpoints:
      main:
        protocol: openai
        base_url: https://private.example.com/v1
`
	instPath := filepath.Join(instDir, "ggcode.yaml")
	if err := os.WriteFile(instPath, []byte(instYAML), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithInstance(cfgPath, ws)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	// Sanity: merge happened.
	if _, ok := cfg.Vendors["privatews"]; !ok {
		t.Fatal("instance vendor privatews should be merged into memory")
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The instance-only vendor must NOT appear in the global vendors.yaml.
	vendorsYAML, err := os.ReadFile(filepath.Join(cfgDir, "vendors.yaml"))
	if err != nil {
		t.Fatalf("reading global vendors.yaml: %v", err)
	}
	if strings.Contains(string(vendorsYAML), "privatews") {
		t.Error("instance-only vendor leaked into global vendors.yaml (#293)")
	}
	if strings.Contains(string(vendorsYAML), "sk-instance-secret-456") {
		t.Error("instance plaintext API key leaked into global vendors.yaml (#293)")
	}
	if !strings.Contains(string(vendorsYAML), "testv") {
		t.Error("global vendor should still be saved to global vendors.yaml")
	}

	// The instance plaintext key must NOT be migrated into the global keys.env.
	keysEnv, rerr := os.ReadFile(filepath.Join(cfgDir, "keys.env"))
	if rerr != nil && !os.IsNotExist(rerr) {
		t.Fatalf("reading global keys.env: %v", rerr)
	}
	if rerr == nil && strings.Contains(string(keysEnv), "sk-instance-secret-456") {
		t.Error("instance plaintext API key leaked into global keys.env (#293)")
	}
}

// TestSave_KeepsGlobalVendorPlaintextMigration ensures the fix did not break
// the #250 behavior: a plaintext key on a GLOBAL vendor in vendors.yaml is
// still migrated to the global keys.env with an unprefixed var name.
func TestSave_KeepsGlobalVendorPlaintextMigration(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	globalYAML := `
vendor: testv
endpoint: main
model: m1
vendors:
  testv:
    api_key: globalkey789
    endpoints:
      main:
        protocol: openai
        base_url: https://global.example.com/v1
`
	if err := os.WriteFile(cfgPath, []byte(globalYAML), 0600); err != nil {
		t.Fatal(err)
	}

	// No instance config — plain Load keeps globalSnap nil-equivalent path.
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	keysEnv, rerr := os.ReadFile(filepath.Join(cfgDir, "keys.env"))
	if rerr != nil {
		t.Fatalf("global keys.env should exist after saving a global plaintext key: %v", rerr)
	}
	if !strings.Contains(string(keysEnv), "globalkey789") {
		t.Error("global vendor plaintext key should be migrated into global keys.env")
	}
	if strings.Contains(string(keysEnv), "GGCODE_I_") {
		t.Error("global keys.env should not contain instance-prefixed var names")
	}
	// vendors.yaml should now reference the env var instead of the plaintext.
	vendorsYAML, err := os.ReadFile(filepath.Join(cfgDir, "vendors.yaml"))
	if err != nil {
		t.Fatalf("reading vendors.yaml: %v", err)
	}
	if strings.Contains(string(vendorsYAML), "globalkey789") {
		t.Error("plaintext key should be replaced with ${VAR} reference in vendors.yaml")
	}
}
