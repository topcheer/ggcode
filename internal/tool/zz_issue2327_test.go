package tool

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestIssue2327WriteAndEditSerializePerPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shared.txt")
	_ = os.WriteFile(p, []byte("base\n"), 0644)
	defaultFileTracker.RecordRead(p)

	var wg sync.WaitGroup
	var mu sync.Mutex
	order := []string{}

	start := make(chan struct{})

	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		unlock := LockWritePath(p)
		mu.Lock()
		order = append(order, "edit-start")
		mu.Unlock()
		time.Sleep(120 * time.Millisecond)
		_ = os.WriteFile(p, []byte("base\nedited\n"), 0644)
		defaultFileTracker.RecordWrite(p)
		mu.Lock()
		order = append(order, "edit-end")
		mu.Unlock()
		unlock()
	}()
	go func() {
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
				return
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
		unlockLink := LockWritePath(link)
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

	case <-time.After(5 * time.Second):
		t.Fatal("alias lock never acquired after unlock")
	}
}
