package cron

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestNoDoubleFireOnConcurrentUpdate verifies that a stale timer callback
// (one that fired before Update bumped the generation) does NOT reschedule,
// preventing an orphaned duplicate timer. Before the generation-counter fix,
// the stale callback would call scheduleJobLocked, overwriting the timer
// created by Update and leaving Update's timer as an orphan that also fires.
func TestNoDoubleFireOnConcurrentUpdate(t *testing.T) {
	var fireCount int64
	// Slow enqueue to widen the race window (simulates TUI processing).
	var enqueueStart atomic.Int64
	enqueue := func(prompt string, queueIfBusy bool) {
		enqueueStart.Add(1)
		atomic.AddInt64(&fireCount, 1)
		time.Sleep(100 * time.Millisecond) // hold the race window open
	}

	s := NewScheduler(enqueue, "")

	// Create a job. Initial NextFire is the next 5-minute slot.
	// #1308: IDs carry a random suffix (cross-process uniqueness) - capture
	// the real ID instead of hardcoding "cron-1".
	created, err := s.Create("*/5 * * * *", "test prompt", true, false)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	jobID := created.ID

	// Override NextFire to fire immediately, then reschedule.
	s.mu.Lock()
	for _, job := range s.jobs {
		job.NextFire = time.Now()
		s.scheduleJobLocked(job) // gen=1
	}
	s.mu.Unlock()

	// Wait for the timer to fire and enter the slow enqueue window.
	time.Sleep(20 * time.Millisecond)

	// While the callback is in the enqueue window, call Update with a
	// real cronExpr to reschedule far in the future (bumps generation).
	futureCron := "0 0 1 1 *" // Jan 1st — far in the future
	s.Update(jobID, &futureCron, nil, nil)

	// Wait for everything to settle.
	time.Sleep(300 * time.Millisecond)

	count := atomic.LoadInt64(&fireCount)
	// We expect exactly 1 fire: the initial stale callback. The stale
	// callback's reschedule is skipped (generation mismatch), and Update's
	// timer is set to Jan 1st so it won't fire during this test.
	if count != 1 {
		t.Errorf("expected exactly 1 fire, got %d (stale callback rescheduled?)", count)
	}
}

// TestGenerationPreventsOrphanedReschedule verifies that when a timer fires
// and the callback tries to reschedule, but another scheduleJobLocked has
// already been called (bumping the generation), the stale callback does not
// create an orphaned timer.
func TestGenerationPreventsOrphanedReschedule(t *testing.T) {
	var fireCount int64
	enqueue := func(prompt string, queueIfBusy bool) {
		atomic.AddInt64(&fireCount, 1)
		time.Sleep(50 * time.Millisecond)
	}

	s := NewScheduler(enqueue, "")
	created, err := s.Create("*/5 * * * *", "test", true, false)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	jobID := created.ID // #1308: IDs carry a random suffix - capture it

	// Schedule to fire immediately.
	s.mu.Lock()
	for _, job := range s.jobs {
		job.NextFire = time.Now()
		s.scheduleJobLocked(job) // gen=1
	}
	s.mu.Unlock()

	// Wait for the timer to fire and enter the slow enqueue.
	time.Sleep(15 * time.Millisecond)

	// Bump generation manually (simulates Update's scheduleJobLocked call).
	// This makes the running callback's generation stale.
	s.mu.Lock()
	for _, job := range s.jobs {
		// Set NextFire far in the future so any accidental timer won't fire.
		job.NextFire = time.Now().Add(365 * 24 * time.Hour)
		s.generations[job.ID]++ // gen=2
		// Create the "real" replacement timer.
		s.scheduleJobLocked(job) // gen=3 (scheduleJobLocked increments again)
	}
	s.mu.Unlock()

	// Wait for the stale callback to complete.
	time.Sleep(200 * time.Millisecond)

	// The stale callback (gen=1) should have:
	// 1. Enqueued (fire=1) — it entered the enqueue before gen was bumped
	// 2. Skipped reschedule — gen mismatch detected after enqueue
	// No additional fires should occur because the replacement timer is 1 year out.
	count := atomic.LoadInt64(&fireCount)
	if count != 1 {
		t.Errorf("expected 1 fire, got %d", count)
	}

	// Verify the job's timer in s.timers is the replacement (far future).
	s.mu.Lock()
	timer := s.timers[jobID]
	s.mu.Unlock()
	if timer == nil {
		t.Error("expected replacement timer to exist")
	}
}
