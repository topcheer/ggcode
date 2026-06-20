package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTryAcquireSessionLock_FirstInstance(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session-1"

	lock, err := TryAcquireSessionLock(dir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lock == nil {
		t.Fatal("expected non-nil lock")
	}
	if !lock.Acquired() {
		t.Fatal("expected lock to be acquired")
	}
	defer lock.Release()

	// Lock file should exist.
	lockPath := LockFilePath(dir, sessionID)
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("lock file should exist")
	}
}

func TestTryAcquireSessionLock_SecondInstanceDenied(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session-2"

	// First lock.
	lock1, err := TryAcquireSessionLock(dir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !lock1.Acquired() {
		t.Fatal("first lock should be acquired")
	}
	defer lock1.Release()

	// Second lock attempt should fail (return non-acquired lock).
	lock2, err := TryAcquireSessionLock(dir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lock2 == nil {
		t.Fatal("expected non-nil lock (to report holder)")
	}
	if lock2.Acquired() {
		t.Fatal("second lock should NOT be acquired")
	}
}

func TestSessionLock_Release(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session-3"

	lock1, _ := TryAcquireSessionLock(dir, sessionID)
	if !lock1.Acquired() {
		t.Fatal("first lock should be acquired")
	}
	lock1.Release()

	// After release, should be able to acquire again.
	lock2, err := TryAcquireSessionLock(dir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !lock2.Acquired() {
		t.Fatal("should acquire after release")
	}
	lock2.Release()
}

func TestIsSessionLocked(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session-4"

	if IsSessionLocked(dir, sessionID) {
		t.Fatal("should not be locked initially")
	}

	lock, _ := TryAcquireSessionLock(dir, sessionID)
	if !lock.Acquired() {
		t.Fatal("should acquire")
	}
	defer lock.Release()

	if !IsSessionLocked(dir, sessionID) {
		t.Fatal("should be locked after acquire")
	}
}

func TestIsSessionLocked_NoLockFile(t *testing.T) {
	dir := t.TempDir()
	if IsSessionLocked(dir, "nonexistent") {
		t.Fatal("should not be locked when no lock file exists")
	}
}

func TestLockFilePath(t *testing.T) {
	path := LockFilePath("/tmp/sessions", "abc-123")
	expected := filepath.Join("/tmp/sessions", "abc-123.lock")
	// LockFilePath uses "/" separator which is fine for the lock file path
	// since it's only used as a string path on all platforms.
	if path != "/tmp/sessions/abc-123.lock" {
		t.Errorf("got %q, want %q", path, expected)
	}
}
