//go:build !windows

package session

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestRunMaintenanceDoesNotHoldStoreLock guards the startup-latency fix with a
// DETERMINISTIC block point (no timing races):
//
// A FIFO named like a session file is pre-seeded into the store index.
// repairIndex only loads files NOT in the index, so the FIFO's single reader
// is pruneInvalidIndexEntries, whose scan paths (old loadSessionFull / new
// HasUserInteractionOnDisk) open it with os.Open - and opening a FIFO
// read-end blocks until a writer appears. So runMaintenance provably sits
// mid-scan while we probe the lock.
//
// The pre-fix implementation took s.mu BEFORE the scan and released it only
// after the whole store was parsed - while maintenance sat parked on the
// FIFO it still held s.mu, so any Load(resumeID) at startup queued behind it
// (real-world profile: 48s blank TUI on a large store). If s.mu.TryLock
// fails while maintenance is parked on the FIFO, that exact regression is
// back. The fixed implementation holds no store-wide lock during the scan.
func TestRunMaintenanceDoesNotHoldStoreLock(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// A normal session that must remain loadable while maintenance is parked.
	target := NewSession("test", "ep", "m")
	target.Workspace = dir
	target.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "resume me"}}},
	}
	saveFullForTest(t, s, target)
	if err := s.updateIndex(target); err != nil {
		t.Fatalf("index target: %v", err)
	}

	// The block point: a FIFO that os.Open (read end) parks on until a
	// writer appears. Its index entry is pre-seeded so repairIndex (which
	// only loads files NOT in the index) skips it and prune is the single
	// reader - one unblock lets maintenance run to completion.
	fifoID := "20260101-000000-deadbeefdeadbeef"
	fifoPath := filepath.Join(dir, fifoID+".jsonl")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := s.loadIndex()
	if err != nil {
		t.Fatalf("loadIndex: %v", err)
	}
	idx = append(idx, indexEntry{ID: fifoID, Title: "fifo", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err := s.saveIndex(idx); err != nil {
		t.Fatalf("seed fifo index entry: %v", err)
	}
	// tailWriter pairs with EVERY blocked reader of the FIFO, delivering EOF
	// each time: the pre-fix prune path opens a session file more than once
	// (loadSessionFull parse + its internal migrateMessageIDs scan), so a
	// single unblock cannot let old-code maintenance finish here. When no
	// reader is parked, the writer open itself parks until the next reader
	// arrives - the chain never breaks and maintenance provably completes.
	// The goroutine leaks (parked in open after the last reader) once the
	// test ends; deliberate and harmless: it touches no memory, and TempDir
	// cleanup removing the FIFO cannot race it.
	startTail := func() {
		go func() {
			for {
				w, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
				if err != nil {
					return // FIFO gone
				}
				w.Close()
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		s.runMaintenance()
		close(done)
	}()

	// Let the maintenance goroutine reach the FIFO's open. The code path to
	// the prune scan is short; this sleep only needs to cover goroutine
	// scheduling, not scan duration.
	time.Sleep(150 * time.Millisecond)

	if !s.mu.TryLock() {
		startTail()
		<-done
		t.Fatal("runMaintenance holds s.mu while parked on a session file - a concurrent Load(resumeID) queues behind the whole-store scan (startup blank-screen regression)")
	}
	s.mu.Unlock()

	// Behavioral double-check: with the store lock free, Load must succeed
	// even while maintenance is still parked mid-scan.
	loaded := make(chan error, 1)
	go func() {
		_, err := s.Load(target.ID)
		loaded <- err
	}()
	select {
	case err := <-loaded:
		if err != nil {
			t.Fatalf("Load during parked maintenance: %v", err)
		}
	case <-time.After(5 * time.Second):
		startTail()
		<-done
		t.Fatal("Load blocked >5s while maintenance parked - store lock regression")
	}

	startTail()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("runMaintenance did not finish within 30s after unblock")
	}
}
