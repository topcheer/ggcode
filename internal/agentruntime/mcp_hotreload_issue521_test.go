package agentruntime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #521 Bug B: a single global mtime watermark let the NEWER global file
// permanently mask a workspace mcp_servers.yaml edit whose mtime stayed
// below the global's. Per-path (mtime, sha256) state must detect the
// workspace edit regardless of the global file's timestamp.
//
// Timeline (all set explicitly via os.Chtimes — no sleeps):
//   - workspace baseline: T-2h   (older)
//   - global file:       T       (newest — this is what masked the edit)
//   - workspace edit:    T-30m   (still older than global, newer than own baseline)
func TestMCPHotReload_WorkspaceEditNotMaskedByNewerGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(globalDir, "mcp_servers.yaml")
	ws := t.TempDir()
	wsPath := filepath.Join(ws, "mcp_servers.yaml")

	writeMCPYAML(t, wsPath, "ws-only-srv")
	now := time.Now()
	if err := os.Chtimes(wsPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	writeMCPYAML(t, globalPath, "global-srv") // global is the NEWEST file
	if err := os.Chtimes(globalPath, now, now); err != nil {
		t.Fatal(err)
	}

	w := NewMCPHotReload(globalDir, ws, nil)
	// Seed per-path baselines exactly as Start() does (no goroutine).
	for _, p := range w.watchedPaths() {
		w.seedState(p)
	}

	// Edit the workspace file's CONTENT with an mtime still below the global's.
	writeMCPYAML(t, wsPath, "ws-renamed")
	editTime := now.Add(-30 * time.Minute)
	if err := os.Chtimes(wsPath, editTime, editTime); err != nil {
		t.Fatal(err)
	}

	if !w.pathChanged(wsPath) {
		t.Fatal("#521 Bug B: workspace edit masked by newer global file timestamp (single-watermark regression)")
	}
	// The untouched global file must not report a change.
	if w.pathChanged(globalPath) {
		t.Fatal("#521: untouched global file reported as changed")
	}
}

// #521 Bug A: extra watched paths (workspace mcp_servers.yaml) previously
// had NO content-hash debounce — only the global file did. An mtime bump
// with identical content must not fire a reload on ANY watched path.
func TestMCPHotReload_TouchWithoutContentChangeNoReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode") // no global file
	ws := t.TempDir()
	wsPath := filepath.Join(ws, "mcp_servers.yaml")
	writeMCPYAML(t, wsPath, "ws-only-srv")

	w := NewMCPHotReload(globalDir, ws, nil)
	for _, p := range w.watchedPaths() {
		w.seedState(p)
	}

	// Simulate `touch`: same content, mtime bumped into the future.
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(wsPath, future, future); err != nil {
		t.Fatal(err)
	}
	if w.pathChanged(wsPath) {
		t.Fatal("#521 Bug A: mtime-only bump with identical content must not fire a reload")
	}

	// A real content change (same future mtime) must still fire.
	writeMCPYAML(t, wsPath, "ws-renamed")
	if err := os.Chtimes(wsPath, future.Add(time.Minute), future.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !w.pathChanged(wsPath) {
		t.Fatal("#521: real content change on watched path not detected")
	}
}

// #521: files that appear AFTER Start() (scope file created later) must be
// treated as a change so fresh scope configs hot-reload.
func TestMCPHotReload_LateAppearingScopeFileDetected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	ws := t.TempDir()
	wsPath := filepath.Join(ws, "mcp_servers.yaml")
	// No files exist yet.

	w := NewMCPHotReload(globalDir, ws, nil)
	for _, p := range w.watchedPaths() {
		w.seedState(p) // records absence
	}

	writeMCPYAML(t, wsPath, "ws-only-srv")
	if !w.pathChanged(wsPath) {
		t.Fatal("#521: late-appearing workspace mcp_servers.yaml not detected as change")
	}
}
