package knight

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTryAcquireLock_FirstInstance(t *testing.T) {
	dir := t.TempDir()

	lock := tryAcquireLock(dir)
	if lock == nil {
		t.Fatal("first instance should acquire lock")
	}
	lock.release()
}

func TestTryAcquireLock_SecondInstance(t *testing.T) {
	dir := t.TempDir()

	lock1 := tryAcquireLock(dir)
	if lock1 == nil {
		t.Fatal("first instance should acquire lock")
	}
	defer lock1.release()

	lock2 := tryAcquireLock(dir)
	if lock2 != nil {
		lock2.release()
		t.Fatal("second instance should NOT acquire lock")
	}
}

func TestTryAcquireLock_ReleaseAndReacquire(t *testing.T) {
	dir := t.TempDir()

	lock1 := tryAcquireLock(dir)
	if lock1 == nil {
		t.Fatal("first instance should acquire lock")
	}
	lock1.release()

	// After release, a new instance should be able to acquire
	lock2 := tryAcquireLock(dir)
	if lock2 == nil {
		t.Fatal("should acquire lock after release")
	}
	lock2.release()
}

func TestTryAcquireLock_WritesPID(t *testing.T) {
	dir := t.TempDir()

	lock := tryAcquireLock(dir)
	if lock == nil {
		t.Fatal("should acquire lock")
	}
	defer lock.release()

	pid := readLockPID(lock.file)
	if pid != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), pid)
	}
}

func TestLockHeldBy(t *testing.T) {
	dir := t.TempDir()

	// No lock held
	pid, err := LockHeldBy(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pid != 0 {
		t.Errorf("expected 0 (no lock), got %d", pid)
	}

	// Acquire lock
	lock := tryAcquireLock(dir)
	if lock == nil {
		t.Fatal("should acquire lock")
	}
	defer lock.release()

	pid, err = LockHeldBy(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), pid)
	}
}

func TestTryAcquireLock_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deep")
	lock := tryAcquireLock(dir)
	if lock == nil {
		t.Fatal("should acquire lock and create directories")
	}

	// Lock file is at <dir>/.ggcode/knight.lock
	lockPath := filepath.Join(dir, ".ggcode", "knight.lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file should exist at %s: %v", lockPath, err)
	}
	lock.release()
}

func TestFormatLockMessage(t *testing.T) {
	msg := FormatLockMessage(12345)
	if msg == "" {
		t.Error("message should not be empty")
	}
	msg0 := FormatLockMessage(0)
	if msg0 == "" {
		t.Error("message with pid=0 should not be empty")
	}
}
