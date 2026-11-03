package restart

import (
	"testing"
)

func TestResolveBinary(t *testing.T) {

	path, err := ResolveBinary()
	if err != nil {
		t.Fatalf("ResolveBinary failed: %v", err)
	}
	if path == "" {
		t.Fatal("ResolveBinary returned empty path")
	}
}
