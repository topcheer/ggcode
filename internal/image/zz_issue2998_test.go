package image

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// zz_issue2998_test.go - regression probes for #2998: finalizeImage silently
// swallowed both decode and encode failures. Undecodable screenshot bytes
// fell through the `if err == nil` shape and returned (raw, nil) - the
// user's Format/MaxWidth/Quality options silently ignored; encode failures
// kept raw Data paired with RESIZED Width/Height metadata. Both paths must
// now return explicit errors (#975 explicit-failure precedent, same file).

func TestIssue2998UndecodableBytesFailExplicitly(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "garbage.png")
	if err := os.WriteFile(raw, []byte("not a png or jpeg at all"), 0644); err != nil {
		t.Fatal(err)
	}
	img, err := finalizeImage(raw, ScreenshotOptions{})
	if err == nil {
		t.Fatalf("#2998: undecodable screenshot bytes must fail explicitly, got (img, nil) with Data=%d bytes", len(img.Data))
	}
	if img.Data != nil {
		t.Fatalf("#2998: failed finalize must not return partial image data")
	}
}

func TestIssue2998ValidImageStillSucceeds(t *testing.T) {
	// Build a real tiny PNG on disk; finalize must succeed with accurate
	// metadata (guards against over-eager error returns).
	src := filepath.Join(t.TempDir(), "ok.png")
	pngBytes := encodeTinyPNG(t)
	if err := os.WriteFile(src, pngBytes, 0644); err != nil {
		t.Fatal(err)
	}
	img, err := finalizeImage(src, ScreenshotOptions{})
	if err != nil {
		t.Fatalf("#2998: valid screenshot must succeed, got %v", err)
	}
	if img.Width <= 0 || img.Height <= 0 {
		t.Fatalf("#2998: metadata must be populated, got %dx%d", img.Width, img.Height)
	}
	if len(img.Data) == 0 {
		t.Fatalf("#2998: data must be present")
	}
}

func encodeTinyPNG(t *testing.T) []byte {
	t.Helper()
	src := image.NewRGBA(image.Rect(0, 0, 40, 30))
	src.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
