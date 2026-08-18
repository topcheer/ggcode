//go:build goolm

package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Issue #647: mergeFromDirty kept X/Y behind the legacy `!= 0` guards while
// WindowPosSet was persisted via the dirty flag. A window parked at the (0,0)
// origin saved "WindowPosSet=true" but the zero coords were dropped in favor
// of the stale on-disk position — next launch restored the OLD coordinates
// from a contradictory persisted state.
func TestIssue647_ZeroOriginSurvivesDirtyMerge(t *testing.T) {
	withTestHome(t)

	// Disk state from a previous session: window at (250,100).
	if err := os.MkdirAll(filepath.Join(homeDir647(t), ".ggcode"), 0755); err != nil {
		t.Fatal(err)
	}
	old := map[string]any{"window_width": 1280, "window_height": 860, "window_x": 250, "window_y": 100, "window_position_set": true}
	oldJSON, _ := json.Marshal(old)
	if err := os.WriteFile(desktopConfigPath(), oldJSON, 0600); err != nil {
		t.Fatal(err)
	}

	// This session dragged the window to the (0,0) origin and quit.
	dc := LoadDesktopConfig()
	dc.SetWindowState(1280, 860, 0, 0, false)
	if err := dc.Save(); err != nil {
		t.Fatal(err)
	}

	loaded := LoadDesktopConfig()
	if !loaded.WindowPosSet {
		t.Fatal("WindowPosSet lost — (0,0) origin regressed to unset")
	}
	if loaded.WindowX != 0 || loaded.WindowY != 0 {
		t.Fatalf("#647: zero origin dropped by dirty merge, stale coords persisted: (%d,%d)", loaded.WindowX, loaded.WindowY)
	}
}

// Partial zero (x=0 only, y nonzero) must likewise write BOTH new values —
// the old guards kept the stale X while writing the new Y, corrupting the
// restored position on one axis.
func TestIssue647_PartialZeroAxis(t *testing.T) {
	withTestHome(t)
	if err := os.MkdirAll(filepath.Join(homeDir647(t), ".ggcode"), 0755); err != nil {
		t.Fatal(err)
	}
	old := map[string]any{"window_width": 1000, "window_height": 700, "window_x": 400, "window_y": 300, "window_position_set": true}
	oldJSON, _ := json.Marshal(old)
	if err := os.WriteFile(desktopConfigPath(), oldJSON, 0600); err != nil {
		t.Fatal(err)
	}

	dc := LoadDesktopConfig()
	dc.SetWindowState(1000, 700, 0, 42, false)
	if err := dc.Save(); err != nil {
		t.Fatal(err)
	}

	loaded := LoadDesktopConfig()
	if loaded.WindowX != 0 || loaded.WindowY != 42 {
		t.Fatalf("#647: partial-zero origin mangled: got (%d,%d), want (0,42)", loaded.WindowX, loaded.WindowY)
	}
}

// Untouched window state must still be preserved from disk (#635 semantics):
// a stale instance that never ran SetWindowState must not write bounds at
// all — including its zero values.
func TestIssue647_UntouchedInstanceKeepsDiskBounds(t *testing.T) {
	withTestHome(t)
	if err := os.MkdirAll(filepath.Join(homeDir647(t), ".ggcode"), 0755); err != nil {
		t.Fatal(err)
	}
	old := map[string]any{"window_width": 1100, "window_height": 750, "window_x": 30, "window_y": 60, "window_position_set": true}
	oldJSON, _ := json.Marshal(old)
	if err := os.WriteFile(desktopConfigPath(), oldJSON, 0600); err != nil {
		t.Fatal(err)
	}

	// Stale in-memory snapshot (loaded pre-disk-change) with zeroed bounds,
	// never dirtied by SetWindowState — e.g. an instance that only toggled
	// notifications. Its zeroed window fields must NOT reach the disk.
	dc := &DesktopConfig{WindowW: 0, WindowH: 0, WindowX: 0, WindowY: 0, WindowPosSet: false}
	dc.SetNotificationsEnabled(false)
	if err := dc.Save(); err != nil {
		t.Fatal(err)
	}

	loaded := LoadDesktopConfig()
	if loaded.WindowX != 30 || loaded.WindowY != 60 || !loaded.WindowPosSet {
		t.Fatalf("#647 regression of #635: untouched instance clobbered disk window state: %+v", loaded)
	}
}

// homeDir647 returns the HOME set by withTestHome via t.Setenv.
func homeDir647(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return home
}
