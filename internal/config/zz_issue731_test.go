package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue731PartialOverlapVendorWriteback is the regression test for #731:
// MergeInstance set instanceFields["vendors"] only when global.Vendors was
// nil. With a partial overlap (global vendors.yaml has A, instance config has
// B), B was merged into memory but the "vendors" key was never registered in
// instanceFields, so the desktop write-back gate (InstanceFields() contains
// "vendors" - saveWithInstanceWriteback in desktop/wailskit/config.go) never
// opened: SaveInstanceScoped was not called and AddCustomEndpoint/SaveAPIKey
// changes silently vanished on restart. The fix registers the flag per adopted
// vendor KEY, matching the key-level semantics of ToolPerms.
func TestIssue731PartialOverlapVendorWriteback(t *testing.T) {
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
    api_key: globalkey731
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
	instYAML := `
vendors:
  b:
    endpoints:
      main:
        protocol: openai
        base_url: https://b.example.com/v1
`
	instPath := filepath.Join(instDir, "ggcode.yaml")
	if err := os.WriteFile(instPath, []byte(instYAML), 0600); err != nil {
		t.Fatal(err)
	}

	// The issue trigger sequence: partial overlap loaded, then a NEW vendor C
	// is added at runtime (what AddCustomEndpoint does) and Save() runs while
	// the instance write-back path (gated on InstanceFields) executes.
	cfg, err := LoadWithInstance(cfgPath, ws)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}

	// The gate condition itself: partial overlap must register "vendors".
	found := false
	for _, k := range cfg.InstanceFields() {
		if k == "vendors" {
			found = true
		}
	}
	if !found {
		t.Fatalf("partial vendor overlap must register 'vendors' in InstanceFields() (#731); got %v", cfg.InstanceFields())
	}

	// Runtime addition of vendor C (AddCustomEndpoint effect).
	if cfg.Vendors == nil {
		cfg.Vendors = map[string]VendorConfig{}
	}
	cfg.Vendors["c"] = VendorConfig{Endpoints: map[string]EndpointConfig{
		"main": {Protocol: "openai", BaseURL: "https://c.example.com/v1"},
	}}

	// saveWithInstanceWriteback equivalent: Save() then instance write-back.
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := cfg.SaveInstanceScoped(cfg.InstanceWorkspace()); err != nil {
		t.Fatalf("SaveInstanceScoped: %v", err)
	}
	cfg.SyncVendorToGlobalSnapshot("c")

	// instance.yaml must contain both B (pre-existing) and C (just added).
	inst, err := os.ReadFile(instPath)
	if err != nil {
		t.Fatalf("reading instance config: %v", err)
	}
	for _, want := range []string{"b:", "c:", "https://c.example.com/v1"} {
		if !strings.Contains(string(inst), want) {
			t.Errorf("instance key %q should be persisted to instance.yaml (#731). file:\n%s", want, inst)
		}
	}

	// Global vendors.yaml must NOT gain B or C on this Save (they are
	// instance-sourced; C is synced into the snapshot only for future saves).
	vp := VendorsPath(cfgDir)
	if data, err := os.ReadFile(vp); err == nil {
		if strings.Contains(string(data), "https://b.example.com") {
			t.Errorf("instance vendor B leaked into global vendors.yaml (#731). file:\n%s", data)
		}
	}

	// Restart semantics: a fresh load of the same global+instance pair must
	// still see C (the data-loss symptom is C disappearing after restart).
	cfg2, err := LoadWithInstance(cfgPath, ws)
	if err != nil {
		t.Fatalf("reload LoadWithInstance: %v", err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, ok := cfg2.Vendors[name]; !ok {
			t.Errorf("after reload, vendor %q must still be visible (#731 data loss); have %v", name, cfg2.Vendors)
		}
	}
}

// TestIssue731MergeInstanceVendorsFlagMatrix covers the instanceFields
// registration for all four vendor overlap shapes:
//   - partial overlap (global has A, instance has B) -> flag set (#731 fix)
//   - fully global (identical keys)                  -> flag NOT set
//   - fully instance (global nil)                    -> flag set (existing branch)
//   - global nil + instance nil                      -> flag NOT set
func TestIssue731MergeInstanceVendorsFlagMatrix(t *testing.T) {
	vend := func(url string) VendorConfig {
		return VendorConfig{Endpoints: map[string]EndpointConfig{
			"main": {Protocol: "openai", BaseURL: url},
		}}
	}
	cases := []struct {
		name       string
		global     map[string]VendorConfig
		instance   map[string]VendorConfig
		wantFlag   bool
		wantMerged []string
	}{
		{
			name:       "partial overlap",
			global:     map[string]VendorConfig{"a": vend("https://a.example.com")},
			instance:   map[string]VendorConfig{"b": vend("https://b.example.com")},
			wantFlag:   true,
			wantMerged: []string{"a", "b"},
		},
		{
			name:       "fully global no new keys",
			global:     map[string]VendorConfig{"a": vend("https://a.example.com")},
			instance:   map[string]VendorConfig{"a": vend("https://other.example.com")},
			wantFlag:   false,
			wantMerged: []string{"a"},
		},
		{
			name:       "fully instance global nil",
			global:     nil,
			instance:   map[string]VendorConfig{"b": vend("https://b.example.com")},
			wantFlag:   true,
			wantMerged: []string{"b"},
		},
		{
			name:       "both nil",
			global:     nil,
			instance:   nil,
			wantFlag:   false,
			wantMerged: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			global := &Config{Vendors: tc.global}
			MergeInstance(global, &Config{Vendors: tc.instance})
			if got := global.instanceFields["vendors"]; got != tc.wantFlag {
				t.Errorf("instanceFields['vendors'] = %v, want %v", got, tc.wantFlag)
			}
			if len(global.Vendors) != len(tc.wantMerged) {
				t.Fatalf("merged vendor count = %d (%v), want %d", len(global.Vendors), global.Vendors, len(tc.wantMerged))
			}
			for _, name := range tc.wantMerged {
				if _, ok := global.Vendors[name]; !ok {
					t.Errorf("merged vendors missing %q; have %v", name, global.Vendors)
				}
			}
		})
	}
}
