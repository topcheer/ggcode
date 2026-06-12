package cron

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
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

// jobJSON is the serializable form of a Job (no timers, callbacks, etc).
type jobJSON struct {
	ID        string `json:"id"`
	CronExpr  string `json:"cron_expr"`
	Prompt    string `json:"prompt"`
	Recurring bool   `json:"recurring"`
	CreatedAt string `json:"created_at"`
}

// workspaceBucket groups jobs under a workspace key.
type workspaceBucket struct {
	Workspace string    `json:"workspace"`
	Jobs      []jobJSON `json:"jobs"`
}

// storeFile is the top-level structure persisted to disk.
// Keyed by SHA256 of the workspace directory path.
type storeFile map[string]workspaceBucket

// Scheduler manages cron-like prompt scheduling with optional persistence.
type Scheduler struct {
	mu        sync.Mutex
	jobs      map[string]*Job
	nextID    int
	enqueue   func(prompt string)
	timers    map[string]*time.Timer
	storePath string
	wsKey     string // SHA256 of current workspace dir
	wsDir     string // original workspace dir path
}

// workspaceKey returns the SHA256 hex of a directory path.
func workspaceKey(dir string) string {
	h := sha256.Sum256([]byte(dir))
	return fmt.Sprintf("%x", h)
}

// NewScheduler creates a scheduler with the given enqueue callback and
// optional persistence path. If storePath is empty, no persistence is used
// (useful for tests).
func NewScheduler(enqueue func(prompt string), storePath string) *Scheduler {
	if enqueue == nil {
		enqueue = func(string) {}
	}
	return &Scheduler{
		jobs:      make(map[string]*Job),
		enqueue:   enqueue,
		timers:    make(map[string]*time.Timer),
		storePath: storePath,
	}
}

// Load reads persisted recurring jobs for the given workspace and schedules them.
// Must be called after NewScheduler, before any Create/Delete calls.
// If storePath is empty or the file doesn't exist, Load is a no-op.
func (s *Scheduler) Load(workspaceDir string) {
	s.mu.Lock()
	s.wsDir = workspaceDir
	s.wsKey = workspaceKey(workspaceDir)
	s.mu.Unlock()

	if s.storePath == "" {
		return
	}

	data, err := os.ReadFile(s.storePath)
	if err != nil {
		// File doesn't exist yet — that's fine.
		return
	}

	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		// Corrupted file — log and skip.
		return
	}

	bucket, ok := sf[s.wsKey]
	if !ok {
		return
	}

	// Load jobs. Sort by CreatedAt for deterministic ID assignment.
	sort.Slice(bucket.Jobs, func(i, j int) bool {
		return bucket.Jobs[i].CreatedAt < bucket.Jobs[j].CreatedAt
	})

	for _, jj := range bucket.Jobs {
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
			ID:        jj.ID,
			CronExpr:  jj.CronExpr,
			Prompt:    jj.Prompt,
			Recurring: jj.Recurring,
			CreatedAt: createdAt,
			NextFire:  next,
		}
		s.jobs[job.ID] = job
		// Track max ID to avoid collisions with new jobs.
		// Parse numeric part from IDs like "cron-5".
		var n int
		fmt.Sscanf(job.ID, "cron-%d", &n)
		if n > s.nextID {
			s.nextID = n
		}
		s.mu.Unlock()

		s.scheduleJob(job)
	}
}

// save persists all recurring jobs for the current workspace to the store file.
// It preserves other workspaces' data unchanged.
func (s *Scheduler) save() error {
	if s.storePath == "" {
		return nil
	}

	// Read existing file (other workspaces' data).
	var sf storeFile
	data, err := os.ReadFile(s.storePath)
	if err == nil {
		if err := json.Unmarshal(data, &sf); err != nil {
			return fmt.Errorf("parse cron store %s: %w", s.storePath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read cron store %s: %w", s.storePath, err)
	}
	if sf == nil {
		sf = make(storeFile)
	}

	// Build current workspace's job list.
	s.mu.Lock()
	jobs := make([]jobJSON, 0)
	for _, j := range s.jobs {
		if !j.Recurring {
			continue
		}
		jobs = append(jobs, jobJSON{
			ID:        j.ID,
			CronExpr:  j.CronExpr,
			Prompt:    j.Prompt,
			Recurring: j.Recurring,
			CreatedAt: j.CreatedAt.Format(time.RFC3339),
		})
	}
	s.mu.Unlock()

	// Update or remove the workspace bucket.
	if len(jobs) == 0 {
		delete(sf, s.wsKey)
	} else {
		sf[s.wsKey] = workspaceBucket{
			Workspace: s.wsDir,
			Jobs:      jobs,
		}
	}

	// Write back.
	out, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cron store: %w", err)
	}
	dir := filepath.Dir(s.storePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create cron store dir %s: %w", dir, err)
	}
	if err := os.WriteFile(s.storePath, out, 0644); err != nil {
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
	if err := s.save(); err != nil {
		s.mu.Lock()
		if timer, ok := s.timers[id]; ok {
			timer.Stop()
			delete(s.timers, id)
		}
		delete(s.jobs, id)
		s.mu.Unlock()
		return Job{}, err
	}

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
		return true, err
	}
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
				if err := s.save(); err != nil {
					log.Printf("[cron] failed to persist removal of broken job %s: %v", job.ID, err)
				}
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
				if err := s.save(); err != nil {
					log.Printf("[cron] failed to persist removal of broken job %s: %v", job.ID, err)
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
