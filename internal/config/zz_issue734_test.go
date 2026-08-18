package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue734VendorNotInGlobalSnapshotTable is the table-driven test for the
// runtime provenance helper behind the desktop write-back gate (#734).
func TestIssue734VendorNotInGlobalSnapshotTable(t *testing.T) {
	vend := func(url string) VendorConfig {
		return VendorConfig{Endpoints: map[string]EndpointConfig{
			"main": {Protocol: "openai", BaseURL: url},
		}}
	}
	snap := &Config{Vendors: map[string]VendorConfig{"a": vend("https://a.example.com")}}
	cur := &Config{Vendors: map[string]VendorConfig{
		"a": vend("https://a.example.com"),
		"c": vend("https://c.example.com"),
	}, globalSnap: snap}

	cases := []struct {
		name string
		cfg  *Config
		vend string
		want bool
	}{
		{"in snapshot -> false", cur, "a", false},
		{"runtime-added -> true", cur, "c", true},
		{"absent everywhere -> false", cur, "zz", false},
		{"empty vendor -> false", cur, "", false},
		{"no snapshot -> false", &Config{Vendors: cur.Vendors}, "c", false},
		{"nil config -> false", nil, "a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.VendorNotInGlobalSnapshot(tc.vend); got != tc.want {
				t.Errorf("VendorNotInGlobalSnapshot(%q) = %v, want %v", tc.vend, got, tc.want)
			}
		})
	}
}

// TestIssue734InstanceWithoutVendorsWriteback is the regression test for #734:
// the #731 per-key registration only covered "instance adopted >= 1 vendor
// key". The common instance.yaml shape carries just language/mode and NO
// vendors section, so instanceFields["vendors"] stayed unset, the desktop
// write-back gate (formerly keyed on InstanceFields() containing "vendors")
// never opened, and a runtime-added vendor was dropped by Save()'s
// globalOnlyVendors filter and lost on restart.
//
// The fix gates on runtime provenance (VendorNotInGlobalSnapshot) instead of
// the serialization flag. This test reproduces the full trigger sequence at
// the config layer: global has vendor a, instance has no vendors, vendor C
// added at runtime, write-back runs when the gate opens.
func TestIssue734InstanceWithoutVendorsWriteback(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	globalYAML := `
vendor: a
endpoint: main
model: m1
vendors:
  a:
    api_key: globalkey734
    endpoints:
      main:
        protocol: openai
        base_url: https://a.example.com/v1
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
	// The #734 trigger shape: instance config WITHOUT a vendors section.
	instPath := filepath.Join(instDir, "ggcode.yaml")
	if err := os.WriteFile(instPath, []byte("language: zh\ndefault_mode: auto\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithInstance(cfgPath, ws)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}

	// Confirm the trigger condition: attached instance, but the vendors flag
	// is NOT set (nothing was adopted from a vendors section that doesn't
	// exist). This documents why the old flag-based gate missed this shape.
	if !cfg.HasInstanceConfigAttached() {
		t.Fatal("instance config must be attached")
	}
	for _, k := range cfg.InstanceFields() {
		if k == "vendors" {
			t.Fatalf("scenario requires vendors flag unset; got %v", cfg.InstanceFields())
		}
	}

	// Runtime addition of vendor C (AddCustomEndpoint effect) and the #734
	// gate: provenance must be detected regardless of the flag.
	cfg.Vendors["c"] = VendorConfig{Endpoints: map[string]EndpointConfig{
		"main": {Protocol: "openai", BaseURL: "https://c734.example.com/v1"},
	}}
	if !cfg.VendorNotInGlobalSnapshot("c") {
		t.Fatal("runtime-added vendor C must be flagged as not-in-snapshot (the #734 gate); write-back would be skipped and C lost")
	}
	if cfg.VendorNotInGlobalSnapshot("a") {
		t.Fatal("global vendor A must not be flagged (write-back not needed)")
	}

	// saveWithInstanceWriteback equivalent (desktop/wailskit/config.go):
	// Save(), then instance write-back + snapshot sync when the gate opens.
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if cfg.VendorNotInGlobalSnapshot("c") {
		if err := cfg.SaveInstanceScoped(cfg.InstanceWorkspace()); err != nil {
			t.Fatalf("SaveInstanceScoped: %v", err)
		}
		cfg.SyncVendorToGlobalSnapshot("c")
	}

	// instance.yaml must now carry C even though it originally had no vendors
	// section (SaveInstanceScoped merges the vendors delta onto the file).
	inst, err := os.ReadFile(instPath)
	if err != nil {
		t.Fatalf("reading instance config: %v", err)
	}
	for _, want := range []string{"c:", "https://c734.example.com/v1", "language: zh"} {
		if !strings.Contains(string(inst), want) {
			t.Errorf("instance.yaml should contain %q (#734 data loss). file:\n%s", want, inst)
		}
	}

	// Global vendors.yaml must NOT gain C on this Save (instance-sourced; the
	// snapshot sync only affects future saves, matching #293 leak semantics).
	if data, err := os.ReadFile(VendorsPath(cfgDir)); err == nil {
		if strings.Contains(string(data), "https://c734.example.com") {
			t.Errorf("runtime vendor C leaked into global vendors.yaml on first save (#293). file:\n%s", data)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("reading global vendors.yaml: %v", err)
	}

	// Restart semantics: a fresh load of the same global+instance pair must
	// still see C - the data-loss symptom is C disappearing after restart.
	cfg2, err := LoadWithInstance(cfgPath, ws)
	if err != nil {
		t.Fatalf("reload LoadWithInstance: %v", err)
	}
	for _, name := range []string{"a", "c"} {
		if _, ok := cfg2.Vendors[name]; !ok {
			t.Errorf("after reload, vendor %q must still be visible (#734 data loss); have %v", name, cfg2.Vendors)
		}
	}
}
