package image

import (
	"image"
	"image/color"
	"testing"
	"time"
)

func saTestImg(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	return img
}

func assertJPEG(t *testing.T, data []byte) {
	t.Helper()
	if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
		t.Fatalf("not a JPEG stream: first bytes % x", data[:minInt(3, len(data))])
	}
	if DetectMIME(data) != MIMEJPEG {
		t.Fatalf("DetectMIME on encoder output = %q, want %q", DetectMIME(data), MIMEJPEG)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// encodeJPEGBytes is the shared JPEG re-encode path for screenshot
// finalization; quality clamps (<=0 -> 85, >100 -> 100) must still produce a
// decodable JPEG stream.
func TestEncodeJPEGBytesQualityClamp(t *testing.T) {
	img := saTestImg(32, 16)
	for _, q := range []int{0, -5, 1, 40, 100, 150} {
		data, err := encodeJPEGBytes(img, q)
		if err != nil {
			t.Fatalf("encodeJPEGBytes(quality=%d): %v", q, err)
		}
		assertJPEG(t, data)
	}
}

func TestEncodePNGBytes(t *testing.T) {
	data, err := encodePNGBytes(saTestImg(16, 16))
	if err != nil {
		t.Fatalf("encodePNGBytes: %v", err)
	}
	if DetectMIME(data) != MIMEPNG {
		t.Fatalf("DetectMIME on encoder output = %q, want %q", DetectMIME(data), MIMEPNG)
	}
	if img, err := decodeImageData(data); err != nil {
		t.Fatalf("re-decode failed: %v", err)
	} else if img.Bounds().Dx() != 16 || img.Bounds().Dy() != 16 {
		t.Fatalf("re-decoded dims %dx%d, want 16x16", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestApplyDelay(t *testing.T) {
	// Zero/negative delay must return promptly.
	start := time.Now()
	applyDelay(0)
	applyDelay(-100)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("zero delay slept %v", d)
	}

	// Positive delay actually waits (lenient lower bound to avoid flakes).
	start = time.Now()
	applyDelay(20)
	if d := time.Since(start); d < 10*time.Millisecond {
		t.Fatalf("20ms delay returned after %v", d)
	}
}
