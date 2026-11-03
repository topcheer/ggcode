//go:build unix

package daemon

import (
	"os"
	"testing"
)

func makePIDFileUnreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("cannot chmod (running as root?): %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
}
