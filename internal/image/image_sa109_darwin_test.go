//go:build darwin

package image

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsNoClipboardImageErrorSa109(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "missing value", err: errors.New("The clipboard whose content is «missing value»"), want: true},
		{name: "missing value case-insensitive", err: errors.New("MISSING VALUE detected"), want: true},
		{name: "can't get", err: errors.New("Can't get the clipboard"), want: true},
		{name: "doesn't contain", err: errors.New("doesn't contain an image"), want: true},
		{name: "type of", err: errors.New("type of clipboard content"), want: true},
		{name: "tcc denial surfaces", err: errors.New("Execution error: Not authorized to send Apple events"), want: false},
		{name: "exec failure surfaces", err: errors.New("exit status 1"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNoClipboardImageError(tt.err); got != tt.want {
				t.Fatalf("isNoClipboardImageError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestConvertClipboardTIFFToPngSa109(t *testing.T) {
	if !commandAvailable("sips") {
		t.Skip("sips not available")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.png")
	if err := os.WriteFile(src, sa109MakePNG(t, 5, 3), 0644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out.png")
	if err := convertClipboardTIFFToPNG(src, dst); err != nil {
		t.Fatalf("convert failed: %v", err)
	}
	out, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("output not written: %v", err)
	}
	if !bytes.HasPrefix(out, []byte{0x89, 0x50, 0x4E, 0x47}) {
		t.Fatalf("output is not PNG (%d bytes)", len(out))
	}

	// sips exits 0 on missing inputs; corrupt content (exit 13) exercises the
	// CombinedOutput error-wrapping path instead.
	badSrc := filepath.Join(dir, "corrupt.tiff")
	if err := os.WriteFile(badSrc, []byte("not-an-image-data"), 0644); err != nil {
		t.Fatal(err)
	}
	err = convertClipboardTIFFToPNG(badSrc, dst)
	if err == nil || !strings.Contains(err.Error(), "converting clipboard image") {
		t.Fatalf("expected wrapped command error, got %v", err)
	}
}
