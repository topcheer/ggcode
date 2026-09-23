package image

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Decode must pass WebP through WITHOUT dimensions (Go's stdlib cannot
// decode WebP) instead of failing the whole call.
func TestDecodeWebPPassthroughNoDimensions(t *testing.T) {
	// 12-byte RIFF/WEBP header with a bogus body.
	data := append([]byte{
		0x52, 0x49, 0x46, 0x46, // RIFF
		0x00, 0x00, 0x00, 0x00, // size
		0x57, 0x45, 0x42, 0x50, // WEBP
	}, bytes.Repeat([]byte{0xAA}, 32)...)
	img, err := Decode(data)
	if err != nil {
		t.Fatalf("webp must pass through, got error: %v", err)
	}
	if img.MIME != MIMEWEBP {
		t.Fatalf("MIME = %q, want %q", img.MIME, MIMEWEBP)
	}
	if img.Width != 0 || img.Height != 0 {
		t.Fatalf("webp must carry no dimensions, got %dx%d", img.Width, img.Height)
	}
	if !bytes.Equal(img.Data, data) {
		t.Fatal("webp bytes must pass through unchanged")
	}
}

// A recognized non-WebP magic whose header cannot be parsed at all must fail
// with the decode error (not the webp passthrough).
func TestDecodeBrokenHeaderNonWebP(t *testing.T) {
	// PNG magic followed by garbage (no IHDR chunk).
	data := append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, bytes.Repeat([]byte{0x00}, 64)...)
	_, err := Decode(data)
	if err == nil || !strings.Contains(err.Error(), "failed to decode image") {
		t.Fatalf("expected decode failure for headerless PNG body, got %v", err)
	}
}

// GIFs are exempted from the full-decode verification (multi-frame tolerance);
// a real single-frame GIF must decode with dimensions from the header.
func TestDecodeGIFSuccess(t *testing.T) {
	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
	}
	rect := image.Rect(0, 0, 24, 18)
	p := image.NewPaletted(rect, palette)
	for x := 0; x < 24; x++ {
		for y := 0; y < 18; y++ {
			p.SetColorIndex(x, y, uint8((x+y)%2))
		}
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, p, nil); err != nil {
		t.Fatal(err)
	}
	img, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatalf("gif decode failed: %v", err)
	}
	if img.MIME != MIMEGIF {
		t.Fatalf("MIME = %q, want %q", img.MIME, MIMEGIF)
	}
	if img.Width != 24 || img.Height != 18 {
		t.Fatalf("dims %dx%d, want 24x18", img.Width, img.Height)
	}
}

func TestDetectMIMERIFFNonWebP(t *testing.T) {
	// RIFF container whose form type is not WEBP (e.g. WAV) is not an image.
	riffWav := []byte{0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00, 0x57, 0x41, 0x56, 0x45}
	if got := DetectMIME(riffWav); got != "" {
		t.Fatalf("DetectMIME(RIFF/WAVE) = %q, want empty", got)
	}
	// WEBP marker missing entirely (11 bytes) stays under the length gate.
	short := []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42}
	if got := DetectMIME(short); got != "" {
		t.Fatalf("DetectMIME(short RIFF) = %q, want empty", got)
	}
}

func TestPlaceholderKBBranch(t *testing.T) {
	img := Image{Data: make([]byte, 2048), MIME: MIMEPNG, Width: 10, Height: 10}
	if got, want := Placeholder("a.png", img), "[Image: a.png, 10x10, 2.0KB]"; got != want {
		t.Fatalf("Placeholder = %q, want %q", got, want)
	}
	// Dimensions win over the extension string.
	imgNoDim := Image{Data: make([]byte, 2048), MIME: MIMEJPEG}
	if got, want := Placeholder("b.jpg", imgNoDim), "[Image: b.jpg, .jpg, 2.0KB]"; got != want {
		t.Fatalf("Placeholder = %q, want %q", got, want)
	}
}

// ReadFile must reject regular files larger than MaxSize via the Stat
// pre-check (#438) BEFORE reading any bytes.
func TestReadFileRegularFileTooLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.png")
	if err := os.WriteFile(path, []byte{0x89, 0x50, 0x4E, 0x47}, 0644); err != nil {
		t.Fatal(err)
	}
	// Sparse truncate: instant, no real disk usage, size > MaxSize.
	if err := os.Truncate(path, int64(MaxSize)+1); err != nil {
		t.Fatal(err)
	}
	_, err := ReadFile(path)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected too-large error, got %v", err)
	}
}

func TestReadFileFullPNGSuccess(t *testing.T) {
	raw := createTestPNG(t, 33, 21)
	path := filepath.Join(t.TempDir(), "ok.png")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	img, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if img.MIME != MIMEPNG || img.Width != 33 || img.Height != 21 {
		t.Fatalf("got %s %dx%d, want png 33x21", img.MIME, img.Width, img.Height)
	}
}

// DownscaleByPixels guards: disabled cap, empty payload and the 1-pixel
// floor when the target scale rounds a dimension to zero.
func TestDownscaleByPixelsGuards(t *testing.T) {
	src := Image{Data: createTestPNG(t, 12, 34), MIME: MIMEPNG}

	for _, cap := range []int64{0, -1} {
		out := DownscaleByPixels(src, cap)
		if !bytes.Equal(out.Data, src.Data) {
			t.Fatalf("cap=%d must return input unchanged", cap)
		}
	}
	out := DownscaleByPixels(Image{Data: nil, MIME: MIMEPNG}, 1000)
	if len(out.Data) != 0 {
		t.Fatal("empty payload must return unchanged")
	}

	// 1x100 image with a tight cap: newW rounds to 0 and must clamp to 1.
	// Note: resizeImage constrains WIDTH only, so a 1px-wide source cannot
	// shrink further — the floor is the contract here, not the pixel cap.
	tall := Image{Data: createTestPNG(t, 1, 100), MIME: MIMEPNG}
	out = DownscaleByPixels(tall, 50)
	img, err := decodeImageData(out.Data)
	if err != nil {
		t.Fatalf("clamped output must decode: %v", err)
	}
	if img.Bounds().Dx() < 1 {
		t.Fatalf("width below 1-pixel floor: %v", img.Bounds())
	}
	if img.Bounds().Dy() < 1 {
		t.Fatalf("height below 1-pixel floor: %v", img.Bounds())
	}

	// A non-degenerate shape must respect the cap: 20x20 -> 7x7 (49 <= 50).
	sq := Image{Data: createTestPNG(t, 20, 20), MIME: MIMEPNG}
	out = DownscaleByPixels(sq, 50)
	img, err = decodeImageData(out.Data)
	if err != nil {
		t.Fatalf("resized output must decode: %v", err)
	}
	if px := int64(img.Bounds().Dx()) * int64(img.Bounds().Dy()); px > 50 {
		t.Fatalf("pixel cap violated: %d px (%v)", px, img.Bounds())
	}
	if out.MIME != MIMEPNG {
		t.Fatalf("downscaled MIME = %q, want png", out.MIME)
	}
}
