package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zz_issue4262_test.go - regression tests for #4262: globalOnlyVendors filtered
// by NAME only, so an instance OVERRIDE of a same-named global vendor (e.g.
// vendors.testv.api_key) was written to the global vendors.yaml / keys.env and
// leaked the workspace-private key to every other workspace. The fix writes the
// snapshot's original struct back for same-named vendors.

func setup4262(t *testing.T, instanceYAML string) (cfgDir string, cfg *Config) {
	t.Helper()
	home := t.TempDir()
	cfgDir = filepath.Join(home, ".ggcode")
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
    api_key: globalkey4262
    endpoints:
      main:
        protocol: openai
        base_url: https://global.example.com/v1
`
	if err := os.WriteFile(cfgPath, []byte(globalYAML), 0600); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	instDir := InstanceDir(ws)
	if instDir == "" {
		t.Fatal("InstanceDir returned empty")
	}
	if err := os.MkdirAll(instDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "ggcode.yaml"), []byte(instanceYAML), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithInstance(cfgPath, ws)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	return cfgDir, cfg
}

// TestSave_InstanceOverrideOfGlobalVendorNotLeakedToGlobalFiles: the instance
// file overrides the SAME-NAMED global vendor testv with a private api_key and
// base_url (#2289 instance-wins merge). Save() must keep the GLOBAL values in
// the global vendors.yaml/keys.env - not the merged instance override.
func TestSave_InstanceOverrideOfGlobalVendorNotLeakedToGlobalFiles(t *testing.T) {
	instYAML := `
vendors:
  testv:
    api_key: sk-wsA-4262-secret
    endpoints:
      main:
        protocol: openai
        base_url: https://private-wsA.example.com/v1
`
	cfgDir, cfg := setup4262(t, instYAML)

	// Sanity: the instance override won the in-memory merge (#2289).
	if got := cfg.Vendors["testv"].APIKey; got != "sk-wsA-4262-secret" {
		t.Fatalf("instance-wins merge should put the instance key in memory, got %q", got)
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	vendorsYAML, err := os.ReadFile(filepath.Join(cfgDir, "vendors.yaml"))
	if err != nil {
		t.Fatalf("reading global vendors.yaml: %v", err)
	}
	// Leak vectors (the #4262 bug): the instance key and base_url must NOT be
	// in the global file.
	if strings.Contains(string(vendorsYAML), "sk-wsA-4262-secret") {
		t.Error("instance override api_key leaked into global vendors.yaml (#4262)")
	}
	if strings.Contains(string(vendorsYAML), "private-wsA.example.com") {
		t.Error("instance override base_url leaked into global vendors.yaml (#4262)")
	}
	// Reverse direction (the "double destruction" half of #4262): the global
	// vendor's own fields must survive - the instance override (whose struct
	// lacked them) must not have clobbered them in the global file.
	if !strings.Contains(string(vendorsYAML), "https://global.example.com/v1") {
		t.Error("global vendor base_url was clobbered by the instance override (#4262)")
	}

	keysEnv, rerr := os.ReadFile(filepath.Join(cfgDir, "keys.env"))
	if rerr != nil && !os.IsNotExist(rerr) {
		t.Fatalf("reading global keys.env: %v", rerr)
	}
	if rerr == nil && strings.Contains(string(keysEnv), "sk-wsA-4262-secret") {
		t.Error("instance override api_key leaked into global keys.env (#4262)")
	}
}

// TestSave_SyncedRuntimeEditOfGlobalVendorIsKept: runtime edits to a global
// vendor flow through SyncVendorToGlobalSnapshot (#368) - the snapshot is
// updated to the edited value, so the snapshot write-back must PRESERVE the
// edit rather than revert it to the pre-edit value.
func TestSave_SyncedRuntimeEditOfGlobalVendorIsKept(t *testing.T) {
	// No vendor override in the instance file (common instance.yaml shape).
	cfgDir, cfg := setup4262(t, "language: en\n")

	// Simulate a runtime edit to the global vendor (AddCustomEndpoint-style).
	vc := cfg.Vendors["testv"]
	vc.APIKey = "rotated-global-key-4262"
	cfg.Vendors["testv"] = vc
	cfg.SyncVendorToGlobalSnapshot("testv")

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	keysEnv, rerr := os.ReadFile(filepath.Join(cfgDir, "keys.env"))
	if rerr != nil {
		t.Fatalf("reading global keys.env: %v", rerr)
	}
	if strings.Contains(string(keysEnv), "globalkey4262") {
		t.Error("stale pre-edit key persisted: synced runtime edit was reverted (#4262 fix must not regress #368)")
	}
	if !strings.Contains(string(keysEnv), "rotated-global-key-4262") {
		t.Error("synced runtime edit of a global vendor was not kept in global keys.env (#368 regression)")
	}
}
