//go:build !windows

package agent

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestIssue2770LockTrajFileBounded pins #2770: the Unix lockTrajFile must
// honor the "Must never panic or block" post-run contract by bounding its
// acquisition (5s deadline, matching the Windows counterpart). Before the
// fix, LOCK_EX blocked indefinitely when another process held the flock -
// hanging the synchronous post-run defer block and the whole run teardown.
func TestIssue2770LockTrajFileBounded(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "traj.lock")

	// Hold the flock from a REAL separate file description (a second open on
	// the same path in this process is enough - flock is per open file
	// description, so this holder competes exactly like another process).
	holder, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("holder flock: %v", err)
	}
	defer syscall.Flock(int(holder.Fd()), syscall.LOCK_UN)

	// The acquisition attempt must give up within the deadline instead of
	// blocking forever. We assert it returns (with a deadline error) inside
	// a generous window (>5s deadline, <20s guard).
	done := make(chan error, 1)
	go func() {
		unlock, err := lockTrajFile(lockPath)
		if err == nil {
			unlock()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("lockTrajFile unexpectedly acquired the lock while a live holder held it")
		}
		if err != os.ErrDeadlineExceeded {
			t.Fatalf("expected os.ErrDeadlineExceeded, got %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("lockTrajFile blocked >20s under a live flock holder - unbounded acquisition violates the Must-never-block post-run contract (#2770)")
	}
}

// TestIssue2770LockTrajFileUncontendedFast pins that the bounded path does
// not regress the uncontended case: with no holder, acquisition succeeds
// immediately and the returned unlock func releases the lock.
func TestIssue2770LockTrajFileUncontendedFast(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "traj.lock")

	start := time.Now()
	unlock, err := lockTrajFile(lockPath)
	if err != nil {
		t.Fatalf("uncontended lockTrajFile: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("uncontended acquisition took %v - retry loop misconfigured", elapsed)
	}

	// unlock must free the lock for a second acquisition.
	unlock()
	unlock2, err := lockTrajFile(lockPath)
	if err != nil {
		t.Fatalf("re-acquire after unlock: %v", err)
	}
	unlock2()
}
