//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"testing"
)

func withTestHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestDesktopConfig_Defaults(t *testing.T) {
	withTestHome(t)
	dc := LoadDesktopConfig()
	if dc.WindowW != 1280 || dc.WindowH != 860 {
		t.Fatalf("expected defaults 1280x860, got %dx%d", dc.WindowW, dc.WindowH)
	}
}

func TestDesktopConfig_MissingFile(t *testing.T) {
	withTestHome(t)
	dc := LoadDesktopConfig()
	if dc.WorkDir != "" || dc.LastSession != "" {
		t.Fatal("expected empty values when file missing")
	}
}

func TestDesktopConfig_SaveLoadRoundTrip(t *testing.T) {
	withTestHome(t)

	dc := &DesktopConfig{
		WorkDir:     "/home/user/project",
		WindowW:     1920,
		WindowH:     1080,
		LastSession: "sess-123",
		Language:    "zh-CN",
	}
	if err := dc.Save(); err != nil {
		t.Fatal(err)
	}

	loaded := LoadDesktopConfig()
	if loaded.WorkDir != "/home/user/project" {
		t.Fatalf("WorkDir mismatch: %q", loaded.WorkDir)
	}
	if loaded.WindowW != 1920 || loaded.WindowH != 1080 {
		t.Fatalf("window size mismatch: %dx%d", loaded.WindowW, loaded.WindowH)
	}
	if loaded.LastSession != "sess-123" {
		t.Fatalf("LastSession mismatch: %q", loaded.LastSession)
	}
	if loaded.Language != "zh-CN" {
		t.Fatalf("Language mismatch: %q", loaded.Language)
	}
}

func TestDesktopConfig_SetWorkDir(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{WindowW: 100, WindowH: 100}
	dc.SetWorkDir("/new/path")
	if dc.WorkDir != "/new/path" {
		t.Fatalf("expected /new/path, got %q", dc.WorkDir)
	}
}

func TestDesktopConfig_SetLastSession(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{WindowW: 100, WindowH: 100}
	dc.SetLastSession("sess-456")
	if dc.LastSession != "sess-456" {
		t.Fatalf("expected sess-456, got %q", dc.LastSession)
	}
}

func TestDesktopConfig_SetWindowState(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{WindowW: 100, WindowH: 100}
	dc.SetWindowState(1920, 1080, 250, 100, true)
	if dc.WindowW != 1920 || dc.WindowH != 1080 {
		t.Fatalf("size mismatch: %dx%d", dc.WindowW, dc.WindowH)
	}
	if dc.WindowX != 250 || dc.WindowY != 100 {
		t.Fatalf("position mismatch: %d,%d", dc.WindowX, dc.WindowY)
	}
	if !dc.WindowMax {
		t.Fatal("expected maximized=true")
	}
}

func TestDesktopConfig_WindowStateRoundTrip(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{}
	dc.SetWindowState(1600, 900, 300, 200, true)
	if err := dc.Save(); err != nil {
		t.Fatal(err)
	}
	loaded := LoadDesktopConfig()
	if loaded.WindowX != 300 || loaded.WindowY != 200 {
		t.Fatalf("position round-trip mismatch: %d,%d", loaded.WindowX, loaded.WindowY)
	}
	if !loaded.WindowMax {
		t.Fatal("maximized flag lost in round-trip")
	}
}

func TestDesktopConfig_CreatesDirectoryIfMissing(t *testing.T) {
	withTestHome(t)

	// Ensure ~/.ggcode doesn't exist
	ggcodeDir := filepath.Join(os.Getenv("HOME"), ".ggcode")
	if _, err := os.Stat(ggcodeDir); !os.IsNotExist(err) {
		t.Fatal("expected .ggcode to not exist yet")
	}

	dc := &DesktopConfig{WindowW: 800, WindowH: 600}
	if err := dc.Save(); err != nil {
		t.Fatal(err)
	}

	// Directory should now exist
	if _, err := os.Stat(ggcodeDir); err != nil {
		t.Fatalf("expected .ggcode to exist after Save: %v", err)
	}
}

func TestDesktopConfig_NotificationsDefault(t *testing.T) {
	withTestHome(t)
	dc := LoadDesktopConfig()
	// Default should be enabled (even though JSON field is omitted/zero-value)
	if !dc.IsNotificationsEnabled() {
		t.Fatal("expected notifications enabled by default")
	}
}

func TestDesktopConfig_SetNotificationsEnabled(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{WindowW: 100, WindowH: 100}
	dc.SetNotificationsEnabled(false)
	if dc.IsNotificationsEnabled() {
		t.Fatal("expected notifications disabled after SetNotificationsEnabled(false)")
	}
	dc.SetNotificationsEnabled(true)
	if !dc.IsNotificationsEnabled() {
		t.Fatal("expected notifications enabled after SetNotificationsEnabled(true)")
	}
}

func TestDesktopConfig_NotificationsRoundTrip(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{WindowW: 100, WindowH: 100}
	dc.SetNotificationsEnabled(false)
	if err := dc.Save(); err != nil {
		t.Fatal(err)
	}
	loaded := LoadDesktopConfig()
	if loaded.IsNotificationsEnabled() {
		t.Fatal("expected notifications disabled after round-trip")
	}
}

func TestDesktopConfig_AlwaysOnTopDefault(t *testing.T) {
	withTestHome(t)
	dc := LoadDesktopConfig()
	if dc.IsAlwaysOnTop() {
		t.Fatal("expected always-on-top to default to false")
	}
}

func TestDesktopConfig_SetAlwaysOnTop(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{WindowW: 100, WindowH: 100}
	dc.SetAlwaysOnTop(true)
	if !dc.IsAlwaysOnTop() {
		t.Fatal("expected always-on-top true after SetAlwaysOnTop(true)")
	}
	dc.SetAlwaysOnTop(false)
	if dc.IsAlwaysOnTop() {
		t.Fatal("expected always-on-top false after SetAlwaysOnTop(false)")
	}
}

func TestDesktopConfig_AlwaysOnTopRoundTrip(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{WindowW: 100, WindowH: 100}
	dc.SetAlwaysOnTop(true)
	if err := dc.Save(); err != nil {
		t.Fatal(err)
	}
	loaded := LoadDesktopConfig()
	if !loaded.IsAlwaysOnTop() {
		t.Fatal("expected always-on-top true after round-trip")
	}
}

func TestDesktopConfig_FontZoomDefault(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{WindowW: 100, WindowH: 100}
	if got := dc.GetFontZoom(); got != 1.0 {
		t.Fatalf("expected default zoom 1.0, got %v", got)
	}
}

func TestDesktopConfig_FontZoomSetGet(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{WindowW: 100, WindowH: 100}
	dc.SetFontZoom(1.5)
	if got := dc.GetFontZoom(); got != 1.5 {
		t.Fatalf("expected zoom 1.5, got %v", got)
	}
	dc.SetFontZoom(0.8)
	if got := dc.GetFontZoom(); got != 0.8 {
		t.Fatalf("expected zoom 0.8, got %v", got)
	}
}

func TestDesktopConfig_FontZoomRoundTrip(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{WindowW: 100, WindowH: 100}
	dc.SetFontZoom(1.3)
	if err := dc.Save(); err != nil {
		t.Fatal(err)
	}
	loaded := LoadDesktopConfig()
	if got := loaded.GetFontZoom(); got != 1.3 {
		t.Fatalf("expected zoom 1.3 after round-trip, got %v", got)
	}
}
