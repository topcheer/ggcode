package cron

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestDebounceBranchAdvancesNextFire reproduces issue #519: when the timer
// callback hits the debounce branch (double-trigger race: Update created a
// same-instant timer during the enqueue window), it used to reschedule
// WITHOUT advancing job.NextFire. Since the just-fired slot is in the past,
// scheduleJobLocked clamped the delay to 0 → AfterFunc(0) fired immediately
// → debounce hit again → busy spin for the whole 5s window, after which the
// dedup was defeated and a duplicate prompt was enqueued anyway.
//
// The test simulates the race by invoking the extracted callback body
// (fireJob) directly, twice, as if the stale timer fired on top of a fresh
// enqueue.
func TestDebounceBranchAdvancesNextFire(t *testing.T) {
	var enqueued atomic.Int32
	s := NewScheduler(func(prompt string, _ bool) { enqueued.Add(1) }, "")
	t.Cleanup(s.Shutdown)

	job := &Job{
		ID:        "debounce-1",
		CronExpr:  "0 * * * *", // hourly
		Prompt:    "p",
		Recurring: true,
		NextFire:  time.Now().Add(-2 * time.Second), // slot just consumed by the racing fire
	}
	s.mu.Lock()
	s.jobs[job.ID] = job
	s.generations[job.ID] = 1
	// Pretend the first fire of the race just enqueued (inside the 5s window).
	s.lastEnqueue[job.ID] = time.Now()
	s.mu.Unlock()

	// First duplicate fire hits the debounce branch.
	s.fireJob(job, 1)

	if got := enqueued.Load(); got != 0 {
		t.Fatalf("debounced fire must not enqueue a duplicate prompt; enqueued %d", got)
	}
	if delay := time.Until(job.NextFire); delay <= 0 {
		t.Fatalf("debounce branch must advance NextFire to a future slot (a stale NextFire means AfterFunc(0) busy-spin); delay=%v", delay)
	}

	// Second trigger — the old code's immediate 0-delay re-entry. The first
	// debounce reschedule bumped the generation, so the new timer's callback
	// carries the new generation.
	s.mu.Lock()
	gen2 := s.generations[job.ID]
	s.lastEnqueue[job.ID] = time.Now() // still inside the 5s dedup window
	s.mu.Unlock()
	s.fireJob(job, gen2)

	if got := enqueued.Load(); got != 0 {
		t.Fatalf("second debounced fire must not enqueue (dedup must hold for the whole window); enqueued %d", got)
	}
	if delay := time.Until(job.NextFire); delay <= 0 {
		t.Fatalf("NextFire must stay in the future after the second debounce; delay=%v", delay)
	}
}

// TestFireOutsideDebounceWindowEnqueues is the control for the fix above:
// a fire whose last enqueue is older than the 5s window is a legitimate fire
// and must still enqueue exactly once and reschedule into the future.
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
	s.lastEnqueue[job.ID] = time.Now().Add(-6 * time.Second) // window expired
	s.mu.Unlock()

	s.fireJob(job, 1)

	if got := enqueued.Load(); got != 1 {
		t.Fatalf("legitimate fire must enqueue exactly once; enqueued %d", got)
	}
	if delay := time.Until(job.NextFire); delay <= 0 {
		t.Fatalf("recurring job must be rescheduled into a future slot; delay=%v", delay)
	}
}

// TestDebounceOneShotDoesNotReschedule: a debounced duplicate fire of a
// one-shot job must neither reschedule (which would resurrect the job
// forever) nor delete it (the original fire's post-enqueue path owns the
// deletion). It must simply drop the duplicate.
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
