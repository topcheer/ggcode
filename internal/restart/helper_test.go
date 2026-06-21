package restart

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSwapBinary(t *testing.T) {
	tmp := t.TempDir()

	// Create target and staged files
	target := filepath.Join(tmp, "target")
	staged := filepath.Join(tmp, "staged")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := swapBinary(target, staged); err != nil {
		t.Fatalf("swapBinary failed: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("target not swapped: got %q want %q", string(data), "new")
	}
}

func TestResolveBinary(t *testing.T) {
	// Just verify it doesn't panic and returns something non-empty.
	path, err := ResolveBinary()
	if err != nil {
		t.Fatalf("ResolveBinary failed: %v", err)
	}
	if path == "" {
		t.Fatal("ResolveBinary returned empty path")
	}
}
