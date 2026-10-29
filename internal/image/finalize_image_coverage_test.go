package image

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestPNG generates a valid w×h PNG at path.
func writeTestPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x7f, A: 0xff})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// finalizeImage passthrough: small PNG, no opts -> dims preserved, valid PNG out.
func TestFinalizeImage_PNGPassthrough(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.png")
	writeTestPNG(t, raw, 120, 60)
	img, err := finalizeImage(raw, ScreenshotOptions{})
	if err != nil {
		t.Fatalf("finalizeImage: %v", err)
	}
	if img.MIME != MIMEPNG {
		t.Errorf("MIME = %q, want png", img.MIME)
	}
	if img.Width != 120 || img.Height != 60 {
		t.Errorf("dims = %dx%d, want 120x60", img.Width, img.Height)
	}
	if _, err := png.Decode(bytes.NewReader(img.Data)); err != nil {
		t.Errorf("data not a valid png: %v", err)
	}
}

// MaxWidth downscale: aspect preserved and bytes re-encoded as PNG.
func TestFinalizeImage_ResizeAndReencodePNG(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.png")
	writeTestPNG(t, raw, 400, 200)
	img, err := finalizeImage(raw, ScreenshotOptions{MaxWidth: 100})
	if err != nil {
		t.Fatalf("finalizeImage: %v", err)
	}
	if img.Width != 100 || img.Height != 50 {
		t.Fatalf("dims = %dx%d, want 100x50", img.Width, img.Height)
	}
	dec, err := png.Decode(bytes.NewReader(img.Data))
	if err != nil {
		t.Fatalf("re-encoded data not png: %v", err)
	}
	if dec.Bounds().Dx() != 100 || dec.Bounds().Dy() != 50 {
		t.Errorf("encoded bounds = %v, want 100x50", dec.Bounds())
	}
}

// Format conversion to JPEG with explicit quality.
func TestFinalizeImage_JPEGConversion(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.png")
	writeTestPNG(t, raw, 64, 32)
	img, err := finalizeImage(raw, ScreenshotOptions{Format: "jpeg", Quality: 80})
	if err != nil {
		t.Fatalf("finalizeImage: %v", err)
	}
	if img.MIME != MIMEJPEG {
		t.Errorf("MIME = %q, want jpeg", img.MIME)
	}
	if _, err := jpeg.Decode(bytes.NewReader(img.Data)); err != nil {
		t.Errorf("data not jpeg: %v", err)
	}
}

// RawOutputPath gets the untouched bytes; OutputPath gets the finalized bytes.
func TestFinalizeImage_RawAndFinalOutputPaths(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.png")
	rawOut := filepath.Join(dir, "raw-out.png")
	finalOut := filepath.Join(dir, "final.jpg")
	writeTestPNG(t, raw, 64, 32)
	rawBytes, err := os.ReadFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	img, err := finalizeImage(raw, ScreenshotOptions{Format: "jpeg", RawOutputPath: rawOut, OutputPath: finalOut})
	if err != nil {
		t.Fatalf("finalizeImage: %v", err)
	}
	if got, err := os.ReadFile(rawOut); err != nil || !bytes.Equal(got, rawBytes) {
		t.Errorf("RawOutputPath mismatch (err=%v)", err)
	}
	if got, err := os.ReadFile(finalOut); err != nil || !bytes.Equal(got, img.Data) {
		t.Errorf("OutputPath mismatch (err=%v)", err)
	}
}

func TestFinalizeImage_ReadError(t *testing.T) {
	_, err := finalizeImage(filepath.Join(t.TempDir(), "missing.png"), ScreenshotOptions{})
	if err == nil || !strings.Contains(err.Error(), "reading screenshot file") {
		t.Errorf("want reading error, got %v", err)
	}
}

// #2998: detectable PNG magic but undecodable body must fail explicitly,
// never silently return zero-dim Image.
func TestFinalizeImage_UndecodableBytes(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "trunc.png")
	body := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, make([]byte, 40)...)
	if err := os.WriteFile(raw, body, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := finalizeImage(raw, ScreenshotOptions{}); err == nil || !strings.Contains(err.Error(), "decoding screenshot bytes") {
		t.Errorf("want decode error, got %v", err)
	}
}

// Zero/negative delay must be a no-op (no sleep on hot path).
func TestApplyDelay_ZeroOrNegativeNoOp(t *testing.T) {
	applyDelay(0)
	applyDelay(-5)
}

func TestWmctrlTitleOffset(t *testing.T) {
	line := "0x01a00001 0 host Term.term My Window Title"
	if got := wmctrlTitleOffset(line); got != strings.Index(line, "My Window") {
		t.Errorf("offset = %d, want %d", got, strings.Index(line, "My Window"))
	}
	// Multi-space column padding is skipped as a unit.
	padded := "0x01a00001   0   host   Term.term   Title"
	if got := wmctrlTitleOffset(padded); got != strings.Index(padded, "Title") {
		t.Errorf("padded offset = %d, want %d", got, strings.Index(padded, "Title"))
	}
	// Degenerate short line: offset clamps to end of line.
	if got := wmctrlTitleOffset("0x01a"); got != len("0x01a") {
		t.Errorf("short offset = %d, want %d", got, len("0x01a"))
	}
}

// current beats preferred beats first-listed; a current pick is sticky.
func TestWlrModePick_OfferPrecedence(t *testing.T) {
	var p wlrModePick
	p.offer(640, 480, false, false) // first listed
	if !p.seen || p.width != 640 || p.height != 480 {
		t.Fatalf("first offer not recorded: %+v", p)
	}
	p.offer(800, 600, false, true) // preferred beats first
	if p.width != 800 || !p.preferred || p.current {
		t.Fatalf("preferred should replace first: %+v", p)
	}
	p.offer(1024, 768, true, false) // current beats preferred
	if p.width != 1024 || !p.current {
		t.Fatalf("current should replace preferred: %+v", p)
	}
	p.offer(500, 400, true, false) // current is sticky
	if p.width != 1024 || p.height != 768 {
		t.Fatalf("current pick must be sticky: %+v", p)
	}
}
