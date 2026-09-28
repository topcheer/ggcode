//go:build !windows

package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// zz_issue2809_test.go guards against the unbounded blocking flock recurrence
// (#2809): lockStoreFileCrossProc used a bare LOCK_EX with no LOCK_NB/timeout
// while (on the im side) callers held s.mu - a suspended holder wedged the
// whole read path. The lock must now be bounded (util.FileLock).

// TestIssue2809LockIsBounded: with the lock already held by our own process
// (a second flock on the same fd family from a different descriptor),
// acquisition must fail within a bounded time instead of hanging forever.
func TestIssue2809LockIsBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")

	// Hold the lock ourselves via a raw descriptor (what a stuck peer looks
	// like to a new acquirer on unix flock semantics).
	held, err := os.OpenFile(path+".flock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("precondition: hold lock: %v", err)
	}
	defer syscall.Flock(int(held.Fd()), syscall.LOCK_UN)

	done := make(chan struct{})
	var acqErr error
	go func() {
		defer close(done)
		_, acqErr = lockStoreFileCrossProc(path)
	}()
	select {
	case <-done:
		if acqErr == nil {
			t.Error("acquisition unexpectedly succeeded while lock held (flock semantics?)")
		}
		// Expected: timeout error after bounded retry - the fail-open path.
	case <-time.After(30 * time.Second):
		t.Fatal("lock acquisition hung >30s under a held lock - unbounded blocking recurrence (#2809)")
	}
}

// TestIssue2809LockUncontended: the normal path still acquires and releases.
func TestIssue2809LockUncontended(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	unlock, err := lockStoreFileCrossProc(path)
	if err != nil {
		t.Fatalf("uncontended acquisition failed: %v", err)
	}
	unlock()
	// Re-acquire after release must succeed.
	unlock2, err := lockStoreFileCrossProc(path)
	if err != nil {
		t.Fatalf("re-acquisition after release failed: %v", err)
	}
	unlock2()
}

// TestIssue2809NoBareBlockingFlock pins the source-level invariant: no bare
// LOCK_EX (without LOCK_NB) remains in the store lock file.
func TestIssue2809NoBareBlockingFlock(t *testing.T) {
	src, err := os.ReadFile("store_lock_unix.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(src)
	if strings.Contains(s, "syscall.Flock") {
		t.Error("hand-rolled syscall.Flock still present - must delegate to util.FileLock (#2809 recurrence)")
	}
	if !strings.Contains(s, "util.FileLock") {
		t.Error("lock file does not delegate to util.FileLock")
	}
}
