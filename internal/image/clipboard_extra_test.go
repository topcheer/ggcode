package image

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func saClipboardPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	img.Set(3, 2, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecodeClipboardImageDataPaths(t *testing.T) {
	// Empty clipboard payload maps to the sentinel "no image" error.
	if _, err := decodeClipboardImageData(nil); !errors.Is(err, ErrClipboardImageUnavailable) {
		t.Fatalf("nil data: got %v, want ErrClipboardImageUnavailable", err)
	}
	if _, err := decodeClipboardImageData([]byte{}); !errors.Is(err, ErrClipboardImageUnavailable) {
		t.Fatalf("empty data: got %v, want ErrClipboardImageUnavailable", err)
	}

	// A real clipboard PNG decodes with dimensions.
	img, err := decodeClipboardImageData(saClipboardPNG(t))
	if err != nil {
		t.Fatalf("valid PNG: %v", err)
	}
	if img.MIME != MIMEPNG || img.Width != 8 || img.Height != 6 {
		t.Fatalf("got %s %dx%d, want png 8x6", img.MIME, img.Width, img.Height)
	}

	// Truncated/corrupt payload surfaces the decode error verbatim.
	broken := append([]byte{}, saClipboardPNG(t)...)
	if _, err := decodeClipboardImageData(broken[:20]); err == nil || errors.Is(err, ErrClipboardImageUnavailable) {
		t.Fatalf("corrupt payload: got %v, want non-sentinel error", err)
	}
}

func TestCommandOutputErrorPaths(t *testing.T) {
	// nil error -> nil, regardless of output.
	if err := commandOutputError("prefix", nil, []byte("ignored")); err != nil {
		t.Fatalf("nil error must stay nil, got %v", err)
	}
	// Error with no output keeps the bare message.
	sentinel := errors.New("exit status 1")
	err := commandOutputError("reading clipboard image", sentinel, []byte("   \n\t "))
	if err == nil || err.Error() != "reading clipboard image: exit status 1" {
		t.Fatalf("empty output: got %v", err)
	}
	// Error with output appends the trimmed stderr/stdout.
	err = commandOutputError("converting clipboard image", sentinel, []byte("  sips: bad file  \n"))
	want := "converting clipboard image: exit status 1: sips: bad file"
	if err == nil || err.Error() != want {
		t.Fatalf("with output: got %q, want %q", err, want)
	}
}
