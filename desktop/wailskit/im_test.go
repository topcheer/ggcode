package wailskit

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestSaveIMAdapterPreservesDisabledState(t *testing.T) {
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

type fakeUnbinder struct {
	failures int
	calls    int
}

func (f *fakeUnbinder) UnbindAdapter(name string) error {
	f.calls++
	if f.calls <= f.failures {
		return fmt.Errorf("binding store unavailable")
	}
	return nil
}

func seedIMTestConfig(t *testing.T) {
	t.Helper()
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
		"enabled":  "true",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveIMAdapterFailedUnbindRetryWorks(t *testing.T) {
	seedIMTestConfig(t)

	fake := &fakeUnbinder{failures: 1}

	if err := RemoveIMAdapter("tg", fake); err == nil {
		t.Fatal("expected first remove to fail while unbind fails")
	}
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg.IM.Adapters["tg"]; !exists {
		t.Fatal("#497 regression: failed unbind must not leave the config deleted — retry would die at 'not found'")
	}

	if err := RemoveIMAdapter("tg", fake); err != nil {
		t.Fatalf("retry should succeed once unbind recovers: %v", err)
	}
	cfg, err = config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg.IM.Adapters["tg"]; exists {
		t.Fatal("adapter should be fully removed after a successful retry")
	}
	if fake.calls != 2 {
		t.Fatalf("expected unbind to be attempted twice (fail then succeed), got %d calls", fake.calls)
	}
}

func TestRemoveIMAdapterNilManager(t *testing.T) {
	seedIMTestConfig(t)
	if err := RemoveIMAdapter("tg", nil); err != nil {
		t.Fatalf("nil imMgr should not block removal: %v", err)
	}
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg.IM.Adapters["tg"]; exists {
		t.Fatal("adapter should be removed with nil imMgr")
	}
}
