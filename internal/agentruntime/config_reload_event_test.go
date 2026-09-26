package agentruntime

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfigHotReload_ReloadListenerApplied verifies an applied cycle
// delivers Applied=true together with the post-merge field snapshot, so the
// frontend can tell the user their edit converged.
func TestConfigHotReload_ReloadListenerApplied(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")

	access := newTestAccess(t, cfgPath)
	events := make(chan ConfigReloadEvent, 4)
	w := NewConfigHotReload(cfgPath, access)
	w.SetReloadListener(func(e ConfigReloadEvent) { events <- e })

	// Edit: add a fallback, then poll.
	writeConfig(t, cfgPath, "anthropic")
	w.pollOnce()

	select {
	case e := <-events:
		if !e.Applied {
			t.Fatalf("expected applied event, got rejection: %v", e.Err)
		}
		if !e.Fallback {
			t.Fatalf("expected fallback=true after merge, got %+v", e)
		}
	default:
		t.Fatal("no reload event delivered for an applied cycle")
	}
}

// TestConfigHotReload_ReloadListenerRejected verifies a broken edit delivers
// Applied=false with the parse error while the last good config survives.
func TestConfigHotReload_ReloadListenerRejected(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "anthropic")

	access := newTestAccess(t, cfgPath)
	events := make(chan ConfigReloadEvent, 4)
	w := NewConfigHotReload(cfgPath, access)
	w.SetReloadListener(func(e ConfigReloadEvent) { events <- e })

	// Break the file mid-edit (editors do this transiently).
	if err := os.WriteFile(cfgPath, []byte("vendor: [broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.pollOnce()

	select {
	case e := <-events:
		if e.Applied {
			t.Fatal("broken yaml must not produce an applied event")
		}
		if e.Err == nil {
			t.Fatal("rejected event must carry the parse error")
		}
	default:
		t.Fatal("no rejection event delivered for a broken edit")
	}
	if !access.cfg.Fallback.IsConfigured() {
		t.Fatal("broken yaml must not wipe the last good config")
	}
}

// TestConfigHotReload_NoListenerDefaultSafe verifies the watcher stays a
// no-op when no listener is registered: pipe/daemon callers keep their
// previous behavior (debug logs only).
func TestConfigHotReload_NoListenerDefaultSafe(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")

	access := newTestAccess(t, cfgPath)
	w := NewConfigHotReload(cfgPath, access)

	// Edit and poll without any listener; must neither panic nor fail.
	writeConfig(t, cfgPath, "anthropic")
	w.pollOnce()

	if !access.cfg.Fallback.IsConfigured() {
		t.Fatal("fallback should still refresh without a listener")
	}
}
