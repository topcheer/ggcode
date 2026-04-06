//go:build !darwin && !linux && !windows

package image

import (
	"fmt"
	"runtime"
)

func ReadClipboard() (Image, error) {
	return Image{}, fmt.Errorf("clipboard image paste is not supported on %s", runtime.GOOS)
}
