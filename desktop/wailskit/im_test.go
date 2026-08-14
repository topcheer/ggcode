package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// Test #155: editing a disabled IM adapter via SaveIMAdapter must not
// silently re-enable it when the "enabled" key is absent from values.
func TestSaveIMAdapterPreservesDisabledState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Seed a minimal config so Load treats it as an existing (non-first-run)
	// config and merges the external im.yaml on reload.
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ggcode", "ggcode.yaml"), []byte("im:\n  enabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create an enabled adapter, then disable it.
	if err := SaveIMAdapter("tg", map[string]string{
		"platform": "telegram",
		"command":  "echo",
		"enabled":  "true",
	}); err != nil {
		t.Fatal(err)
	}
	if err := SetIMAdapterEnabled("tg", false); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IM.Adapters["tg"].Enabled {
		t.Fatal("precondition: adapter should be disabled before edit")
	}

	// Edit only the token field — the edit dialog never sends "enabled".
	if err := SaveIMAdapter("tg", map[string]string{
		"platform": "telegram",
		"command":  "echo",
		"token":    "new-token",
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err = config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.IM.Adapters["tg"]
	if got.Enabled {
		t.Fatal("#155 regression: editing a disabled adapter re-enabled it")
	}
	if got.Extra["token"] != "new-token" {
		t.Fatalf("token update lost: %+v", got.Extra)
	}
}

// Editing with an explicit enabled=true must still enable the adapter.
func TestSaveIMAdapterExplicitEnable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ggcode", "ggcode.yaml"), []byte("im:\n  enabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SaveIMAdapter("tg", map[string]string{
		"platform": "telegram",
		"command":  "echo",
		"enabled":  "false",
	}); err != nil {
		t.Fatal(err)
	}

	if err := SaveIMAdapter("tg", map[string]string{
		"platform": "telegram",
		"command":  "echo",
		"enabled":  "true",
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IM.Adapters["tg"].Enabled {
		t.Fatal("explicit enabled=true should re-enable the adapter")
	}
}
