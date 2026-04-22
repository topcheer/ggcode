package cron

import (
	"fmt"
	"sync"
	"time"
)

// Job represents a scheduled prompt job.
type Job struct {
	ID        string
	CronExpr  string
	Prompt    string
	Recurring bool
	CreatedAt time.Time
	NextFire  time.Time
}

// Snapshot returns a copy of the job safe for external use.
func (j *Job) Snapshot() Job {
	return *j
}

// Scheduler manages in-memory cron-like prompt scheduling.
type Scheduler struct {
	mu      sync.Mutex
	jobs    map[string]*Job
	nextID  int
	enqueue func(prompt string)
	timers  map[string]*time.Timer
}

// NewScheduler creates a scheduler with the given enqueue callback.
// The enqueue callback is called when a job fires, typically injecting
// the prompt into the TUI's conversation.
func NewScheduler(enqueue func(prompt string)) *Scheduler {
	if enqueue == nil {
		enqueue = func(string) {}
	}
	return &Scheduler{
		jobs:    make(map[string]*Job),
		enqueue: enqueue,
		timers:  make(map[string]*time.Timer),
	}
}

// Create adds a new scheduled job and returns its snapshot.
// The cron expression is a standard 5-field format:
//
//	minute hour day-of-month month day-of-week
//
// Supports: *, */N, N, N-M, N,M,K, N-M/S
func (s *Scheduler) Create(cronExpr, prompt string, recurring bool) (Job, error) {
	now := time.Now()
	next, err := NextTime(cronExpr, now)
	if err != nil {
		return Job{}, err
	}

	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("cron-%d", s.nextID)
	job := &Job{
		ID:        id,
		CronExpr:  cronExpr,
		Prompt:    prompt,
		Recurring: recurring,
		CreatedAt: now,
		NextFire:  next,
	}
	s.jobs[id] = job
	s.mu.Unlock()

	s.scheduleJob(job)

	return job.Snapshot(), nil
}

// Delete removes a scheduled job by ID.
func (s *Scheduler) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.jobs[id]; !ok {
		return false
	}
	if timer, ok := s.timers[id]; ok {
		timer.Stop()
		delete(s.timers, id)
	}
	delete(s.jobs, id)
	return true
}

// List returns snapshots of all jobs.
func (s *Scheduler) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j.Snapshot())
	}
	return out
}

// Get retrieves a job by ID.
func (s *Scheduler) Get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	return j.Snapshot(), true
}

// SetEnqueue sets or replaces the enqueue callback. Use this when the
// scheduler is created before the TUI is available.
func (s *Scheduler) SetEnqueue(fn func(prompt string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fn != nil {
		s.enqueue = fn
	}
}

func (s *Scheduler) scheduleJob(job *Job) {
	delay := time.Until(job.NextFire)
	if delay < 0 {
		delay = 0
	}

	timer := time.AfterFunc(delay, func() {
		s.enqueue(job.Prompt)

		s.mu.Lock()
		if job.Recurring {
			next, err := NextTime(job.CronExpr, time.Now())
			if err != nil {
				// Can't compute next fire — remove the broken job.
				delete(s.jobs, job.ID)
				delete(s.timers, job.ID)
				s.mu.Unlock()
				return
			}
			job.NextFire = next
			// Register the next timer while still holding the lock.
			// Use scheduleJobLocked to avoid re-locking (deadlock).
			s.scheduleJobLocked(job)
		} else {
			delete(s.jobs, job.ID)
			delete(s.timers, job.ID)
		}
		s.mu.Unlock()
	})

	s.mu.Lock()
	s.timers[job.ID] = timer
	s.mu.Unlock()
}

// scheduleJobLocked registers a timer for the job's NextFire time.
// Must be called with s.mu held. Used for recursive rescheduling
// from inside the timer callback to avoid deadlock (Go's sync.Mutex
// is not reentrant).
func (s *Scheduler) scheduleJobLocked(job *Job) {
	delay := time.Until(job.NextFire)
	if delay < 0 {
		delay = 0
	}
	s.timers[job.ID] = time.AfterFunc(delay, func() {
		s.enqueue(job.Prompt)

		s.mu.Lock()
		if job.Recurring {
			next, err := NextTime(job.CronExpr, time.Now())
			if err != nil {
				delete(s.jobs, job.ID)
				delete(s.timers, job.ID)
				s.mu.Unlock()
				return
			}
			job.NextFire = next
			s.scheduleJobLocked(job)
		} else {
			delete(s.jobs, job.ID)
			delete(s.timers, job.ID)
		}
		s.mu.Unlock()
	})
}

// Shutdown stops all timers and clears all jobs. The scheduler cannot be
// reused after shutdown.
func (s *Scheduler) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, timer := range s.timers {
		timer.Stop()
		delete(s.timers, id)
	}
	for id := range s.jobs {
		delete(s.jobs, id)
	}
}
