//go:build goolm

package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIssue647_ZeroOriginSurvivesDirtyMerge(t *testing.T) {
	withTestHome(t)

	if err := os.MkdirAll(filepath.Join(homeDir647(t), ".ggcode"), 0755); err != nil {
		t.Fatal(err)
	}
	old := map[string]any{"window_width": 1280, "window_height": 860, "window_x": 250, "window_y": 100, "window_position_set": true}
	oldJSON, _ := json.Marshal(old)
	if err := os.WriteFile(desktopConfigPath(), oldJSON, 0600); err != nil {
		t.Fatal(err)
	}

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

func homeDir647(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return home
}
