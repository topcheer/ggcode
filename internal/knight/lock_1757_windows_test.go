//go:build windows

package knight

import (
	"os"
	"path/filepath"
	"testing"
)

// #1757 case 2: ENOENT means "not held" (0, nil); other OpenFile failures
// (e.g. permission) must surface as errors like the unix branch.
func TestLockHeldByWindowsENOENTVsPerm1757(t *testing.T) {
	dir := t.TempDir()
	// No lock file at all.
	pid, err := LockHeldBy(dir)
	if pid != 0 || err != nil {
		t.Fatalf("missing lock file: got (%d, %v), want (0, nil)", pid, err)
	}

	// Lock file exists but is unreadable/unopenable -> error, not silence.
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
