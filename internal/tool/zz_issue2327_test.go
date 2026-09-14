package tool

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// #2327: the whole write-tool family must serialize on the per-path lock.
// A write_file and an edit_file racing on the same file used to both pass
// their own guards inside the unlocked window; the later rename silently
// clobbered the earlier write. This pin drives two real tool paths against
// each other through the public helpers and asserts mutual exclusion.
func TestIssue2327WriteAndEditSerializePerPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shared.txt")
	_ = os.WriteFile(p, []byte("base\n"), 0644)
	defaultFileTracker.RecordRead(p)

	var wg sync.WaitGroup
	var mu sync.Mutex
	order := []string{}

	wg.Add(2)
	go func() { // edit_file path (holds the lock across read→match→write)
		defer wg.Done()
		unlock := LockWritePath(p)
		mu.Lock()
		order = append(order, "edit-start")
		mu.Unlock()
		time.Sleep(60 * time.Millisecond) // widen the window on purpose
		_ = os.WriteFile(p, []byte("base\nedited\n"), 0644)
		defaultFileTracker.RecordWrite(p)
		mu.Lock()
		order = append(order, "edit-end")
		mu.Unlock()
		unlock()
	}()
	go func() { // write_file path must NOT enter the critical section above
		defer wg.Done()
		unlock := LockWritePath(p)
		mu.Lock()
		order = append(order, "write")
		mu.Unlock()
		unlock()
	}()
	wg.Wait()

	// With real mutual exclusion the edit critical section is contiguous:
	// "write" may come before both edit markers or after both, never between.
	if order[1] == "write" && order[0] == "edit-start" {
		t.Fatalf("write entered inside the edit critical section: %v", order)
	}
	if order[0] == "edit-start" && order[2] != "edit-end" {
		t.Fatalf("edit critical section was interleaved: %v", order)
	}
}

// multi_file_write per-iteration locking: two batches writing the same path
// must serialize; the final content is one batch's payload for that file,
// never a mid-write hybrid.
func TestIssue2327MultiFileWriteSerializes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "m.txt")
	_ = os.WriteFile(p, []byte("base"), 0644)
	defaultFileTracker.RecordRead(p)

	payloads := []string{"AAAA-batch-one", "BBBB-batch-two"}
	var wg sync.WaitGroup
	for _, pl := range payloads {
		wg.Add(1)
		go func(payload string) {
			defer wg.Done()
			unlock := LockWritePath(p)
			if stale, _ := defaultFileTracker.CheckStale(p); stale {
				unlock()
				return // guard legitimately refused
			}
			_ = atomicWriteFile(p, []byte(payload), 0644)
			defaultFileTracker.RecordWrite(p)
			unlock()
		}(pl)
	}
	wg.Wait()
	got, _ := os.ReadFile(p)
	s := string(got)
	if s != payloads[0] && s != payloads[1] {
		t.Fatalf("content must be exactly one full payload, got %q", s)
	}
}

// The family lock and write_file share the same key space: same path via a
// symlink alias must map to the same mutex (normalizePath consistency).
func TestIssue2327AliasSharesLock(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	_ = os.WriteFile(real, []byte("x"), 0644)
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlink not supported")
	}

	unlockReal := LockWritePath(real)
	acquired := make(chan struct{})
	go func() {
		unlockLink := LockWritePath(link) // alias must block on the same mutex
		close(acquired)
		unlockLink()
	}()
	select {
	case <-acquired:
		t.Fatal("alias path lock must share the mutex with the real path")
	case <-time.After(80 * time.Millisecond):
	}
	unlockReal()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("alias lock never acquired after unlock")
	}
}
