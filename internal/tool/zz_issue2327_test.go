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
	// #2327-flake hardening: a start barrier makes both goroutines reach the
	// lock call together. Without it a slow CI runner could schedule the
	// edit goroutine fully through its critical section before the write
	// goroutine was even spawned - the order assertions then exercise the
	// lock from only one side and the "never between" invariant is not
	// actually under test.
	start := make(chan struct{})

	wg.Add(2)
	go func() { // edit_file path (holds the lock across read→match→write)
		defer wg.Done()
		<-start
		unlock := LockWritePath(p)
		mu.Lock()
		order = append(order, "edit-start")
		mu.Unlock()
		time.Sleep(120 * time.Millisecond) // widen the window on purpose
		_ = os.WriteFile(p, []byte("base\nedited\n"), 0644)
		defaultFileTracker.RecordWrite(p)
		mu.Lock()
		order = append(order, "edit-end")
		mu.Unlock()
		unlock()
	}()
	go func() { // write_file path must NOT enter the critical section above
		defer wg.Done()
		<-start
		unlock := LockWritePath(p)
		mu.Lock()
		order = append(order, "write")
		mu.Unlock()
		unlock()
	}()
	close(start)
	wg.Wait()

	// #2327-flake: the REAL invariant is "write's position is never strictly
	// between edit-start and edit-end". The old shape (order[2]!="edit-end"
	// when order[0]=="edit-start") also fired on the perfectly legal serial
	// order [edit-start, edit-end, write] - edit fully completed, write
	// acquired afterwards. That false positive is the CI flake.
	startIdx, writeIdx, endIdx := -1, -1, -1
	for i, ev := range order {
		switch ev {
		case "edit-start":
			startIdx = i
		case "write":
			writeIdx = i
		case "edit-end":
			endIdx = i
		}
	}
	if startIdx >= 0 && endIdx >= 0 && writeIdx > startIdx && writeIdx < endIdx {
		t.Fatalf("write entered inside the edit critical section: %v", order)
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
	case <-time.After(300 * time.Millisecond):
	}
	unlockReal()
	select {
	case <-acquired:
		// #2327-flake hardening: the original 2s timeout was tight enough
		// that a CI runner under load could starve the waiter goroutine past
		// it right after unlockReal - a false positive. 5s keeps the test
		// fast on healthy runners while giving starved schedulers room.
	case <-time.After(5 * time.Second):
		t.Fatal("alias lock never acquired after unlock")
	}
}
