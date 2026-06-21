package restart

import (
	"testing"
)

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
