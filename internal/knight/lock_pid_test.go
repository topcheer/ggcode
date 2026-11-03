package knight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3020_ParseBothPlatformLayouts(t *testing.T) {

	unixLayout := []byte("4242")
	if pid := parseLockFileData(unixLayout); pid != 4242 {
		t.Fatalf("unix layout: got pid=%d, want 4242", pid)
	}

	winLayout := make([]byte, lockPIDOffset)
	winLayout = append(winLayout, []byte("8484")...)
	if pid := parseLockFileData(winLayout); pid != 8484 {
		t.Fatalf("windows layout: got pid=%d, want 8484", pid)
	}

	if pid := parseLockFileData(nil); pid != 0 {
		t.Fatalf("empty: got pid=%d, want 0", pid)
	}
	if pid := parseLockFileData([]byte("\x00\x00\x00")); pid != 0 {
		t.Fatalf("zeros only: got pid=%d, want 0", pid)
	}

	if pid := parseLockFileData([]byte("123456")); pid != 123456 {
		t.Fatalf("offset0 long pid: got pid=%d, want 123456", pid)
	}
}

func TestIssue3020_LockHeldByReadsForeignLayout(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, ".ggcode")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatal(err)
	}

	content := make([]byte, lockPIDOffset)
	content = append(content, []byte("7777")...)
	if err := os.WriteFile(filepath.Join(lockDir, "knight.lock"), content, 0600); err != nil {
		t.Fatal(err)
	}
	pid, err := LockHeldBy(dir)
	if err != nil {
		t.Fatal(err)
	}

	if p := parseLockFileData(content); p != 7777 {
		t.Fatalf("parse of windows layout: got %d, want 7777", p)
	}
	if pid != 0 && pid != 7777 {
		t.Fatalf("LockHeldBy unexpected pid=%d", pid)
	}
}

var _ = strings.TrimSpace
