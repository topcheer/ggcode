package cron

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestNoDoubleFireOnConcurrentUpdate(t *testing.T) {
	var fireCount int64
	var enqueueStart atomic.Int64
	enqueue := func(prompt string, queueIfBusy bool) {
		enqueueStart.Add(1)
		atomic.AddInt64(&fireCount, 1)
		time.Sleep(100 * time.Millisecond)
	}

	s := NewScheduler(enqueue, "")

	created, err := s.Create("*/5 * * * *", "test prompt", true, false)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	jobID := created.ID

	s.mu.Lock()
	for _, job := range s.jobs {
		job.NextFire = time.Now()
		s.scheduleJobLocked(job)
	}
	s.mu.Unlock()

	time.Sleep(20 * time.Millisecond)

	futureCron := "0 0 1 1 *"
	s.Update(jobID, &futureCron, nil, nil)

	time.Sleep(300 * time.Millisecond)

	count := atomic.LoadInt64(&fireCount)
	if count != 1 {
		t.Errorf("expected exactly 1 fire, got %d (stale callback rescheduled?)", count)
	}
}

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
	jobID := created.ID

	s.mu.Lock()
	for _, job := range s.jobs {
		job.NextFire = time.Now()
		s.scheduleJobLocked(job)
	}
	s.mu.Unlock()

	time.Sleep(15 * time.Millisecond)

	s.mu.Lock()
	for _, job := range s.jobs {
		job.NextFire = time.Now().Add(365 * 24 * time.Hour)
		s.generations[job.ID]++
		s.scheduleJobLocked(job)
	}
	s.mu.Unlock()

	time.Sleep(200 * time.Millisecond)

	count := atomic.LoadInt64(&fireCount)
	if count != 1 {
		t.Errorf("expected 1 fire, got %d", count)
	}

	s.mu.Lock()
	timer := s.timers[jobID]
	s.mu.Unlock()
	if timer == nil {
		t.Error("expected replacement timer to exist")
	}
}
