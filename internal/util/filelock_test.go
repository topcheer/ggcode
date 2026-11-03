package util

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileLockAcquireReleaseCycle(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := FileLock(lockPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	unlock()

	unlock2, err := FileLock(lockPath)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	unlock2()
}

func TestFileLockBusyHolderTimesOut(t *testing.T) {

	prev := fileLockTimeout
	fileLockTimeout = 300 * time.Millisecond
	t.Cleanup(func() { fileLockTimeout = prev })

	lockPath := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := FileLock(lockPath)
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	defer unlock()

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
