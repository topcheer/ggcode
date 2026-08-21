package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// writeConfig writes a minimal ggcode.yaml with the given fallback vendor.
func writeConfig(t *testing.T, path, fbVendor string) {
	t.Helper()
	yaml := "language: en\n"
	if fbVendor != "" {
		yaml += "fallback:\n  enabled: true\n  vendor: " + fbVendor + "\n  endpoint: default\n  model: m1\n"
	}
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestAccess(t *testing.T, cfgPath string) *configAccess {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return NewConfigAccess(cfg, t.TempDir())
}

func TestConfigHotReload_FallbackRefresh(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")

	access := newTestAccess(t, cfgPath)
	if access.cfg.Fallback.IsConfigured() {
		t.Fatal("baseline should have no fallback")
	}

	w := NewConfigHotReload(cfgPath, access)
	w.interval = 10 * time.Millisecond

	// Edit: add a fallback.
	writeConfig(t, cfgPath, "anthropic")
	w.pollOnce()

	if !access.cfg.Fallback.IsConfigured() {
		t.Fatalf("fallback not refreshed: %+v", access.cfg.Fallback)
	}
	if access.cfg.Fallback.Vendor != "anthropic" {
		t.Fatalf("wrong vendor: %q", access.cfg.Fallback.Vendor)
	}
}

func TestConfigHotReload_VendorDefsRefresh(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")

	access := newTestAccess(t, cfgPath)
	if len(access.cfg.Vendors) == 0 {
		t.Skip("no vendor defs loadable in this environment")
	}
	before := access.cfg.Vendors

	w := NewConfigHotReload(cfgPath, access)

	// Simulate a vendors.yaml change (fresh config with different vendor map).
	fresh, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Vendors = map[string]config.VendorConfig{"custom-vendor": {DisplayName: "Custom"}}
	w.applyFreshConfig(fresh)

	if _, ok := access.cfg.Vendors["custom-vendor"]; !ok {
		t.Fatal("vendor definitions not refreshed in place")
	}
	_ = before
}

func TestConfigHotReload_InvalidYAMLKeepsLastGood(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "anthropic")

	access := newTestAccess(t, cfgPath)
	w := NewConfigHotReload(cfgPath, access)

	// Break the file mid-edit (editors do this transiently).
	if err := os.WriteFile(cfgPath, []byte("vendor: [broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.pollOnce()

	// Last good fallback must survive.
	if !access.cfg.Fallback.IsConfigured() {
		t.Fatal("broken yaml must not wipe the last good config")
	}
}

func TestConfigHotReload_NoChangeNoRefresh(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")

	access := newTestAccess(t, cfgPath)
	w := NewConfigHotReload(cfgPath, access)

	// Baseline is seeded at Start; without Start, seed manually via one
	// poll (which will treat the file as changed the first time). Simulate
	// steady state instead: poll twice with no edits and assert no panic
	// and config stays consistent.
	for i := 0; i < 2; i++ {
		w.pollOnce()
	}
	if access.cfg.Language != "en" {
		t.Fatalf("language unexpectedly changed: %q", access.cfg.Language)
	}
}

func TestConfigHotReload_SessionSelectionPreserved(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")

	access := newTestAccess(t, cfgPath)
	// Simulate a session-scoped model override (TUI /model, #541 semantics).
	access.cfg.Model = "glm-5.2-custom"

	w := NewConfigHotReload(cfgPath, access)
	fresh, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Model == "glm-5.2-custom" {
		t.Skip("test config already equals session override")
	}
	w.applyFreshConfig(fresh)

	// Vendor/Endpoint/Model selection is session-scoped: file edits must
	// not stomp a live session's model choice.
	if access.cfg.Model != "glm-5.2-custom" {
		t.Fatalf("session model selection stomped by reload: %q", access.cfg.Model)
	}
}

func TestConfigHotReload_ConcurrentAccessUnderRefresh(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")

	access := newTestAccess(t, cfgPath)
	w := NewConfigHotReload(cfgPath, access)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.interval = 5 * time.Millisecond
	w.Start(ctx)

	// Concurrent Get/Set while the watcher refreshes - race detector bait.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_, _ = access.Get("vendor")
			if i%20 == 0 {
				writeConfig(t, cfgPath, "anthropic")
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	<-done
	cancel()
}
