package cron

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

func TestPatrolRecurringReschedulesAfterMissedFire(t *testing.T) {
	var mu sync.Mutex
	fired := 0
	s := NewScheduler(func(prompt string, queueIfBusy bool) {
		mu.Lock()
		fired++
		mu.Unlock()
	}, "")
	defer s.Shutdown()

	job, err := s.Create("*/5 * * * *", "hello", true, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	s.mu.Lock()
	j := s.jobs[job.ID]
	j.NextFire = time.Now().Add(-time.Hour)
	if old, ok := s.timers[job.ID]; ok {
		old.Stop()
		delete(s.timers, job.ID)
	}
	s.timers[job.ID] = time.AfterFunc(time.Hour, func() {})
	s.mu.Unlock()

	s.patrolCheck()

	got, ok := s.Get(job.ID)
	if !ok {
		t.Fatal("job should still exist after patrol")
	}
	if !got.NextFire.After(time.Now()) {
		t.Fatalf("NextFire should be in the future after patrol, got %v", got.NextFire)
	}
	mu.Lock()
	n := fired
	mu.Unlock()
	if n != 0 {
		t.Fatalf("patrol must not replay missed historical fires, but fired %d times", n)
	}
	s.mu.Lock()
	_, hasTimer := s.timers[job.ID]
	s.mu.Unlock()
	if !hasTimer {
		t.Fatal("job should have an active timer after patrol reschedule")
	}
}

func TestPatrolOneShotFiresImmediatelyWhenOverdue(t *testing.T) {
	var mu sync.Mutex
	fired := 0
	done := make(chan struct{})
	s := NewScheduler(func(prompt string, queueIfBusy bool) {
		mu.Lock()
		fired++
		mu.Unlock()
		close(done)
	}, "")
	defer s.Shutdown()

	job, err := s.Create("*/5 * * * *", "once", false, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	s.mu.Lock()
	s.jobs[job.ID].NextFire = time.Now().Add(-time.Hour)
	if old, ok := s.timers[job.ID]; ok {
		old.Stop()
		delete(s.timers, job.ID)
	}
	s.timers[job.ID] = time.AfterFunc(time.Hour, func() {})
	s.mu.Unlock()

	s.patrolCheck()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("one-shot job did not fire after patrol")
	}
	waitFor(t, 2*time.Second, func() bool {
		_, ok := s.Get(job.ID)
		return !ok
	})
	mu.Lock()
	defer mu.Unlock()
	if fired != 1 {
		t.Fatalf("one-shot should fire exactly once, fired %d", fired)
	}
}

func TestPatrolSkipsPausedAndFreshJobs(t *testing.T) {
	s := NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()

	active, err := s.Create("*/5 * * * *", "active", true, false)
	if err != nil {
		t.Fatalf("Create active: %v", err)
	}
	paused, err := s.Create("*/5 * * * *", "paused", true, false)
	if err != nil {
		t.Fatalf("Create paused: %v", err)
	}
	if err := s.Pause(paused.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}

	before, _ := s.Get(active.ID)
	s.patrolCheck()
	after, _ := s.Get(active.ID)

	if after.NextFire.Before(before.NextFire) {
		t.Fatalf("patrol must not regress a fresh job's NextFire: %v -> %v", before.NextFire, after.NextFire)
	}
	pj, _ := s.Get(paused.ID)
	if !pj.NextFire.IsZero() {
		t.Fatalf("paused job NextFire should remain zero, got %v", pj.NextFire)
	}
}

func TestPauseClearsNextFire(t *testing.T) {
	s := NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()

	job, err := s.Create("*/5 * * * *", "p", true, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if job.NextFire.IsZero() {
		t.Fatal("new job should have a NextFire")
	}

	if err := s.Pause(job.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	got, ok := s.Get(job.ID)
	if !ok || !got.Paused {
		t.Fatal("job should be paused")
	}
	if !got.NextFire.IsZero() {
		t.Fatalf("Pause must clear NextFire, got %v", got.NextFire)
	}

	s.mu.Lock()
	_, hasTimer := s.timers[job.ID]
	s.mu.Unlock()
	if hasTimer {
		t.Fatal("paused job must have no timer")
	}

	if err := s.Resume(job.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got, _ = s.Get(job.ID)
	if got.Paused || got.NextFire.IsZero() || !got.NextFire.After(time.Now()) {
		t.Fatalf("Resume must recompute a future NextFire, got %+v", got)
	}
}

func TestPatrolPersistenceWithPausedJob(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "cron.json")

	s := NewScheduler(func(string, bool) {}, store)
	job, err := s.Create("*/5 * * * *", "persist me", true, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Pause(job.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	s.Shutdown()

	if _, err := os.Stat(store); err != nil {
		t.Fatalf("store file should exist: %v", err)
	}

	s2 := NewScheduler(func(string, bool) {}, store)
	defer s2.Shutdown()
	s2.Load()
	got, ok := s2.Get(job.ID)
	if !ok {
		t.Fatal("paused recurring job should survive Load")
	}
	if !got.Paused {
		t.Fatal("loaded job should still be paused")
	}
	if !got.NextFire.IsZero() {
		t.Fatalf("loaded paused job should have zero NextFire (computed on Resume), got %v", got.NextFire)
	}
}
