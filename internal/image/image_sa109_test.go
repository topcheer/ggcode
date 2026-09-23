package image

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sa109MakePNG renders a solid-color w x h image and encodes it as PNG.
func sa109MakePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeClipboardImageDataSa109(t *testing.T) {
	pngData := sa109MakePNG(t, 3, 2)
	tests := []struct {
		name      string
		data      []byte
		wantMIME  string
		wantW, H  int
		wantEmpty bool
		wantErr   bool
	}{
		{name: "valid png", data: pngData, wantMIME: MIMEPNG, wantW: 3, H: 2},
		{name: "garbage bytes", data: []byte("definitely not an image"), wantErr: true},
		{name: "png magic truncated body", data: pngData[:10], wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img, err := decodeClipboardImageData(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got Image{MIME: %s}", img.MIME)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if img.MIME != tt.wantMIME || img.Width != tt.wantW || img.Height != tt.H {
				t.Fatalf("got {MIME:%s W:%d H:%d}, want {MIME:%s W:%d H:%d}", img.MIME, img.Width, img.Height, tt.wantMIME, tt.wantW, tt.H)
			}
		})
	}
}

func TestCommandOutputErrorSa109(t *testing.T) {
	if err := commandOutputError("prefix", nil, []byte("ignored")); err != nil {
		t.Fatalf("nil error should propagate as nil, got %v", err)
	}
	err := commandOutputError("prefix", errors.New("boom"), []byte("  \n\t "))
	if err == nil {
		t.Fatal("expected wrapped error for empty output")
	}
	if !strings.Contains(err.Error(), "prefix: boom") || strings.Contains(err.Error(), "boom:") {
		t.Fatalf("empty output must wrap without trailing output segment, got %q", err.Error())
	}
}

func TestEncodeJPEGBytesSa109(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	tests := []struct {
		name    string
		quality int
	}{{"zero clamps to 85", 0}, {"negative clamps to 85", -3}, {"above 100 clamps to 100", 150}, {"normal", 60}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := encodeJPEGBytes(img, tt.quality)
			if err != nil {
				t.Fatalf("encodeJPEGBytes(q=%d): %v", tt.quality, err)
			}
			if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
				t.Fatalf("expected JPEG magic bytes, got %d bytes", len(data))
			}
		})
	}
}

func TestEncodePNGBytesSa109(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	data, err := encodePNGBytes(img)
	if err != nil {
		t.Fatalf("encodePNGBytes: %v", err)
	}
	if !bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47}) {
		t.Fatalf("expected PNG magic bytes, got %d bytes", len(data))
	}
}

func TestApplyDelaySa109(t *testing.T) {
	start := time.Now()
	applyDelay(0)
	applyDelay(-5)
	if d := time.Since(start); d > 20*time.Millisecond {
		t.Fatalf("non-positive delay must not sleep, took %v", d)
	}
	start = time.Now()
	applyDelay(5)
	if d := time.Since(start); d < 5*time.Millisecond {
		t.Fatalf("positive delay must sleep, took %v", d)
	}
}

func TestFinalizeImageSa109(t *testing.T) {
	dir := t.TempDir()
	smallPNG := sa109MakePNG(t, 4, 2)
	rawPath := filepath.Join(dir, "raw.png")
	if err := os.WriteFile(rawPath, smallPNG, 0644); err != nil {
		t.Fatal(err)
	}
	widePNG := sa109MakePNG(t, 40, 20)
	widePath := filepath.Join(dir, "wide.png")
	if err := os.WriteFile(widePath, widePNG, 0644); err != nil {
		t.Fatal(err)
	}
	garbage := []byte("twelve-byte-garbage") // >=12 bytes, no image magic
	garbagePath := filepath.Join(dir, "garbage.bin")
	if err := os.WriteFile(garbagePath, garbage, 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		rawPath    string
		opts       ScreenshotOptions
		wantErrSub string
		check      func(t *testing.T, img Image)
	}{
		{
			name:       "missing raw file errors",
			rawPath:    filepath.Join(dir, "nope.png"),
			wantErrSub: "reading screenshot file",
		},
		{
			name:    "raw output copy",
			rawPath: rawPath,
			opts:    ScreenshotOptions{RawOutputPath: filepath.Join(dir, "copy.png")},
			check: func(t *testing.T, img Image) {
				copied, err := os.ReadFile(filepath.Join(dir, "copy.png"))
				if err != nil {
					t.Fatalf("raw copy not written: %v", err)
				}
				if !bytes.Equal(copied, smallPNG) {
					t.Fatal("raw copy must equal source bytes")
				}
				if img.Width != 4 || img.Height != 2 || img.MIME != MIMEPNG {
					t.Fatalf("got {W:%d H:%d MIME:%s}", img.Width, img.Height, img.MIME)
				}
			},
		},
		{
			name:    "png passthrough without resize keeps data",
			rawPath: rawPath,
			opts:    ScreenshotOptions{MaxWidth: 4}, // width == maxW: no resize
			check: func(t *testing.T, img Image) {
				if !bytes.Equal(img.Data, smallPNG) {
					t.Fatal("no-resize PNG must keep original bytes")
				}
			},
		},
		{
			name:    "undecodable data defaults to png mime",
			rawPath: garbagePath,
			check: func(t *testing.T, img Image) {
				if img.MIME != MIMEPNG {
					t.Fatalf("MIME = %s, want default %s", img.MIME, MIMEPNG)
				}
				if !bytes.Equal(img.Data, garbage) {
					t.Fatal("undecodable data must pass through unchanged")
				}
				if img.Width != 0 || img.Height != 0 {
					t.Fatalf("undecodable data must not set dimensions, got %dx%d", img.Width, img.Height)
				}
			},
		},
		{
			name:    "jpeg conversion default quality",
			rawPath: rawPath,
			opts:    ScreenshotOptions{Format: "JPEG"}, // case-insensitive path
			check: func(t *testing.T, img Image) {
				if img.MIME != MIMEJPEG {
					t.Fatalf("MIME = %s, want %s", img.MIME, MIMEJPEG)
				}
				if len(img.Data) < 3 || img.Data[0] != 0xFF || img.Data[1] != 0xD8 {
					t.Fatal("expected JPEG bytes after conversion")
				}
			},
		},
		{
			name:    "jpeg conversion explicit quality",
			rawPath: rawPath,
			opts:    ScreenshotOptions{Format: "jpeg", Quality: 42},
			check: func(t *testing.T, img Image) {
				if img.MIME != MIMEJPEG || len(img.Data) == 0 {
					t.Fatalf("got {MIME:%s len:%d}", img.MIME, len(img.Data))
				}
			},
		},
		{
			name:    "resize beyond max width re-encodes png",
			rawPath: widePath,
			opts:    ScreenshotOptions{MaxWidth: 10},
			check: func(t *testing.T, img Image) {
				if img.Width != 10 || img.Height != 5 {
					t.Fatalf("resized to %dx%d, want 10x5", img.Width, img.Height)
				}
				if !bytes.HasPrefix(img.Data, []byte{0x89, 0x50, 0x4E, 0x47}) {
					t.Fatal("resized image must be re-encoded as PNG")
				}
			},
		},
		{
			name:    "finalized output written to output path",
			rawPath: rawPath,
			opts:    ScreenshotOptions{OutputPath: filepath.Join(dir, "final.png")},
			check: func(t *testing.T, img Image) {
				written, err := os.ReadFile(filepath.Join(dir, "final.png"))
				if err != nil {
					t.Fatalf("finalized output not written: %v", err)
				}
				if !bytes.Equal(written, img.Data) {
					t.Fatal("output path file must match returned image data")
				}
			},
		},
		{
			name:       "raw output write error",
			rawPath:    rawPath,
			opts:       ScreenshotOptions{RawOutputPath: filepath.Join(dir, "missing-dir", "raw.png")},
			wantErrSub: "writing raw screenshot",
		},
		{
			name:       "finalized output write error",
			rawPath:    rawPath,
			opts:       ScreenshotOptions{OutputPath: filepath.Join(dir, "missing-dir", "final.png")},
			wantErrSub: "writing finalized screenshot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img, err := finalizeImage(tt.rawPath, tt.opts)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, img)
			}
		})
	}
}
