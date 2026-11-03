package agentruntime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	writeMCPYAML(t, globalPath, "global-srv")
	if err := os.Chtimes(globalPath, now, now); err != nil {
		t.Fatal(err)
	}

	w := NewMCPHotReload(globalDir, ws, nil)

	for _, p := range w.watchedPaths() {
		w.seedState(p)
	}

	writeMCPYAML(t, wsPath, "ws-renamed")
	editTime := now.Add(-30 * time.Minute)
	if err := os.Chtimes(wsPath, editTime, editTime); err != nil {
		t.Fatal(err)
	}

	if !w.pathChanged(wsPath) {
		t.Fatal("#521 Bug B: workspace edit masked by newer global file timestamp (single-watermark regression)")
	}

	if w.pathChanged(globalPath) {
		t.Fatal("#521: untouched global file reported as changed")
	}
}

func TestMCPHotReload_TouchWithoutContentChangeNoReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	ws := t.TempDir()
	wsPath := filepath.Join(ws, "mcp_servers.yaml")
	writeMCPYAML(t, wsPath, "ws-only-srv")

	w := NewMCPHotReload(globalDir, ws, nil)
	for _, p := range w.watchedPaths() {
		w.seedState(p)
	}

	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(wsPath, future, future); err != nil {
		t.Fatal(err)
	}
	if w.pathChanged(wsPath) {
		t.Fatal("#521 Bug A: mtime-only bump with identical content must not fire a reload")
	}

	writeMCPYAML(t, wsPath, "ws-renamed")
	if err := os.Chtimes(wsPath, future.Add(time.Minute), future.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !w.pathChanged(wsPath) {
		t.Fatal("#521: real content change on watched path not detected")
	}
}

func TestMCPHotReload_LateAppearingScopeFileDetected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	ws := t.TempDir()
	wsPath := filepath.Join(ws, "mcp_servers.yaml")

	w := NewMCPHotReload(globalDir, ws, nil)
	for _, p := range w.watchedPaths() {
		w.seedState(p)
	}

	writeMCPYAML(t, wsPath, "ws-only-srv")
	if !w.pathChanged(wsPath) {
		t.Fatal("#521: late-appearing workspace mcp_servers.yaml not detected as change")
	}
}
