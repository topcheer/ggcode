//go:build windows

package image

import (
	"strings"
	"testing"
)

// TestWindowsScreenshotScriptSetsDpiAwarenessBeforeCapture guards #763/#976
// on the capture side: DPI awareness must be declared before any geometry is
// queried so window bounds and CopyFromScreen operate in physical pixels.
func TestWindowsScreenshotScriptSetsDpiAwarenessBeforeCapture(t *testing.T) {
	script := buildWindowsScreenshotScript(`C:\out.png`, ScreenshotOptions{})

	dpiIdx := strings.Index(script, "SetProcessDPIAware")
	if dpiIdx < 0 {
		t.Fatal("screenshot script is missing the SetProcessDPIAware preamble")
	}
	captureIdx := strings.Index(script, "CopyFromScreen")
	if captureIdx < 0 {
		t.Fatal("screenshot script no longer captures")
	}
	if dpiIdx > captureIdx {
		t.Fatalf("DPI awareness must be declared before capture (dpi=%d, capture=%d)", dpiIdx, captureIdx)
	}
}

// TestWindowsScreenshotWindowScriptUsesExtendedFrameBounds guards the #976
// cosmetic fix: window captures should prefer DWMWA_EXTENDED_FRAME_BOUNDS
// (attribute 9) over GetWindowRect, which includes the invisible resize
// borders (~7px per side on Win10/11), and must keep the GetWindowRect
// fallback for environments without DWM.
func TestWindowsScreenshotWindowScriptUsesExtendedFrameBounds(t *testing.T) {
	script := buildWindowsScreenshotScript(`C:\out.png`, ScreenshotOptions{Window: "notepad"})

	if !strings.Contains(script, "DwmGetWindowAttribute") {
		t.Fatal("window capture script should query DWMWA_EXTENDED_FRAME_BOUNDS")
	}
	if !strings.Contains(script, "DwmGetWindowAttribute($p.MainWindowHandle, 9, [ref]$rect") {
		t.Fatal("window capture script should pass DWMWA_EXTENDED_FRAME_BOUNDS (9) for the visible rect")
	}
	if !strings.Contains(script, "GetWindowRect($p.MainWindowHandle") {
		t.Fatal("window capture script must keep the GetWindowRect fallback when DWM is unavailable")
	}
}
