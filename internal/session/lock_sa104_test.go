//go:build !windows

package session

import (
	"os"
	"testing"
)

// --- sa-104 coverage net: session lock lifecycle (unix flock path). ---

func TestSa104_SessionLock_Lifecycle(t *testing.T) {
	dir := t.TempDir()

	// Nil lock is safe to query and release.
	var nilLock *SessionLock
	if nilLock.Acquired() {
		t.Fatal("nil lock must not be acquired")
	}
	if nilLock.HolderPID() != 0 {
		t.Fatal("nil lock holder PID must be 0")
	}
	nilLock.Release() // no panic

	l1, err := TryAcquireSessionLock(dir, "sess-sa104")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if !l1.Acquired() || l1.HolderPID() != 0 {
		t.Fatalf("first lock must be acquired with PID 0: acquired=%t pid=%d", l1.Acquired(), l1.HolderPID())
	}

	// Second acquire in another open-file description must fail and report
	// the holder PID written by l1.
	l2, err := TryAcquireSessionLock(dir, "sess-sa104")
	if err != nil {
		t.Fatalf("second acquire probe: %v", err)
	}
	if l2.Acquired() {
		t.Fatal("second acquire must be refused while l1 holds the lock")
	}
	if l2.HolderPID() != os.Getpid() {
		t.Fatalf("holder PID must be ours: got %d want %d", l2.HolderPID(), os.Getpid())
	}

	// Release removes the lock file and unlocks; a fresh acquire succeeds.
	l1.Release()
	if _, err := os.Stat(LockFilePath(dir, "sess-sa104")); !os.IsNotExist(err) {
		t.Fatal("Release must remove the lock file")
	}
	l3, err := TryAcquireSessionLock(dir, "sess-sa104")
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if !l3.Acquired() {
		t.Fatal("re-acquire must succeed after release")
	}
	l3.Release()

	// Releasing twice is a no-op.
	l3.Release()

	// Non-acquired lock Release must not remove anything it doesn't own.
	l4, err := TryAcquireSessionLock(dir, "sess-sa104")
	if err != nil || !l4.Acquired() {
		t.Fatalf("setup l4: %v", err)
	}
	l2.Release() // l2 was never acquired, so this must be a safe no-op
	l5, err := TryAcquireSessionLock(dir, "sess-sa104")
	if err == nil && l5.Acquired() {
		t.Fatal("l2.Release must not have disturbed l4's lock")
	}
	l4.Release()
}
