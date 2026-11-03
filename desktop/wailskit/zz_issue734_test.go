package wailskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

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

	instPath := filepath.Join(instDir, "ggcode.yaml")
	if err := os.WriteFile(instPath, []byte("language: zh\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadWithInstance(cfgPath, ws)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	SetConfig(cfg)

	if err := AddCustomEndpoint("c", "main", "openai", "https://c734.example.com/v1", ""); err != nil {
		t.Fatalf("AddCustomEndpoint: %v", err)
	}

	inst, err := os.ReadFile(instPath)
	if err != nil {
		t.Fatalf("reading instance config: %v", err)
	}
	for _, want := range []string{"c:", "https://c734.example.com/v1"} {
		if !strings.Contains(string(inst), want) {
			t.Errorf("instance.yaml should contain %q (#734 data loss: vendor lost on restart). file:\n%s", want, inst)
		}
	}

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
