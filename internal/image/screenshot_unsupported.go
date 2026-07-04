//go:build !darwin && !linux && !windows

package image

import "fmt"

// CaptureScreen is a stub on unsupported platforms.
func CaptureScreen(opts ScreenshotOptions) (ScreenshotResult, error) {
	return ScreenshotResult{}, fmt.Errorf("screenshot is not supported on this platform")
}

// ListDisplays is a stub on unsupported platforms.
func ListDisplays() ([]DisplayInfo, error) {
	return nil, fmt.Errorf("listing displays is not supported on this platform")
}

// ListWindows is a stub on unsupported platforms.
func ListWindows() ([]WindowInfo, error) {
	return nil, fmt.Errorf("listing windows is not supported on this platform")
}
