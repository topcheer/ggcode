//go:build linux

package image

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

func TestIssue3003_Display1StillResolved(t *testing.T) {
	withDisplayResolver(t, func(display int) (ScreenshotRegion, error) {
		t.Fatal("primary resolver must not be called for Display >= 1")
		return ScreenshotRegion{}, nil
	})

	opts, err := prepareLinuxCaptureOpts("grim", ScreenshotOptions{Display: 1})
	if err != nil {
		t.Fatal(err)
	}
	_ = opts
}

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
