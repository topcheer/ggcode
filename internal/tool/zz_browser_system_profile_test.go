package tool

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSystemProfile_IsolatedDataDir: profile "system" must NOT reuse the real
// Chrome user data dir (user directive 2026-09-24, Windows report): reusing it
// while Chrome is running fails - the dir is locked, the new chrome.exe hands
// off to the existing instance, and Chrome 136+ disables CDP on the default
// dir. "system" must resolve to a dedicated persistent profile directory under
// ~/.ggcode/browser-profiles/system, exactly like a named profile.
func TestSystemProfile_IsolatedDataDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	b := NewBrowser()
	p, err := b.getProfile("system", nil)
	if err != nil {
		t.Fatalf("getProfile(system) failed: %v", err)
	}
	if p == nil {
		t.Fatal("getProfile(system) returned nil profile")
	}

	wantDir := filepath.Join(homeDir(), ".ggcode", "browser-profiles", "system")
	if info, err := os.Stat(wantDir); err != nil || !info.IsDir() {
		t.Fatalf("system profile must use dedicated dir %s (got err=%v)", wantDir, err)
	}

	// Repeated resolution is stable (same profile object, dir untouched).
	p2, err := b.getProfile("system", nil)
	if err != nil || p2 != p {
		t.Fatalf("getProfile(system) must be stable across calls: err=%v same=%v", err, p2 == p)
	}
}

// TestNamedProfile_StillIsolated: named profiles keep their own directory and
// "default" does not create one (chromedp temp dir).
func TestNamedProfile_StillIsolated(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	b := NewBrowser()
	if _, err := b.getProfile("default", nil); err != nil {
		t.Fatalf("getProfile(default) failed: %v", err)
	}
	namedRoot := filepath.Join(homeDir(), ".ggcode", "browser-profiles")
	if _, err := os.Stat(filepath.Join(namedRoot, "default")); err == nil {
		t.Error("default profile must not create a persistent directory")
	}
}
