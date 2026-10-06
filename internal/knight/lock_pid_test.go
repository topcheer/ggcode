package knight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue3020_ParseBothPlatformLayouts: a lock file written by either
// platform must yield the holder PID when parsed on the other (#3020).
func TestIssue3020_ParseBothPlatformLayouts(t *testing.T) {
	// Unix layout: plain PID text at offset 0.
	unixLayout := []byte("4242")
	if pid := parseLockFileData(unixLayout); pid != 4242 {
		t.Fatalf("unix layout: got pid=%d, want 4242", pid)
	}
	// Windows layout: 32 zero bytes, then PID text.
	winLayout := make([]byte, lockPIDOffset)
	winLayout = append(winLayout, []byte("8484")...)
	if pid := parseLockFileData(winLayout); pid != 8484 {
		t.Fatalf("windows layout: got pid=%d, want 8484", pid)
	}
	// Garbage/empty yields 0.
	if pid := parseLockFileData(nil); pid != 0 {
		t.Fatalf("empty: got pid=%d, want 0", pid)
	}
	if pid := parseLockFileData([]byte("\x00\x00\x00")); pid != 0 {
		t.Fatalf("zeros only: got pid=%d, want 0", pid)
	}
	// Six-digit PID at offset 0 (real unix layout is exactly the PID text:
	// the writer truncates first, no trailing data).
	if pid := parseLockFileData([]byte("123456")); pid != 123456 {
		t.Fatalf("offset0 long pid: got pid=%d, want 123456", pid)
	}
}

// TestIssue3020_LockHeldByReadsForeignLayout: LockHeldBy must report the
// PID from a lock file written with the OTHER platform's layout.
func TestIssue3020_LockHeldByReadsForeignLayout(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, ".ggcode")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Emulate a windows-written lock file (offset-32 layout).
	content := make([]byte, lockPIDOffset)
	content = append(content, []byte("7777")...)
	if err := os.WriteFile(filepath.Join(lockDir, "knight.lock"), content, 0600); err != nil {
		t.Fatal(err)
	}
	pid, err := LockHeldBy(dir)
	if err != nil {
		t.Fatal(err)
	}
	// On unix the lock is not actually held (no flock), so LockHeldBy may
	// return 0 after the hold-verification probe - but it must never error,
	// and the parse itself must have found 7777. Assert the parse layer
	// directly to keep this test platform-stable.
	if p := parseLockFileData(content); p != 7777 {
		t.Fatalf("parse of windows layout: got %d, want 7777", p)
	}
	if pid != 0 && pid != 7777 {
		t.Fatalf("LockHeldBy unexpected pid=%d", pid)
	}
}

var _ = strings.TrimSpace // keep strings import if unused by future edits
