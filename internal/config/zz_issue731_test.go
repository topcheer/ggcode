package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	cfg, err := LoadWithInstance(cfgPath, ws)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}

	found := false
	for _, k := range cfg.InstanceFields() {
		if k == "vendors" {
			found = true
		}
	}
	if !found {
		t.Fatalf("partial vendor overlap must register 'vendors' in InstanceFields() (#731); got %v", cfg.InstanceFields())
	}

	if cfg.Vendors == nil {
		cfg.Vendors = map[string]VendorConfig{}
	}
	cfg.Vendors["c"] = VendorConfig{Endpoints: map[string]EndpointConfig{
		"main": {Protocol: "openai", BaseURL: "https://c.example.com/v1"},
	}}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := cfg.SaveInstanceScoped(cfg.InstanceWorkspace()); err != nil {
		t.Fatalf("SaveInstanceScoped: %v", err)
	}
	cfg.SyncVendorToGlobalSnapshot("c")

	inst, err := os.ReadFile(instPath)
	if err != nil {
		t.Fatalf("reading instance config: %v", err)
	}
	for _, want := range []string{"b:", "c:", "https://c.example.com/v1"} {
		if !strings.Contains(string(inst), want) {
			t.Errorf("instance key %q should be persisted to instance.yaml (#731). file:\n%s", want, inst)
		}
	}

	vp := VendorsPath(cfgDir)
	if data, err := os.ReadFile(vp); err == nil {
		if strings.Contains(string(data), "https://b.example.com") {
			t.Errorf("instance vendor B leaked into global vendors.yaml (#731). file:\n%s", data)
		}
	}

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

			name:       "same key different value now overrides",
			global:     map[string]VendorConfig{"a": vend("https://a.example.com")},
			instance:   map[string]VendorConfig{"a": vend("https://other.example.com")},
			wantFlag:   true,
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
