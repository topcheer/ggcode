package cron

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// #673 (#667 note): the Update-rollback defensive branch (NextTime(origCron)
// failing) restores the pre-update NextFire, which in that branch is almost
// certainly elapsed; rebuilding the timer then clamps the delay to 0
// (AfterFunc(0)) and the job fires spuriously. The rollback must skip the
// rebuild when the restored NextFire is not in the future.
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

	// Reachability: the defensive branch needs save() to fail AND
	// NextTime(origCron) to error; origCron is validated on the way in, so
	// inject the preconditions directly (in-package test): an origCron that
	// no longer parses plus an already-elapsed origNextFire.
	s.mu.Lock()
	stored := s.jobs[job.ID]
	stored.CronExpr = "definitely not a cron expression"
	stored.NextFire = time.Now().Add(-time.Hour)
	s.mu.Unlock()

	// Make persistence fail: replace the store with a same-name directory
	// (deterministic on all platforms; the previous os.Chmod(dir, 0o500)
	// injection was a no-op on Windows — found during windows/amd64
	// verification of #673).
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

	// The defensive rollback branch keeps the prior (elapsed) NextFire...
	got, ok := s.Get(job.ID)
	if !ok {
		t.Fatalf("job disappeared after failed Update")
	}
	if got.NextFire.IsZero() || !got.NextFire.Before(time.Now()) {
		t.Fatalf("defensive branch should keep prior (elapsed) NextFire, got %v", got.NextFire)
	}
	// ...and must NOT rebuild the timer: AfterFunc(0) would fire spuriously.
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
