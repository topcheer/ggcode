package agentruntime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// TestConfigHotReload_SelfAuthoredSaveSkipsReload verifies the watcher's
// no-op judgment: a save the session itself made (TUI panel, /config
// command, WebUI, agent loop) must not run the reload cycle, because the
// writer already mutated the shared in-memory config before persisting.
// A subsequent genuine external edit must still reload.
func TestConfigHotReload_SelfAuthoredSaveSkipsReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")

	access := newTestAccess(t, cfgPath)
	w := NewConfigHotReload(cfgPath, access)
	w.interval = 10 * time.Millisecond

	// Seed the baseline exactly like Start does.
	for _, p := range w.watchedFiles() {
		w.baselines[p] = snapshotFile(p)
	}
	if access.cfg.Fallback.IsConfigured() {
		t.Fatal("baseline should have no fallback")
	}

	// Session-style save: write new bytes AND record the self-write mark,
	// exactly what writeSecureConfigFile does for every in-process save.
	selfSaved := []byte("language: en\nfallback:\n  enabled: true\n  vendor: anthropic\n  endpoint: default\n  model: m1\n")
	if err := os.WriteFile(cfgPath, selfSaved, 0o644); err != nil {
		t.Fatal(err)
	}
	config.NoteConfigSelfWrite(cfgPath, selfSaved)

	w.pollOnce()

	// The reload cycle must have been skipped: the in-memory fallback stays
	// untouched (the writer owns that state; re-merging is a no-op at best).
	if access.cfg.Fallback.IsConfigured() {
		t.Fatalf("self-authored save must not trigger a reload: %+v", access.cfg.Fallback)
	}

	// External edit (another instance / manual editor): different bytes,
	// no mark in this process. The watcher must reload normally.
	writeConfig(t, cfgPath, "kimi")
	w.pollOnce()
	if !access.cfg.Fallback.IsConfigured() {
		t.Fatal("external edit must still trigger a reload")
	}
	if access.cfg.Fallback.Vendor != "kimi" {
		t.Fatalf("external edit reloaded wrong vendor: %q", access.cfg.Fallback.Vendor)
	}
}

// TestConfigHotReload_StaleSelfWriteMarkStillReloads guards the exact-hash
// match: a mark left by an older save must not mask a later external edit,
// even when the session saved the same file moments before.
func TestConfigHotReload_StaleSelfWriteMarkStillReloads(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")

	access := newTestAccess(t, cfgPath)
	w := NewConfigHotReload(cfgPath, access)
	for _, p := range w.watchedFiles() {
		w.baselines[p] = snapshotFile(p)
	}

	// Session saves version A (marked), then an external editor replaces the
	// file with version B before the next poll. Only A is marked; B must
	// reload.
	versionA := []byte("language: en\n")
	if err := os.WriteFile(cfgPath, versionA, 0o644); err != nil {
		t.Fatal(err)
	}
	config.NoteConfigSelfWrite(cfgPath, versionA)

	versionB := []byte("language: en\nfallback:\n  enabled: true\n  vendor: ark\n  endpoint: default\n  model: m1\n")
	if err := os.WriteFile(cfgPath, versionB, 0o644); err != nil {
		t.Fatal(err)
	}
	w.pollOnce()

	if !access.cfg.Fallback.IsConfigured() || access.cfg.Fallback.Vendor != "ark" {
		t.Fatalf("external edit masked by stale self-write mark: %+v", access.cfg.Fallback)
	}
}
