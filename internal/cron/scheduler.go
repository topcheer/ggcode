package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
	runtimedebug "runtime/debug"
)

// Job represents a scheduled prompt job.
type Job struct {
	ID          string
	CronExpr    string
	Prompt      string
	Recurring   bool
	QueueIfBusy bool // if true, queue the prompt when agent is busy; if false (default), skip
	CreatedAt   time.Time
	NextFire    time.Time
	Paused      bool // if true, the job is suspended (no timers, no firing)
}

// Snapshot returns a copy of the job safe for external use.
func (j *Job) Snapshot() Job {
	return *j
}

// jobJSON is the serializable form of a Job (no timers, callbacks, etc).
type jobJSON struct {
	ID          string `json:"id"`
	CronExpr    string `json:"cron_expr"`
	Prompt      string `json:"prompt"`
	Recurring   bool   `json:"recurring"`
	QueueIfBusy bool   `json:"queue_if_busy"`
	Paused      bool   `json:"paused,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// sessionStore is the per-session JSON file structure.
type sessionStore struct {
	Jobs []jobJSON `json:"jobs"`
}

// Scheduler manages cron-like prompt scheduling with optional persistence.
type Scheduler struct {
	mu        sync.Mutex
	jobs      map[string]*Job
	nextID    int
	enqueue   func(prompt string, queueIfBusy bool)
	timers    map[string]*time.Timer
	storePath string // path to this session's JSON file
}

// NewScheduler creates a scheduler with the given enqueue callback and
// optional persistence path. If storePath is empty, no persistence is used
// (useful for tests).
func NewScheduler(enqueue func(prompt string, queueIfBusy bool), storePath string) *Scheduler {
	if enqueue == nil {
		enqueue = func(string, bool) {}
	}
	return &Scheduler{
		jobs:      make(map[string]*Job),
		enqueue:   enqueue,
		timers:    make(map[string]*time.Timer),
		storePath: storePath,
	}
}

// Load reads persisted recurring jobs for this session and schedules them.
// Must be called after NewScheduler, before any Create/Delete calls.
// If storePath is empty or the file doesn't exist, Load is a no-op.
func (s *Scheduler) Load() {
	if s.storePath == "" {
		return
	}

	data, err := os.ReadFile(s.storePath)
	if err != nil {
		// File doesn't exist yet — that's fine.
		return
	}

	var ss sessionStore
	if err := json.Unmarshal(data, &ss); err != nil {
		// Corrupted file — log and skip.
		debug.Log("cron", "Load: failed to parse store file %s: %v", s.storePath, err)
		return
	}

	// Sort by CreatedAt for deterministic ID assignment.
	sort.Slice(ss.Jobs, func(i, j int) bool {
		return ss.Jobs[i].CreatedAt < ss.Jobs[j].CreatedAt
	})

	for _, jj := range ss.Jobs {
		if !jj.Recurring {
			continue // don't restore one-shot jobs
		}

		now := time.Now()
		next, err := NextTime(jj.CronExpr, now)
		if err != nil {
			continue // skip broken cron expressions
		}

		createdAt, _ := time.Parse(time.RFC3339, jj.CreatedAt)

		s.mu.Lock()
		s.nextID++
		job := &Job{
			ID:          jj.ID,
			CronExpr:    jj.CronExpr,
			Prompt:      jj.Prompt,
			Recurring:   jj.Recurring,
			QueueIfBusy: jj.QueueIfBusy,
			CreatedAt:   createdAt,
			NextFire:    next,
			Paused:      jj.Paused,
		}
		s.jobs[job.ID] = job
		// Track max ID to avoid collisions with new jobs.
		var n int
		fmt.Sscanf(job.ID, "cron-%d", &n)
		if n > s.nextID {
			s.nextID = n
		}
		s.mu.Unlock()

		if !job.Paused {
			s.scheduleJob(job)
		}
	}

	loadedCount := 0
	s.mu.Lock()
	loadedCount = len(s.jobs)
	s.mu.Unlock()
	if loadedCount > 0 {
		debug.Log("cron", "Load: restored %d recurring cron jobs from %s", loadedCount, s.storePath)
	}
}

// save persists all recurring jobs for this session to the store file.
// The mutex is held throughout to prevent concurrent writes from racing.
func (s *Scheduler) save() error {
	if s.storePath == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	jobs := make([]jobJSON, 0)
	for _, j := range s.jobs {
		if !j.Recurring {
			continue
		}
		jobs = append(jobs, jobJSON{
			ID:          j.ID,
			CronExpr:    j.CronExpr,
			Prompt:      j.Prompt,
			Recurring:   j.Recurring,
			QueueIfBusy: j.QueueIfBusy,
			Paused:      j.Paused,
			CreatedAt:   j.CreatedAt.Format(time.RFC3339),
		})
	}

	if len(jobs) == 0 {
		// Remove the file when no recurring jobs remain.
		os.Remove(s.storePath)
		return nil
	}

	ss := sessionStore{Jobs: jobs}
	out, err := json.MarshalIndent(ss, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cron store: %w", err)
	}
	dir := filepath.Dir(s.storePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create cron store dir %s: %w", dir, err)
	}
	if err := util.AtomicWriteFile(s.storePath, out, 0644); err != nil {
		return fmt.Errorf("write cron store %s: %w", s.storePath, err)
	}
	return nil
}

// Create adds a new scheduled job and returns its snapshot.
// The cron expression is a standard 5-field format:
//
//	minute hour day-of-month month day-of-week
//
// Supports: *, */N, N, N-M, N,M,K, N-M/S
func (s *Scheduler) Create(cronExpr, prompt string, recurring bool, queueIfBusy bool) (Job, error) {
	now := time.Now()
	next, err := NextTime(cronExpr, now)
	if err != nil {
		return Job{}, err
	}

	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("cron-%d", s.nextID)
	job := &Job{
		ID:          id,
		CronExpr:    cronExpr,
		Prompt:      prompt,
		Recurring:   recurring,
		QueueIfBusy: queueIfBusy,
		CreatedAt:   now,
		NextFire:    next,
	}
	s.jobs[id] = job
	s.mu.Unlock()

	s.scheduleJob(job)
	if err := s.save(); err != nil {
		s.mu.Lock()
		if timer, ok := s.timers[id]; ok {
			timer.Stop()
			delete(s.timers, id)
		}
		delete(s.jobs, id)
		s.mu.Unlock()
		debug.Log("cron", "Create: failed to persist job %s: %v", id, err)
		return Job{}, err
	}

	debug.Log("cron", "Create: added job %s (expr=%s recurring=%t)", id, cronExpr, recurring)
	return job.Snapshot(), nil
}

// Delete removes a scheduled job by ID.
func (s *Scheduler) Delete(id string) bool {
	deleted, err := s.DeleteWithError(id)
	return deleted && err == nil
}

// DeleteWithError removes a scheduled job by ID and reports persistence errors.
func (s *Scheduler) DeleteWithError(id string) (bool, error) {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return false, nil
	}
	timer, hadTimer := s.timers[id]
	if hadTimer {
		timer.Stop()
		delete(s.timers, id)
	}
	delete(s.jobs, id)
	s.mu.Unlock()

	if err := s.save(); err != nil {
		s.mu.Lock()
		s.jobs[id] = job
		if hadTimer {
			s.scheduleJobLocked(job)
		}
		s.mu.Unlock()
		debug.Log("cron", "Delete: failed to persist removal of job %s: %v", id, err)
		return true, err
	}

	debug.Log("cron", "Delete: removed job %s", id)
	return true, nil
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

// Update modifies an existing job's cron expression, prompt, and/or queue_if_busy.
// Only the provided (non-nil) fields are changed. The job's timer is rescheduled.
// If the new cron expression is invalid, the original job is left unchanged.
func (s *Scheduler) Update(id string, cronExpr *string, prompt *string, queueIfBusy *bool) (Job, error) {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return Job{}, fmt.Errorf("job %q not found", id)
	}

	// Validate new cron expression before mutating.
	if cronExpr != nil {
		if _, err := NextTime(*cronExpr, time.Now()); err != nil {
			s.mu.Unlock()
			return Job{}, fmt.Errorf("invalid cron expression %q: %w", *cronExpr, err)
		}
	}

	// Snapshot original values for rollback on save failure.
	origCron := job.CronExpr
	origPrompt := job.Prompt
	origQueue := job.QueueIfBusy

	if cronExpr != nil {
		job.CronExpr = *cronExpr
	}
	if prompt != nil {
		job.Prompt = *prompt
	}
	if queueIfBusy != nil {
		job.QueueIfBusy = *queueIfBusy
	}

	// Recompute NextFire if cron changed.
	if cronExpr != nil {
		next, err := NextTime(job.CronExpr, time.Now())
		if err != nil {
			job.CronExpr = origCron
			s.mu.Unlock()
			return Job{}, err
		}
		job.NextFire = next
	}
	newSnapshot := job.Snapshot()

	// Stop and reschedule the timer (unless paused).
	if timer, ok := s.timers[id]; ok {
		timer.Stop()
		delete(s.timers, id)
	}
	if !job.Paused {
		s.scheduleJobLocked(job)
	}
	s.mu.Unlock()

	if err := s.save(); err != nil {
		// Rollback on persistence failure.
		s.mu.Lock()
		job.CronExpr = origCron
		job.Prompt = origPrompt
		job.QueueIfBusy = origQueue
		if cronExpr != nil {
			next, _ := NextTime(origCron, time.Now())
			job.NextFire = next
		}
		if t, ok := s.timers[id]; ok {
			t.Stop()
			delete(s.timers, id)
		}
		if !job.Paused {
			s.scheduleJobLocked(job)
		}
		s.mu.Unlock()
		debug.Log("cron", "Update: failed to persist job %s: %v", id, err)
		return Job{}, err
	}

	debug.Log("cron", "Update: updated job %s", id)
	return newSnapshot, nil
}

// Pause suspends a job's timer without deleting it. The job remains in the
// scheduler and is persisted (for recurring jobs). Resume with Resume().
func (s *Scheduler) Pause(id string) error {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("job %q not found", id)
	}
	if job.Paused {
		s.mu.Unlock()
		return nil // already paused, no-op
	}
	job.Paused = true
	if timer, ok := s.timers[id]; ok {
		timer.Stop()
		delete(s.timers, id)
	}
	s.mu.Unlock()

	if err := s.save(); err != nil {
		// Rollback
		s.mu.Lock()
		job.Paused = false
		s.scheduleJobLocked(job)
		s.mu.Unlock()
		return err
	}
	debug.Log("cron", "Pause: paused job %s", id)
	return nil
}

// Resume reactivates a paused job by recomputing its NextFire and scheduling a new timer.
func (s *Scheduler) Resume(id string) error {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("job %q not found", id)
	}
	if !job.Paused {
		s.mu.Unlock()
		return nil // already active, no-op
	}
	job.Paused = false

	next, err := NextTime(job.CronExpr, time.Now())
	if err != nil {
		job.Paused = true // rollback
		s.mu.Unlock()
		return fmt.Errorf("invalid cron expression in job %s: %w", id, err)
	}
	job.NextFire = next
	s.scheduleJobLocked(job)
	s.mu.Unlock()

	if err := s.save(); err != nil {
		// Rollback
		s.mu.Lock()
		job.Paused = true
		if t, ok := s.timers[id]; ok {
			t.Stop()
			delete(s.timers, id)
		}
		s.mu.Unlock()
		return err
	}
	debug.Log("cron", "Resume: resumed job %s", id)
	return nil
}

// SetEnqueue sets or replaces the enqueue callback. Use this when the
// scheduler is created before the TUI is available.
func (s *Scheduler) SetEnqueue(fn func(prompt string, queueIfBusy bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fn != nil {
		s.enqueue = fn
	}
}

// scheduleJob registers a timer for the job's NextFire time.
// It acquires the lock and delegates to scheduleJobLocked to ensure
// the timer is created and stored atomically, preventing a race where
// a delay=0 timer fires before being stored in s.timers.
func (s *Scheduler) scheduleJob(job *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduleJobLocked(job)
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
		defer func() {
			if r := recover(); r != nil {
				debug.Log("cron", "panic in timer callback for job %s: %v\n%s", job.ID, r, runtimedebug.Stack())
			}
		}()

		// Read mutable fields under lock to avoid data race with Update().
		s.mu.Lock()
		prompt := job.Prompt
		queueIfBusy := job.QueueIfBusy
		s.mu.Unlock()

		s.enqueue(prompt, queueIfBusy)

		s.mu.Lock()
		defer s.mu.Unlock()
		// Check if job was deleted while we were enqueueing (TOCTOU fix).
		// Without this check, a deleted recurring job would be re-scheduled
		// here, creating an infinite loop of phantom firings.
		if _, exists := s.jobs[job.ID]; !exists {
			return
		}
		if job.Recurring {
			next, err := NextTime(job.CronExpr, time.Now())
			if err != nil {
				delete(s.jobs, job.ID)
				delete(s.timers, job.ID)
				s.mu.Unlock()
				if err := s.save(); err != nil {
					debug.Log("cron", "failed to persist removal of broken job %s: %v", job.ID, err)
				} else {
					debug.Log("cron", "removed broken cron job %s (invalid expression: %s)", job.ID, job.CronExpr)
				}
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

// SetSession binds this scheduler to a session store path, migrating
// from the old workspace-scoped store if needed, then loading. This is used
// when the session ID is not yet known at scheduler creation time (e.g., TUI
// new session or desktop lazy init).
//
// storePath is the per-session JSON file path.
// oldStorePath is the legacy cron-jobs.json path (empty to skip migration).
// workspaceDir is the working directory key for migration (empty to skip).
func (s *Scheduler) SetSession(storePath, oldStorePath, workspaceDir string) {
	if storePath == "" {
		return
	}

	s.mu.Lock()
	if s.storePath != "" {
		s.mu.Unlock()
		return // already bound
	}
	s.storePath = storePath
	s.mu.Unlock()

	// Migrate from old workspace-scoped store if present.
	MigrateWorkspaceJobs(oldStorePath, storePath, workspaceDir)

	s.Load()
}

// SwitchSession rebinds the scheduler to a new session. Unlike SetSession
// (which is one-time only), SwitchSession stops all existing timers, clears
// all current jobs, and loads jobs from the new session's store file.
//
// storePath is the per-session JSON file path.
// oldStorePath is the legacy cron-jobs.json path (empty to skip migration).
// workspaceDir is the working directory key for migration (empty to skip).
func (s *Scheduler) SwitchSession(storePath, oldStorePath, workspaceDir string) {
	if storePath == "" {
		return
	}

	// Stop all existing timers and clear all jobs from the old session.
	s.mu.Lock()
	for id, timer := range s.timers {
		timer.Stop()
		delete(s.timers, id)
	}
	for id := range s.jobs {
		delete(s.jobs, id)
	}
	s.nextID = 0
	s.storePath = storePath
	s.mu.Unlock()

	debug.Log("cron", "SwitchSession: cleared old jobs, rebinding to %s", storePath)

	// Migrate from old workspace-scoped store if present.
	MigrateWorkspaceJobs(oldStorePath, storePath, workspaceDir)

	s.Load()
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
