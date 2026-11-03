//go:build windows

package knight

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockHeldByWindowsENOENTVsPerm1757(t *testing.T) {
	dir := t.TempDir()

	pid, err := LockHeldBy(dir)
	if pid != 0 || err != nil {
		t.Fatalf("missing lock file: got (%d, %v), want (0, nil)", pid, err)
	}

	lockDir := filepath.Join(dir, ".ggcode")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(lockDir, "knight.lock")
	if err := os.WriteFile(lockPath, []byte("    "), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := LockHeldBy(dir); err == nil {
		t.Fatal("permission failure must surface as an error, not (0, nil)")
	}
}
