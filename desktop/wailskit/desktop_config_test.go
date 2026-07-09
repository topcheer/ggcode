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
