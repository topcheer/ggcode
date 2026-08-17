//go:build windows

package daemon

import (
	"os"
	"testing"
)

// makePIDFileUnreadable emulates an unreadable PID file for #520/#552-A
// tests. Windows chmod only toggles the readonly attribute, which never
// blocks reads, so instead we replace the file with a directory of the
// same name: os.ReadFile then fails with a non-NotExist error — the
// real-world equivalent of #520's EACCES/EIO scenario — while os.Stat
// still succeeds, letting the tests assert the propagate-don't-delete
// branch. (A zero-share handle cannot be used here: WritePIDFile already
// holds a share-read/write/delete handle, which nobody can deny after
// the fact.)
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
