package wailskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestIssue734AddCustomEndpointNoInstanceVendorsSection is the write-back
// layer regression test for #734: an instance config WITHOUT a vendors
// section (the common instance.yaml carrying just language/mode) left
// instanceFields["vendors"] unset, so saveWithInstanceWriteback's old gate
// (InstanceFields() contains "vendors") never opened. AddCustomEndpoint's
// new vendor was then dropped by Save()'s globalOnlyVendors filter and
// silently lost on restart. The gate now checks runtime provenance
// (Config.VendorNotInGlobalSnapshot) and covers every #368-family shape.
func TestIssue734AddCustomEndpointNoInstanceVendorsSection(t *testing.T) {
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
    api_key: globalkey734w
    endpoints:
      main:
        protocol: openai
        base_url: https://a.example.com/v1
`
	if err := os.WriteFile(cfgPath, []byte(globalYAML), 0600); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	instDir := config.InstanceDir(ws)
	if instDir == "" {
		t.Fatal("InstanceDir returned empty")
	}
	if err := os.MkdirAll(instDir, 0700); err != nil {
		t.Fatal(err)
	}
	// #734 trigger shape: instance attached, NO vendors section.
	instPath := filepath.Join(instDir, "ggcode.yaml")
	if err := os.WriteFile(instPath, []byte("language: zh\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadWithInstance(cfgPath, ws)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	SetConfig(cfg)

	// The user-visible action: add a brand-new vendor endpoint at runtime.
	if err := AddCustomEndpoint("c", "main", "openai", "https://c734.example.com/v1", ""); err != nil {
		t.Fatalf("AddCustomEndpoint: %v", err)
	}

	// C must be persisted to the instance file (the only durable layer that
	// survives restart for an instance-bound workspace with this shape).
	inst, err := os.ReadFile(instPath)
	if err != nil {
		t.Fatalf("reading instance config: %v", err)
	}
	for _, want := range []string{"c:", "https://c734.example.com/v1"} {
		if !strings.Contains(string(inst), want) {
			t.Errorf("instance.yaml should contain %q (#734 data loss: vendor lost on restart). file:\n%s", want, inst)
		}
	}

	// Restart semantics: fresh load must still see C.
	cfg2, err := config.LoadWithInstance(cfgPath, ws)
	if err != nil {
		t.Fatalf("reload LoadWithInstance: %v", err)
	}
	for _, name := range []string{"a", "c"} {
		if _, ok := cfg2.Vendors[name]; !ok {
			t.Errorf("after reload, vendor %q must still be visible (#734 data loss); have %v", name, cfg2.Vendors)
		}
	}
}
