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

func TestIssue2809LockIsBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")

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

	case <-time.After(30 * time.Second):
		t.Fatal("lock acquisition hung >30s under a held lock - unbounded blocking recurrence (#2809)")
	}
}

func TestIssue2809LockUncontended(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	unlock, err := lockStoreFileCrossProc(path)
	if err != nil {
		t.Fatalf("uncontended acquisition failed: %v", err)
	}
	unlock()

	unlock2, err := lockStoreFileCrossProc(path)
	if err != nil {
		t.Fatalf("re-acquisition after release failed: %v", err)
	}
	unlock2()
}

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
