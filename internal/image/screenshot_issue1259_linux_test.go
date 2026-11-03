//go:build linux

package image

import (
	"strings"
	"testing"
)

func TestBuildScrotCommandWindowResolutionFailure(t *testing.T) {

	cmd, err := buildScrotCommand("/tmp/out.png", ScreenshotOptions{Window: "DefinitelyNotARealWindowTitle-#1259"})
	if err == nil {
		if cmd == nil {
			t.Fatal("no command and no error")
		}

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
