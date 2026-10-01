package image

// Regression probes for #3013: the gnome-screenshot unsupported-opts gate
// must stay in sync with the Region-translation gate in
// prepareLinuxCaptureOpts (Display >= 1, #3003). The old `> 1` gate let an
// explicit Display=1 request through; buildGnomeScreenshotCommand ignores
// the translated Region and gnome-screenshot returned a composite of ALL
// outputs with exit 0, blocking the fallback to region-capable tools.

import (
	"strings"
	"testing"
)

func TestIssue3013_GnomeGateRejectsExplicitDisplayIndexes(t *testing.T) {
	for _, display := range []int{1, 2, 3} {
		err := gnomeScreenshotUnsupportedOpts(ScreenshotOptions{Display: display})
		if err == nil {
			t.Fatalf("explicit Display=%d must be rejected for gnome-screenshot", display)
		}
		if !strings.Contains(err.Error(), "cannot select a display by index") {
			t.Fatalf("Display=%d error must name the per-display limitation, got: %v", display, err)
		}
	}
}

func TestIssue3013_GnomeGateAllowsImplicitDefault(t *testing.T) {
	// Display==0 is the implicit primary default of a plain capture: no
	// display intent, gnome full-screen behavior stays available
	// (single-head setups get the correct shot).
	if err := gnomeScreenshotUnsupportedOpts(ScreenshotOptions{Display: 0}); err != nil {
		t.Fatalf("implicit Display=0 must stay allowed, got: %v", err)
	}
}

func TestIssue3013_GnomeGateStillRejectsRegion(t *testing.T) {
	err := gnomeScreenshotUnsupportedOpts(ScreenshotOptions{
		Region: &ScreenshotRegion{X: 0, Y: 0, Width: 100, Height: 100},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot capture a region") {
		t.Fatalf("explicit Region must keep being rejected, got: %v", err)
	}
}
