//go:build !windows

package im

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestIssue2809BindingsLockIsBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "im-bindings.json")

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
		_, acqErr = lockBindingsFile(path)
	}()
	select {
	case <-done:
		if acqErr == nil {
			t.Error("acquisition unexpectedly succeeded while lock held")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("lock acquisition hung >30s under a held lock - unbounded blocking recurrence (#2809)")
	}
}

func TestIssue2809BindingsLockUncontended(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "im-bindings.json")
	unlock, err := lockBindingsFile(path)
	if err != nil {
		t.Fatalf("uncontended acquisition failed: %v", err)
	}
	unlock()
	unlock2, err := lockBindingsFile(path)
	if err != nil {
		t.Fatalf("re-acquisition after release failed: %v", err)
	}
	unlock2()
}

func TestIssue2809NoBareBlockingFlock(t *testing.T) {
	for _, name := range []string{"bindings_lock_unix.go", "bindings_lock_windows.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		s := string(src)
		if strings.Contains(s, "syscall.Flock") || strings.Contains(s, "LockFileEx") {
			t.Errorf("%s: hand-rolled lock call still present - must delegate to util.FileLock (#2809 recurrence)", name)
		}
		if !strings.Contains(s, "util.FileLock") {
			t.Errorf("%s: does not delegate to util.FileLock", name)
		}
	}
}
