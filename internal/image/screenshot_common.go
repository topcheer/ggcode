package image

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ScreenshotAction specifies what the screenshot tool should do.
type ScreenshotAction string

const (
	ActionCapture      ScreenshotAction = "capture"
	ActionListDisplays ScreenshotAction = "list_displays"
	ActionListWindows  ScreenshotAction = "list_windows"
)

// ScreenshotOptions controls screenshot capture behavior.
type ScreenshotOptions struct {
	Display    int               // 1-based monitor index, 0=primary
	Window     string            // match by title/app name
	Region     *ScreenshotRegion // rectangular area
	Cursor     bool              // include mouse cursor
	DelayMs    int               // delay before capture
	Format     string            // "png" or "jpeg"
	Quality    int               // JPEG quality 1-100
	OutputPath string            // save to file
	MaxWidth   int               // auto-resize max width
}

// ScreenshotRegion defines a rectangular area to capture.
type ScreenshotRegion struct {
	X, Y, Width, Height int
}

// DisplayInfo describes a monitor.
type DisplayInfo struct {
	Index     int    `json:"index"`
	IsPrimary bool   `json:"is_primary"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Name      string `json:"name,omitempty"`
}

// WindowInfo describes a capturable window.
type WindowInfo struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	App    string `json:"app"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// ScreenshotResult is the output of CaptureScreen.
type ScreenshotResult struct {
	Image     Image
	SavedPath string
}

// applyDelay waits for opts.DelayMs if non-zero.
func applyDelay(delayMs int) {
	if delayMs > 0 {
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
	}
}

// finalizeImage reads the raw screenshot file, applies format conversion
// and resize, then returns the final Image.
func finalizeImage(rawPath string, opts ScreenshotOptions) (Image, error) {
	data, err := os.ReadFile(rawPath)
	if err != nil {
		return Image{}, fmt.Errorf("reading screenshot file: %w", err)
	}

	mime := DetectMIME(data)
	if mime == "" {
		mime = MIMEPNG
	}

	img := Image{Data: data, MIME: mime}

	decoded, err := decodeImageData(data)
	if err == nil {
		img.Width = decoded.Bounds().Dx()
		img.Height = decoded.Bounds().Dy()

		maxW := opts.MaxWidth
		if maxW == 0 {
			maxW = 1920
		}

		target := decoded
		if img.Width > maxW {
			target = resizeNearestNeighbor(decoded, maxW)
			img.Width = target.Bounds().Dx()
			img.Height = target.Bounds().Dy()
		}

		// Re-encode if format conversion or resize is needed.
		format := strings.ToLower(opts.Format)
		if format == "" {
			format = "png"
		}

		if format == "jpeg" {
			q := opts.Quality
			if q == 0 {
				q = 85
			}
			buf, err := encodeJPEGBytes(target, q)
			if err == nil {
				img.Data = buf
				img.MIME = MIMEJPEG
			}
		} else if img.Width > maxW || target != decoded {
			// Re-encode PNG after resize.
			buf, err := encodePNGBytes(target)
			if err == nil {
				img.Data = buf
				img.MIME = MIMEPNG
			}
		}
	}

	return img, nil
}

func decodeImageData(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// resizeNearestNeighbor resizes src so its width does not exceed maxW,
// maintaining aspect ratio using nearest-neighbor sampling.
func resizeNearestNeighbor(src image.Image, maxW int) image.Image {
	bounds := src.Bounds()
	oldW := bounds.Dx()
	oldH := bounds.Dy()
	if oldW <= maxW {
		return src
	}
	newW := maxW
	newH := oldH * newW / oldW
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		sy := y * oldH / newH
		for x := 0; x < newW; x++ {
			sx := x * oldW / newW
			dst.Set(x, y, src.At(bounds.Min.X+sx, bounds.Min.Y+sy))
		}
	}
	return dst
}

// createTempScreenshotPath creates a temp or output file path.
func createTempScreenshotPath(opts ScreenshotOptions) (string, func()) {
	if opts.OutputPath != "" {
		return opts.OutputPath, func() {}
	}
	tmpDir, _ := os.MkdirTemp("", "ggcode-screenshot-*")
	ext := "png"
	if strings.ToLower(opts.Format) == "jpeg" {
		ext = "jpg"
	}
	return filepath.Join(tmpDir, "screenshot."+ext), func() { os.RemoveAll(tmpDir) }
}
