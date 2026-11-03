//go:build !darwin && !linux && !windows

package image

import "fmt"

func CaptureScreen(opts ScreenshotOptions) (ScreenshotResult, error) {
	return ScreenshotResult{}, fmt.Errorf("screenshot is not supported on this platform")
}

func ListDisplays() ([]DisplayInfo, error) {
	return nil, fmt.Errorf("listing displays is not supported on this platform")
}

func ListWindows() ([]WindowInfo, error) {
	return nil, fmt.Errorf("listing windows is not supported on this platform")
}
