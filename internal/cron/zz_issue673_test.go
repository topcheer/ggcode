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
	defer os.Chmod(dir, 0o755) // restore for TempDir cleanup
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

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	newExpr := "0 6 1 1 *"
	if _, err := s.Update(job.ID, &newExpr, nil, nil); err == nil {
		t.Fatalf("Update should fail when persistence fails")
	}
	_ = os.Chmod(dir, 0o755)

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
