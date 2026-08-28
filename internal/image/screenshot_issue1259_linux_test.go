//go:build linux

package image

// Regression test for #1259 (scrot window fallback must error, not shoot the
// focused window). See also zz_issue975_test.go for the shared parsers.

import (
	"strings"
	"testing"
)

// TestBuildScrotCommandWindowResolutionFailure pins #1259: when wmctrl cannot
// resolve the requested window, buildScrotCommand must return an error
// instead of silently appending "-u" (focused window) — the old fallback
// returned a screenshot of the WRONG window with a success status.
func TestBuildScrotCommandWindowResolutionFailure(t *testing.T) {
	// "DefinitelyNotARealWindowTitle..." cannot resolve via wmctrl on any
	// sane test environment (missing wmctrl or no match both produce an
	// error from matchLinuxWindowID).
	cmd, err := buildScrotCommand("/tmp/out.png", ScreenshotOptions{Window: "DefinitelyNotARealWindowTitle-#1259"})
	if err == nil {
		if cmd == nil {
			t.Fatal("no command and no error")
		}
		// If wmctrl DID somehow resolve it, still verify no "-u" was used.
		for _, arg := range cmd.Args {
			if arg == "-u" {
				t.Fatal("#1259: focused-window fallback -u must never be used")
			}
		}
		return
	}
	if !strings.Contains(err.Error(), "window capture with scrot") {
		t.Fatalf("expected actionable scrot window error, got: %v", err)
	}
}

// TestBuildScrotCommandRegionNoWindow: region captures without a window
// target must keep building successfully (no window-resolution involved).
func TestBuildScrotCommandRegionNoWindow(t *testing.T) {
	cmd, err := buildScrotCommand("/tmp/out.png", ScreenshotOptions{
		Region: &ScreenshotRegion{X: 0, Y: 0, Width: 100, Height: 100},
	})
	if err != nil {
		t.Fatalf("region capture must not fail: %v", err)
	}
	found := false
	for _, arg := range cmd.Args {
		if arg == "-a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("region capture must pass -a, args = %v", cmd.Args)
	}
}
