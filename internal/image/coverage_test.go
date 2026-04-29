package image

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommandAvailable(t *testing.T) {
	// ls should exist on all platforms
	if !commandAvailable("ls") {
		t.Error("expected ls to be available")
	}
	if commandAvailable("nonexistent-command-xyz-123") {
		t.Error("expected nonexistent command to not be available")
	}
}

func TestIsImageFileExtra(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"document.pdf", false},
		{"script.sh", false},
		{"noext", false},
	}
	for _, tt := range tests {
		got := IsImageFile(tt.path)
		if got != tt.want {
			t.Errorf("IsImageFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestReadFile_NotFound(t *testing.T) {
	_, err := ReadFile("/nonexistent/image.png")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestReadFile_ValidPNG(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.png")
	// Write minimal PNG header
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if err := os.WriteFile(path, pngHeader, 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFile(path)
	// May or may not succeed depending on decoder, but shouldn't panic
	_ = err
}
