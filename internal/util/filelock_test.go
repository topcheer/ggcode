package util

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #1834 case 2 probes: FileLock must be BOUNDED - a busy holder leads to
// a timeout error, never an indefinite hang.

func TestFileLockAcquireReleaseCycle(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := FileLock(lockPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	unlock()
	// Immediately re-acquirable after release.
	unlock2, err := FileLock(lockPath)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	unlock2()
}

func TestFileLockBusyHolderTimesOut(t *testing.T) {
	// Shrink the bound so the probe finishes fast (restored on exit).
	prev := fileLockTimeout
	fileLockTimeout = 300 * time.Millisecond
	t.Cleanup(func() { fileLockTimeout = prev })

	lockPath := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := FileLock(lockPath)
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	defer unlock()

	// A second acquisition attempt on a held lock (separate fd) must fail
	// with the timeout error within a bounded window - not hang. Shrink
	// the effective wait by asserting the error arrives well before the
	// 10s cap; on CI allow generous headroom.
	start := time.Now()
	_, err = FileLock(lockPath)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("second acquire on a held lock unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "lock busy") && !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected busy/timeout error, got: %v", err)
	}
	if elapsed > FileLockTimeout()+2*time.Second {
		t.Fatalf("acquisition took %s, exceeding the %s bound", elapsed, FileLockTimeout())
	}
	t.Logf("busy acquire failed as expected after %s: %v", elapsed, err)
}
