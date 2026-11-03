package cron

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestIssue673UpdateRollbackSkipsExpiredTimerRebuild(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "jobs.json")

	var fired atomic.Int32
	s := NewScheduler(func(string, bool) { fired.Add(1) }, storePath)
	defer s.Shutdown()

	job, err := s.Create("0 0 1 1 *", "prompt", true, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	s.mu.Lock()
	stored := s.jobs[job.ID]
	stored.CronExpr = "definitely not a cron expression"
	stored.NextFire = time.Now().Add(-time.Hour)
	s.mu.Unlock()

	if err := os.Remove(storePath); err != nil {
		t.Fatalf("remove store for dir-swap: %v", err)
	}
	if err := os.Mkdir(storePath, 0o755); err != nil {
		t.Fatalf("mkdir in place of store: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(storePath) })

	newExpr := "0 6 1 1 *"
	if _, err := s.Update(job.ID, &newExpr, nil, nil); err == nil {
		t.Fatalf("Update should fail when persistence fails")
	}

	got, ok := s.Get(job.ID)
	if !ok {
		t.Fatalf("job disappeared after failed Update")
	}
	if got.NextFire.IsZero() || !got.NextFire.Before(time.Now()) {
		t.Fatalf("defensive branch should keep prior (elapsed) NextFire, got %v", got.NextFire)
	}
	s.mu.Lock()
	_, hasTimer := s.timers[job.ID]
	s.mu.Unlock()
	if hasTimer {
		t.Fatalf("rollback rebuilt a timer for an elapsed NextFire — AfterFunc(0) spurious fire (#673)")
	}
	time.Sleep(150 * time.Millisecond)
	if n := fired.Load(); n != 0 {
		t.Fatalf("spurious fire after rollback: %d", n)
	}
}
