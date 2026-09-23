package image

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// finalizeImage is the shared post-capture pipeline: format detection,
// max-width resize, JPEG/PNG conversion and output-path persistence.
func TestFinalizeImageMissingFile(t *testing.T) {
	_, err := finalizeImage(filepath.Join(t.TempDir(), "nope.png"), ScreenshotOptions{})
	if err == nil || !strings.Contains(err.Error(), "reading screenshot file") {
		t.Fatalf("expected read error, got %v", err)
	}
}

func TestFinalizeImageGarbageForcesPNGMIME(t *testing.T) {
	// Undecodable bytes: MIME falls back to PNG and dimensions stay 0,
	// but the raw bytes must pass through untouched.
	raw := []byte("definitely not an image at all")
	path := filepath.Join(t.TempDir(), "raw.bin")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	img, err := finalizeImage(path, ScreenshotOptions{})
	if err != nil {
		t.Fatalf("finalizeImage: %v", err)
	}
	if img.MIME != MIMEPNG {
		t.Fatalf("MIME = %q, want fallback %q", img.MIME, MIMEPNG)
	}
	if img.Width != 0 || img.Height != 0 {
		t.Fatalf("garbage must not yield dimensions, got %dx%d", img.Width, img.Height)
	}
	if !bytes.Equal(img.Data, raw) {
		t.Fatal("garbage bytes must pass through unchanged")
	}
}

func TestFinalizeImagePNGPassthroughByteIdentical(t *testing.T) {
	raw := createTestPNG(t, 100, 60)
	path := filepath.Join(t.TempDir(), "raw.png")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	img, err := finalizeImage(path, ScreenshotOptions{})
	if err != nil {
		t.Fatalf("finalizeImage: %v", err)
	}
	if img.MIME != MIMEPNG || img.Width != 100 || img.Height != 60 {
		t.Fatalf("got %s %dx%d, want png 100x60", img.MIME, img.Width, img.Height)
	}
	if !bytes.Equal(img.Data, raw) {
		t.Fatal("unresized PNG within max width must stay byte-identical")
	}
}

func TestFinalizeImageResizesToMaxWidth(t *testing.T) {
	raw := createTestPNG(t, 2400, 600)
	path := filepath.Join(t.TempDir(), "wide.png")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	img, err := finalizeImage(path, ScreenshotOptions{MaxWidth: 800})
	if err != nil {
		t.Fatalf("finalizeImage: %v", err)
	}
	if img.Width != 800 || img.Height != 200 {
		t.Fatalf("resized to %dx%d, want 800x200", img.Width, img.Height)
	}
	if bytes.Equal(img.Data, raw) {
		t.Fatal("resized image must be re-encoded")
	}
	if _, err := Decode(img.Data); err != nil {
		t.Fatalf("resized output must decode: %v", err)
	}
}

func TestFinalizeImageJPEGConversion(t *testing.T) {
	raw := createTestPNG(t, 50, 40)
	path := filepath.Join(t.TempDir(), "raw.png")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	// Default quality (0 -> 85) and case-insensitive format.
	for _, tc := range []struct {
		name    string
		format  string
		quality int
	}{
		{"default-quality", "jpeg", 0},
		{"uppercase-overclamp", "JPEG", 150},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, err := finalizeImage(path, ScreenshotOptions{Format: tc.format, Quality: tc.quality})
			if err != nil {
				t.Fatalf("finalizeImage: %v", err)
			}
			if img.MIME != MIMEJPEG {
				t.Fatalf("MIME = %q, want jpeg", img.MIME)
			}
			assertJPEG(t, img.Data)
			if img.Width != 50 || img.Height != 40 {
				t.Fatalf("dims %dx%d, want 50x40", img.Width, img.Height)
			}
		})
	}
}

func TestFinalizeImageJPEGRawPassthrough(t *testing.T) {
	img0 := saTestImg(40, 30)
	jpegData, err := encodeJPEGBytes(img0, 90)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "raw.jpg")
	if err := os.WriteFile(path, jpegData, 0644); err != nil {
		t.Fatal(err)
	}
	img, err := finalizeImage(path, ScreenshotOptions{})
	if err != nil {
		t.Fatalf("finalizeImage: %v", err)
	}
	// Default png format with no resize must NOT re-encode a JPEG input.
	if img.MIME != MIMEJPEG {
		t.Fatalf("MIME = %q, want jpeg passthrough", img.MIME)
	}
	if !bytes.Equal(img.Data, jpegData) {
		t.Fatal("unresized non-png input must stay byte-identical")
	}
	if img.Width != 40 || img.Height != 30 {
		t.Fatalf("dims %dx%d, want 40x30", img.Width, img.Height)
	}
}

func TestFinalizeImageRawAndOutputPaths(t *testing.T) {
	raw := createTestPNG(t, 30, 20)
	path := filepath.Join(t.TempDir(), "raw.png")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rawOut := filepath.Join(dir, "kept-raw.png")
	finalOut := filepath.Join(dir, "final.png")
	if _, err := finalizeImage(path, ScreenshotOptions{RawOutputPath: rawOut, OutputPath: finalOut}); err != nil {
		t.Fatalf("finalizeImage: %v", err)
	}
	for _, p := range []string{rawOut, finalOut} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s not written: %v", p, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty", p)
		}
	}
	finalData, _ := os.ReadFile(finalOut)
	if DetectMIME(finalData) != MIMEPNG {
		t.Fatalf("finalized output MIME = %q", DetectMIME(finalData))
	}
}

func TestFinalizeImageWriteErrorsSurface(t *testing.T) {
	raw := createTestPNG(t, 20, 20)
	path := filepath.Join(t.TempDir(), "raw.png")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	badDir := filepath.Join(t.TempDir(), "missing", "sub")

	if _, err := finalizeImage(path, ScreenshotOptions{RawOutputPath: filepath.Join(badDir, "r.png")}); err == nil || !strings.Contains(err.Error(), "writing raw screenshot") {
		t.Fatalf("expected raw write error, got %v", err)
	}
	if _, err := finalizeImage(path, ScreenshotOptions{OutputPath: filepath.Join(badDir, "f.png")}); err == nil || !strings.Contains(err.Error(), "writing finalized screenshot") {
		t.Fatalf("expected finalized write error, got %v", err)
	}
}

// TestResizeImageEarlyReturn exercises the within-cap shortcut: the source
// must be returned untouched so callers can skip re-encoding.
func TestResizeImageEarlyReturn(t *testing.T) {
	src := saTestImg(10, 10)
	if got := resizeImage(src, 100); got != src {
		t.Fatal("image narrower than maxW must be returned as-is")
	}
}

// TestFinalizeImageRoundTripsResizedPNG verifies the saved OutputPath equals
// the returned finalized bytes when a resize happened.
func TestFinalizeImageRoundTripsResizedPNG(t *testing.T) {
	raw := createTestPNG(t, 1000, 500)
	path := filepath.Join(t.TempDir(), "raw.png")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.png")
	img, err := finalizeImage(path, ScreenshotOptions{MaxWidth: 500, OutputPath: out})
	if err != nil {
		t.Fatalf("finalizeImage: %v", err)
	}
	saved, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, img.Data) {
		t.Fatal("saved OutputPath must match returned finalized bytes")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(saved))
	if err != nil {
		t.Fatalf("saved output not a PNG: %v", err)
	}
	if cfg.Width != 500 || cfg.Height != 250 {
		t.Fatalf("saved dims %dx%d, want 500x250", cfg.Width, cfg.Height)
	}
}
