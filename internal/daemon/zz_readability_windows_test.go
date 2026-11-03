//go:build windows

package daemon

import (
	"os"
	"testing"
)

func makePIDFileUnreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove PID file for dir-swap: %v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir in place of PID file: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
}
