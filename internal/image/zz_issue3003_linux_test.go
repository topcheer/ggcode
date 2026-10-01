//go:build linux

package image

// Regression probes for #3003 follow-up: Display is 1-based with 0=primary
// (screenshot_common.go). The >=1 gate fix (07abd1b64) left Display==0 - the
// documented default, "primary" - falling through to grim's all-outputs
// composite. These probes pin the primary resolution.

import (
	"errors"
	"testing"
)

func withDisplayResolver(t *testing.T, fn func(display int) (ScreenshotRegion, error)) {
	t.Helper()
	prev := linuxDisplayRegionForFn
	linuxDisplayRegionForFn = fn
	t.Cleanup(func() { linuxDisplayRegionForFn = prev })
}

// TestIssue3003_Display0ResolvesPrimary: 0=primary must resolve an explicit
// region, never the all-outputs composite.
func TestIssue3003_Display0ResolvesPrimary(t *testing.T) {
	withDisplayResolver(t, func(display int) (ScreenshotRegion, error) {
		if display != 0 {
			t.Fatalf("resolver must receive Display=0, got %d", display)
		}
		return ScreenshotRegion{X: 1920, Y: 0, Width: 1512, Height: 982}, nil
	})
	opts, err := prepareLinuxCaptureOpts("grim", ScreenshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Region == nil || opts.Region.X != 1920 {
		t.Fatalf("Display=0 must resolve the primary region, got %+v", opts.Region)
	}
}

// TestIssue3003_Display1StillResolved: the merged >=1 gate (07abd1b64) stays
// intact - Display=1 keeps linuxDisplayBounds and never reaches the primary
// resolver.
func TestIssue3003_Display1StillResolved(t *testing.T) {
	withDisplayResolver(t, func(display int) (ScreenshotRegion, error) {
		t.Fatal("primary resolver must not be called for Display >= 1")
		return ScreenshotRegion{}, nil
	})
	// Display=1 reaches linuxDisplayBounds directly; without xrandr/wlr-randr
	// on the test host it silently falls back (no region, no error) - the
	// #555 contract. The key assertion is that the seam above is untouched.
	opts, err := prepareLinuxCaptureOpts("grim", ScreenshotOptions{Display: 1})
	if err != nil {
		t.Fatal(err)
	}
	_ = opts
}

// TestIssue3003_ResolverFailureKeepsFallback: when primary resolution fails
// (no xrandr/wlr-randr), the tool falls back to its default output silently.
func TestIssue3003_ResolverFailureKeepsFallback(t *testing.T) {
	withDisplayResolver(t, func(display int) (ScreenshotRegion, error) {
		return ScreenshotRegion{}, errors.New("no display info tool found")
	})
	opts, err := prepareLinuxCaptureOpts("grim", ScreenshotOptions{})
	if err != nil {
		t.Fatalf("resolution failure must not error: %v", err)
	}
	if opts.Region != nil {
		t.Fatalf("failed resolution must not set a region, got %+v", opts.Region)
	}
}

// TestIssue3003_ExplicitRegionAndWindowPrecedence: a user-supplied Region or
// Window target must bypass primary resolution entirely.
func TestIssue3003_ExplicitRegionAndWindowPrecedence(t *testing.T) {
	withDisplayResolver(t, func(display int) (ScreenshotRegion, error) {
		t.Fatal("resolver must not be called when Region is already set")
		return ScreenshotRegion{}, nil
	})
	user := &ScreenshotRegion{X: 1, Y: 2, Width: 10, Height: 10}
	if opts, err := prepareLinuxCaptureOpts("grim", ScreenshotOptions{Region: user}); err != nil || opts.Region != user {
		t.Fatalf("user region must be preserved, opts=%+v err=%v", opts.Region, err)
	}

	withDisplayResolver(t, func(display int) (ScreenshotRegion, error) {
		t.Fatal("resolver must not be called when Window is set")
		return ScreenshotRegion{}, nil
	})
	if opts, err := prepareLinuxCaptureOpts("scrot", ScreenshotOptions{Window: "Firefox"}); err != nil || opts.Region != nil {
		t.Fatalf("window targets must not gain a region, opts=%+v err=%v", opts.Region, err)
	}
}

// TestIssue3003_PrimaryDisplayIndex pins the pure helper: flagged primary
// wins; nothing flagged falls back to the first display.
func TestIssue3003_PrimaryDisplayIndex(t *testing.T) {
	if got := primaryDisplayIndex([]DisplayInfo{{Name: "DP-1"}, {Name: "HDMI-A-1", IsPrimary: true}}); got != 2 {
		t.Fatalf("flagged primary at slot 2 must resolve index 2, got %d", got)
	}
	if got := primaryDisplayIndex([]DisplayInfo{{Name: "DP-1"}, {Name: "HDMI-A-1"}}); got != 1 {
		t.Fatalf("no primary flag must fall back to first display, got %d", got)
	}
	if got := primaryDisplayIndex(nil); got != 1 {
		t.Fatalf("empty list must still return 1 (first display), got %d", got)
	}
}
