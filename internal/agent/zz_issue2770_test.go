//go:build !windows

package agent

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestIssue2770LockTrajFileBounded(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "traj.lock")

	holder, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("holder flock: %v", err)
	}
	defer syscall.Flock(int(holder.Fd()), syscall.LOCK_UN)

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

	unlock()
	unlock2, err := lockTrajFile(lockPath)
	if err != nil {
		t.Fatalf("re-acquire after unlock: %v", err)
	}
	unlock2()
}
