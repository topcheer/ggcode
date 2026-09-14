package tool

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// #2318: the CheckStale→write sequence must be serialized per path. Two
// concurrent write_file calls to the same file used to both pass their stale
// check inside the unlocked window, and the later rename silently clobbered
// the earlier write. This pins the per-path mutual exclusion directly.
func TestIssue2318WritePathLockIsPerPathMutex(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")

	unlock1 := LockWritePath(p)
	locked := make(chan struct{})
	acquired := make(chan struct{})
	go func() {
		close(locked)
		unlock2 := LockWritePath(p) // must block until unlock1
		close(acquired)
		unlock2()
	}()
	<-locked
	select {
	case <-acquired:
		t.Fatal("second LockWritePath on the same path must block while held")
	case <-time.After(80 * time.Millisecond):
	}
	unlock1()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second LockWritePath never acquired after unlock")
	}
}

// Different paths must not serialize against each other.
func TestIssue2318DifferentPathsIndependent(t *testing.T) {
	unlockA := LockWritePath("/tmp/zz-2318-a.txt")
	acquired := make(chan struct{})
	go func() {
		unlockB := LockWritePath("/tmp/zz-2318-b.txt")
		close(acquired)
		unlockB()
	}()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("different-path lock must not block on a held lock of another path")
	}
	unlockA()
}

// End-to-end shape: two racing writes both pass CheckStale only because the
// window is closed by the lock - the second write's stale check now sees the
// first write's recorded mtime and refuses... unless it legitimately re-read.
// Here we verify the critical section actually serializes writes to disk:
// with the lock, the final content is always one of the two FULL payloads,
// never an interleaved/lost hybrid of the temp-file dance.
func TestIssue2318ConcurrentWritesNeverLoseWholeFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shared.txt")
	_ = os.WriteFile(p, []byte("base"), 0644)
	defaultFileTracker.RecordRead(p)

	var wg sync.WaitGroup
	payloads := []string{"AAAA-first-write", "BBBB-second-write"}
	for _, pl := range payloads {
		wg.Add(1)
		go func(payload string) {
			defer wg.Done()
			unlock := LockWritePath(p)
			defer unlock()
			if stale, _ := defaultFileTracker.CheckStale(p); stale {
				return // legitimately refused by the guard
			}
			_ = atomicWriteFile(p, []byte(payload), 0644)
			defaultFileTracker.RecordWrite(p)
		}(pl)
	}
	wg.Wait()
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if s != payloads[0] && s != payloads[1] {
		t.Fatalf("file content must be exactly one full payload, got %q", s)
	}
}
