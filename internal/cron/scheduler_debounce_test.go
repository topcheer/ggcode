package cron

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestDebounceBranchAdvancesNextFire(t *testing.T) {
	var enqueued atomic.Int32
	s := NewScheduler(func(prompt string, _ bool) { enqueued.Add(1) }, "")
	t.Cleanup(s.Shutdown)

	job := &Job{
		ID:        "debounce-1",
		CronExpr:  "0 * * * *",
		Prompt:    "p",
		Recurring: true,
		NextFire:  time.Now().Add(-2 * time.Second),
	}
	s.mu.Lock()
	s.jobs[job.ID] = job
	s.generations[job.ID] = 1
	s.lastEnqueue[job.ID] = time.Now()
	s.mu.Unlock()

	s.fireJob(job, 1)

	if got := enqueued.Load(); got != 0 {
		t.Fatalf("debounced fire must not enqueue a duplicate prompt; enqueued %d", got)
	}
	if delay := time.Until(job.NextFire); delay <= 0 {
		t.Fatalf("debounce branch must advance NextFire to a future slot (a stale NextFire means AfterFunc(0) busy-spin); delay=%v", delay)
	}

	s.mu.Lock()
	gen2 := s.generations[job.ID]
	s.lastEnqueue[job.ID] = time.Now()
	s.mu.Unlock()
	s.fireJob(job, gen2)

	if got := enqueued.Load(); got != 0 {
		t.Fatalf("second debounced fire must not enqueue (dedup must hold for the whole window); enqueued %d", got)
	}
	if delay := time.Until(job.NextFire); delay <= 0 {
		t.Fatalf("NextFire must stay in the future after the second debounce; delay=%v", delay)
	}
}

func TestFireOutsideDebounceWindowEnqueues(t *testing.T) {
	var enqueued atomic.Int32
	s := NewScheduler(func(string, bool) { enqueued.Add(1) }, "")
	t.Cleanup(s.Shutdown)

	job := &Job{
		ID:        "debounce-2",
		CronExpr:  "0 * * * *",
		Prompt:    "p",
		Recurring: true,
		NextFire:  time.Now().Add(-2 * time.Second),
	}
	s.mu.Lock()
	s.jobs[job.ID] = job
	s.generations[job.ID] = 1
	s.lastEnqueue[job.ID] = time.Now().Add(-6 * time.Second)
	s.mu.Unlock()

	s.fireJob(job, 1)

	if got := enqueued.Load(); got != 1 {
		t.Fatalf("legitimate fire must enqueue exactly once; enqueued %d", got)
	}
	if delay := time.Until(job.NextFire); delay <= 0 {
		t.Fatalf("recurring job must be rescheduled into a future slot; delay=%v", delay)
	}
}

func TestDebounceOneShotDoesNotReschedule(t *testing.T) {
	var enqueued atomic.Int32
	s := NewScheduler(func(string, bool) { enqueued.Add(1) }, "")
	t.Cleanup(s.Shutdown)

	job := &Job{
		ID:        "debounce-3",
		CronExpr:  "0 * * * *",
		Prompt:    "p",
		Recurring: false,
		NextFire:  time.Now().Add(-2 * time.Second),
	}
	s.mu.Lock()
	s.jobs[job.ID] = job
	s.generations[job.ID] = 1
	s.lastEnqueue[job.ID] = time.Now()
	s.mu.Unlock()

	s.fireJob(job, 1)

	if got := enqueued.Load(); got != 0 {
		t.Fatalf("debounced one-shot fire must not enqueue; enqueued %d", got)
	}
	s.mu.Lock()
	_, hasTimer := s.timers[job.ID]
	_, hasJob := s.jobs[job.ID]
	s.mu.Unlock()
	if hasTimer {
		t.Error("debounced one-shot fire must not schedule a new timer (would resurrect the job)")
	}
	if !hasJob {
		t.Error("debounced one-shot fire must not delete the job; the original fire's path handles deletion")
	}
}
