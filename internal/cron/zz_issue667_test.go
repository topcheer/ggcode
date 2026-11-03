package cron

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssue667UpdatePausedKeepsZeroNextFire(t *testing.T) {
	s := NewScheduler(func(string, bool) {}, filepath.Join(t.TempDir(), "jobs.json"))
	defer s.Shutdown()

	job, err := s.Create("0 0 1 1 *", "prompt", true, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Pause(job.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	paused, ok := s.Get(job.ID)
	if !ok {
		t.Fatalf("job %s disappeared after Pause", job.ID)
	}
	if !paused.NextFire.IsZero() {
		t.Fatalf("precondition: Pause should zero NextFire (got %v)", paused.NextFire)
	}

	newExpr := "0 6 1 1 *"
	updated, err := s.Update(job.ID, &newExpr, nil, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.Paused {
		t.Fatalf("job should remain paused")
	}
	if !updated.NextFire.IsZero() {
		t.Fatalf("Update on paused job wrote non-zero NextFire %v; paused jobs must keep zero (#667)", updated.NextFire)
	}
	if updated.CronExpr != newExpr {
		t.Fatalf("cron expr not updated: %q", updated.CronExpr)
	}

	got, ok := s.Get(job.ID)
	if !ok || !got.NextFire.IsZero() || got.CronExpr != newExpr {
		t.Fatalf("Get after Update: %+v", got)
	}

	if err := s.Resume(job.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got, _ = s.Get(job.ID)
	if got.NextFire.IsZero() {
		t.Fatalf("Resume should recompute non-zero NextFire")
	}
}

func TestIssue667UpdateRollbackPausedInvariant(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "jobs.json")

	s := NewScheduler(func(string, bool) {}, storePath)
	defer s.Shutdown()

	job, err := s.Create("0 0 1 1 *", "prompt", true, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Pause(job.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}

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
	if got.CronExpr != "0 0 1 1 *" {
		t.Fatalf("rollback did not restore cron expr: %q", got.CronExpr)
	}
	if !got.Paused {
		t.Fatalf("rollback lost paused state")
	}
	if !got.NextFire.IsZero() {
		t.Fatalf("rollback wrote non-zero NextFire %v on paused job (#667)", got.NextFire)
	}
}

func TestIssue667UpdateActiveRecomputesNextFire(t *testing.T) {
	s := NewScheduler(func(string, bool) {}, filepath.Join(t.TempDir(), "jobs.json"))
	defer s.Shutdown()

	job, err := s.Create("0 0 1 1 *", "prompt", true, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	newExpr := "0 6 1 1 *"
	updated, err := s.Update(job.ID, &newExpr, nil, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.NextFire.IsZero() {
		t.Fatalf("active job Update should set NextFire")
	}
	if time.Until(updated.NextFire) < 0 {
		t.Fatalf("NextFire should be in the future: %v", updated.NextFire)
	}
}
