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

func TestRunMaintenanceDoesNotHoldStoreLock(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	target := NewSession("test", "ep", "m")
	target.Workspace = dir
	target.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "resume me"}}},
	}
	saveFullForTest(t, s, target)
	if err := s.updateIndex(target); err != nil {
		t.Fatalf("index target: %v", err)
	}

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

	startTail := func() {
		go func() {
			for {
				w, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
				if err != nil {
					return
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

	time.Sleep(150 * time.Millisecond)

	if !s.mu.TryLock() {
		startTail()
		<-done
		t.Fatal("runMaintenance holds s.mu while parked on a session file - a concurrent Load(resumeID) queues behind the whole-store scan (startup blank-screen regression)")
	}
	s.mu.Unlock()

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
