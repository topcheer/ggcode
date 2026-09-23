//go:build unix

package session

// sa-146: Unix-only flock lifecycle coverage (TryAcquireSessionLock,
// SessionLock.Release, IsSessionLocked, CleanupStaleLocks, readLockPIDFromFile).
// Gated behind the `unix` build tag to mirror lock_unix.go's platform split;
// Windows has its own lock_windows.go implementation.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSA146SessionLockLifecycle(t *testing.T) {
	dir := t.TempDir()
	id := "sa146-lock"
	lockPath := LockFilePath(dir, id)

	// Acquire -> Acquired() true, release clears everything.
	l1, err := TryAcquireSessionLock(dir, id)
	if err != nil {
		t.Fatalf("TryAcquireSessionLock: %v", err)
	}
	if !l1.Acquired() {
		t.Fatal("first acquisition should succeed on a fresh lock file")
	}
	l1.Release()
	l1.Release() // idempotent: second release must not panic
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should be removed after Release")
	}

	// Foreign holder: take the flock manually (same process, separate fd).
	fd, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open foreign lock: %v", err)
	}
	defer os.Remove(lockPath)
	if err := syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("foreign flock: %v", err)
	}
	if _, err := fd.WriteAt([]byte("4242"), 0); err != nil {
		t.Fatalf("write holder pid: %v", err)
	}

	l2, err := TryAcquireSessionLock(dir, id)
	if err != nil {
		t.Fatalf("TryAcquireSessionLock under contention: %v", err)
	}
	if l2.Acquired() {
		t.Fatal("contended acquisition must not report acquired")
	}
	if got := l2.HolderPID(); got != 4242 {
		t.Errorf("HolderPID = %d, want 4242 (readLockPIDFromFile path)", got)
	}
	if !IsSessionLocked(dir, id) {
		t.Error("IsSessionLocked should report true while the foreign flock is held")
	}

	// After the foreign holder exits, the lock becomes free again.
	if err := syscall.Flock(int(fd.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("foreign unlock: %v", err)
	}
	fd.Close()
	if IsSessionLocked(dir, id) {
		t.Error("IsSessionLocked should be false after the foreign flock is released")
	}
	l3, err := TryAcquireSessionLock(dir, id)
	if err != nil {
		t.Fatalf("re-acquire after foreign release: %v", err)
	}
	if !l3.Acquired() {
		t.Fatal("re-acquisition should succeed after the foreign flock is gone")
	}
	l3.Release()
}

func TestSA146CleanupStaleLocks(t *testing.T) {
	dir := t.TempDir()
	// A stale lock file (no process holds a flock on it) plus a real session
	// file that must not be touched by the *.lock glob sweep.
	stale := filepath.Join(dir, "sa146-stale.lock")
	if err := os.WriteFile(stale, []byte("999999"), 0o600); err != nil {
		t.Fatalf("create stale lock: %v", err)
	}
	sessionFile := filepath.Join(dir, "sa146-real.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}"), 0o600); err != nil {
		t.Fatalf("create session file: %v", err)
	}

	CleanupStaleLocks(dir) // must not panic on empty or missing dirs either
	CleanupStaleLocks(filepath.Join(dir, "does-not-exist"))

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale lock should have been removed: %v", err)
	}
	if _, err := os.Stat(sessionFile); err != nil {
		t.Errorf("session file must survive cleanup: %v", err)
	}
	if !strings.HasSuffix(sessionFile, ".jsonl") {
		t.Error("sanity: session file naming")
	}
}
