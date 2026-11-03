package image

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
