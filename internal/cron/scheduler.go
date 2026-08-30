package cron

import (
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
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
	mu          sync.Mutex
	jobs        map[string]*Job
	nextID      int
	enqueue     func(prompt string, queueIfBusy bool)
	timers      map[string]*time.Timer
	generations map[string]uint64    // job ID -> generation counter to detect stale timers
	lastEnqueue map[string]time.Time // job ID -> last enqueue timestamp (dedup guard)
	storePath   string               // path to this session's JSON file
	// knownIDs tracks every recurring job ID this process has ever seen
	// (loaded or created). save() uses it to distinguish "deleted here"
	// from "created by another process": on-disk entries with unknown IDs
	// are preserved instead of being clobbered by our full-file rewrite
	// (#1308).
	knownIDs map[string]bool

	// Wall-clock patrol (issue #311): time.AfterFunc timers use the
	// monotonic clock, which does not advance while the machine sleeps
	// (Go issue #24595). The patrol goroutine periodically checks the
	// wall clock and compensates for missed fires after wake-up.
	patrolInterval time.Duration // 0 → defaultPatrolInterval
	patrolStop     chan struct{}
}

const defaultPatrolInterval = 30 * time.Second

// patrolGrace is how far past NextFire the wall clock must be before the
// patrol considers a fire "missed". It avoids racing a timer callback that
// is about to run normally (the debounce guard in the callback also protects
// against a double fire).
const patrolGrace = 2 * time.Second

// NewScheduler creates a scheduler with the given enqueue callback and
// optional persistence path. If storePath is empty, no persistence is used
// (useful for tests).
func NewScheduler(enqueue func(prompt string, queueIfBusy bool), storePath string) *Scheduler {
	if enqueue == nil {
		enqueue = func(string, bool) {}
	}
	s := &Scheduler{
		nextID:      0,
		jobs:        make(map[string]*Job),
		knownIDs:    make(map[string]bool),
		enqueue:     enqueue,
		timers:      make(map[string]*time.Timer),
		generations: make(map[string]uint64),
		lastEnqueue: make(map[string]time.Time),
		storePath:   storePath,
	}
	s.startPatrol()
	return s
}

// startPatrol launches the background wall-clock patrol goroutine. It is
// idempotent and stopped by Shutdown.
func (s *Scheduler) startPatrol() {
	s.mu.Lock()
	if s.patrolStop != nil {
		s.mu.Unlock()
		return // already running
	}
	stop := make(chan struct{})
	s.patrolStop = stop
	interval := s.patrolInterval
	s.mu.Unlock()
	if interval <= 0 {
		interval = defaultPatrolInterval
	}

	go safego.Run("cron.scheduler.patrol", func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.patrolCheck()
			}
		}
	})
}

// patrolCheck scans all active jobs against the wall clock. If a job's
// NextFire is in the past by more than patrolGrace (i.e., the monotonic
// timer did not fire — typically because the machine slept through it):
//
//   - recurring jobs are rescheduled to the next future slot (missed slots
//     are skipped, not replayed);
//   - one-shot jobs that are already past due fire immediately, once.
func (s *Scheduler) patrolCheck() {
	now := time.Now()
	removedBroken := false

	s.mu.Lock()

	for id, job := range s.jobs {
		if job.Paused || job.NextFire.IsZero() {
			continue
		}
		if !now.After(job.NextFire.Add(patrolGrace)) {
			continue
		}

		// The fire was missed. Stop the stale monotonic timer and bump the
		// generation so any in-flight callback aborts. The enqueue debounce
		// in the timer callback guards against a double fire if the callback
		// already passed its generation check.
		if t, ok := s.timers[id]; ok {
			t.Stop()
			delete(s.timers, id)
		}
		s.generations[id]++

		if job.Recurring {
			// Recompute to the nearest future slot; missed historical slots
			// are skipped (no unbounded catch-up replay).
			next, err := NextTime(job.CronExpr, now)
			if err != nil {
				delete(s.jobs, id)
				removedBroken = true
				debug.Log("cron", "patrol: removed broken cron job %s (invalid expression: %s)", id, job.CronExpr)
				continue
			}
			debug.Log("cron", "patrol: missed fire detected for %s, rescheduled to %s", id, next.Format(time.RFC3339))
			job.NextFire = next
			s.scheduleJobLocked(job)
		} else {
			// One-shot past due after sleep: fire immediately, once.
			debug.Log("cron", "patrol: missed one-shot fire for %s, firing now", id)
			job.NextFire = now
			s.scheduleJobLocked(job)
		}
	}

	s.mu.Unlock()

	if removedBroken {
		if err := s.save(); err != nil {
			debug.Log("cron", "patrol: failed to persist removal of broken job: %v", err)
		}
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
		// Skip if this job was already loaded (prevents duplicate timers
		// when Load() is called multiple times for the same file).
		if _, exists := s.jobs[jj.ID]; exists {
			s.mu.Unlock()
			continue
		}
		s.nextID++
		job := &Job{
			ID:          jj.ID,
			CronExpr:    jj.CronExpr,
			Prompt:      jj.Prompt,
			Recurring:   jj.Recurring,
			QueueIfBusy: jj.QueueIfBusy,
			CreatedAt:   createdAt,
			Paused:      jj.Paused,
		}
		// Paused jobs load with a zero NextFire (consistent with Pause(),
		// which clears it so UIs don't show a stale "next fire" time).
		// Resume() recomputes it from the cron expression. (issue #311)
		if !job.Paused {
			job.NextFire = next
		}
		s.jobs[job.ID] = job
		s.knownIDs[job.ID] = true // #1308: seen here - don't merge it back on save
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

	// #1308: this file is shared across processes (TUI resume + desktop can
	// bind the same session); the session lock does NOT cover it. A plain
	// full-file rewrite from a stale in-memory snapshot silently erased jobs
	// another process had just created. Merge instead: preserve on-disk
	// recurring jobs whose IDs this process has never seen (ours, including
	// deleted ones, are knownIDs and stay authoritative).
	if data, err := os.ReadFile(s.storePath); err == nil {
		var onDisk sessionStore
		if json.Unmarshal(data, &onDisk) == nil {
			inMemory := make(map[string]bool, len(jobs))
			for _, j := range jobs {
				inMemory[j.ID] = true
			}
			for _, jj := range onDisk.Jobs {
				if jj.Recurring && !inMemory[jj.ID] && !s.knownIDs[jj.ID] {
					jobs = append(jobs, jj)
				}
			}
		}
	}

	if len(jobs) == 0 {
		// Remove the file when no recurring jobs remain.
		// #1308: surface (rather than ignore) a failed removal so the
		// operator can see the store file is stale.
		if err := os.Remove(s.storePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove cron store %s: %w", s.storePath, err)
		}
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

// cronIDSuffix returns 4 random bytes hex-encoded, making scheduler job
// IDs unique across processes sharing a store file (#1308).
func cronIDSuffix() string {
	var b [4]byte
	if _, err := crand.Read(b[:]); err != nil {
		// Fall back to time-based uniqueness; collision odds across
		// processes remain negligible for the fallback path.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// Create adds a new scheduled job and returns its snapshot.
// The cron expression is a standard 5-field format:
//
//	minute hour day-of-month month day-of-week
//
// Supports: *, */N, N, N-M, N,M,K, N-M/S
// minCronIntervalMinutes is the minimum allowed interval between cron job
// firings. Prevents the agent from creating jobs that fire too frequently
// (e.g. every second or every minute), which would flood the context with
// repeated prompts and waste API budget.
const minCronIntervalMinutes = 5

func (s *Scheduler) Create(cronExpr, prompt string, recurring bool, queueIfBusy bool) (Job, error) {
	now := time.Now()
	next, err := NextTime(cronExpr, now)
	if err != nil {
		return Job{}, err
	}

	// Enforce minimum interval: check that the second fire is at least
	// minCronIntervalMinutes after the first.
	second, _ := NextTime(cronExpr, next)
	interval := second.Sub(next)
	if interval > 0 && interval < time.Duration(minCronIntervalMinutes)*time.Minute {
		return Job{}, fmt.Errorf("cron interval too short: %v (minimum %d minutes). Use a larger interval to avoid flooding the agent with repeated prompts.", interval, minCronIntervalMinutes)
	}

	s.mu.Lock()
	s.nextID++
	// #1308: bare "cron-<n>" collides across processes (each counts from
	// 1), so a foreign job with the same numeric suffix was silently
	// overwritten on merge. Append a random suffix for global uniqueness;
	// the numeric prefix keeps Load's max-ID scan working.
	id := fmt.Sprintf("cron-%d-%s", s.nextID, cronIDSuffix())
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
	s.knownIDs[id] = true // #1308: seen here - don't merge it back on save
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
		next, err := NextTime(*cronExpr, time.Now())
		if err != nil {
			s.mu.Unlock()
			return Job{}, fmt.Errorf("invalid cron expression %q: %w", *cronExpr, err)
		}
		// Enforce minimum interval (same as Create).
		second, _ := NextTime(*cronExpr, next)
		interval := second.Sub(next)
		if interval > 0 && interval < time.Duration(minCronIntervalMinutes)*time.Minute {
			s.mu.Unlock()
			return Job{}, fmt.Errorf("cron interval too short: %v (minimum %d minutes)", interval, minCronIntervalMinutes)
		}
	}

	// Snapshot original values for rollback on save failure.
	origCron := job.CronExpr
	origPrompt := job.Prompt
	origQueue := job.QueueIfBusy
	origNextFire := job.NextFire

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
		// Paused jobs must keep a zero NextFire (the #311 invariant maintained
		// by Pause() and Load()): the timer is not scheduled while paused, so a
		// non-zero NextFire would show the UI a fire time that never arrives.
		// Resume() recomputes it from the new expression. (#667)
		if !job.Paused {
			job.NextFire = next
		}
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
			if job.Paused {
				// Preserve the paused zero-NextFire invariant on rollback too. (#667)
				job.NextFire = time.Time{}
			} else if next, nerr := NextTime(origCron, time.Now()); nerr != nil {
				// origCron was previously validated, so this path is defensive;
				// a zero NextFire here would make scheduleJobLocked clamp the
				// delay to 0 and fire immediately via AfterFunc(0). Keep the
				// pre-update value instead. (#667)
				job.NextFire = origNextFire
				debug.Log("cron", "Update rollback: NextTime(%q) failed: %v; keeping prior NextFire", origCron, nerr)
			} else {
				job.NextFire = next
			}
		}
		if t, ok := s.timers[id]; ok {
			t.Stop()
			delete(s.timers, id)
		}
		if !job.Paused {
			if job.NextFire.After(time.Now()) {
				s.scheduleJobLocked(job)
			} else {
				// Only reachable via the defensive NextTime-failure branch
				// above, where the restored NextFire is zero or already
				// elapsed. Rebuilding the timer anyway would clamp the delay
				// to 0 and fire immediately via AfterFunc(0) — a spurious fire
				// the user never asked for (#673). Leave the job unscheduled;
				// the log line keeps the stalled timer observable, and the next
				// Update/Resume/Load reschedules it.
				debug.Log("cron", "Update rollback: job %s NextFire %v is not in the future; skipping timer rebuild to avoid an immediate AfterFunc(0) fire", id, job.NextFire)
			}
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
	// Clear NextFire so consumers (e.g., the desktop UI) don't keep showing
	// a stale "next fire" time while the job is suspended. Resume()
	// recomputes it from the cron expression. (issue #311)
	origNextFire := job.NextFire
	job.NextFire = time.Time{}
	if timer, ok := s.timers[id]; ok {
		timer.Stop()
		delete(s.timers, id)
	}
	s.mu.Unlock()

	if err := s.save(); err != nil {
		// Rollback
		s.mu.Lock()
		job.Paused = false
		job.NextFire = origNextFire
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
		// Match Pause() semantics (issue #519 companion): a paused job must
		// not keep showing a stale next-fire time in UI/List.
		job.NextFire = time.Time{}
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
	// Stop any existing timer for this job before creating a new one.
	// Without this, Load() called twice (e.g., initial Load + SwitchSession→Load)
	// would leave the old timer running alongside the new one → double-fire.
	if old, ok := s.timers[job.ID]; ok {
		old.Stop()
		delete(s.timers, job.ID)
	}
	// Increment generation so that any previously scheduled timer callback
	// can detect that it is stale and abort, preventing double-fire races.
	s.generations[job.ID]++
	gen := s.generations[job.ID]

	delay := time.Until(job.NextFire)
	if delay < 0 {
		delay = 0
	}
	s.timers[job.ID] = time.AfterFunc(delay, func() {
		s.fireJob(job, gen)
	})
}

// fireJob is the timer-callback body for one scheduled fire of job at
// generation gen. It is extracted from scheduleJobLocked's AfterFunc closure
// so the fire path (including the debounce branch) can be exercised directly
// by tests simulating double-trigger races (issue #519), and so the panic
// recovery covers the whole body.
func (s *Scheduler) fireJob(job *Job, gen uint64) {
	defer func() {
		if r := recover(); r != nil {
			debug.Log("cron", "panic in timer callback for job %s: %v\n%s", job.ID, r, runtimedebug.Stack())
		}
	}()

	// Read mutable fields under lock to avoid data race with Update().
	s.mu.Lock()
	// If a newer timer was scheduled (Update/Resume/Create), abort this
	// stale callback to prevent a duplicate fire.
	if s.generations[job.ID] != gen {
		s.mu.Unlock()
		return
	}
	if _, exists := s.jobs[job.ID]; !exists {
		s.mu.Unlock()
		return
	}
	// Debounce: skip if this job was enqueued within the last 5 seconds.
	// This prevents double-fire when Update runs during the unlocked
	// enqueue window and creates a new timer that fires at the same time.
	if last, ok := s.lastEnqueue[job.ID]; ok && time.Since(last) < 5*time.Second {
		debug.Log("cron", "debounced duplicate fire for job %s (last enqueue %s ago)", job.ID, time.Since(last).Round(time.Millisecond))
		if job.Recurring {
			// Advance NextFire BEFORE rescheduling (issue #519): the timer
			// that just fired consumed the current slot, so
			// time.Until(NextFire) <= 0 here. Leaving it would clamp the
			// delay to zero (AfterFunc(0)) and immediately re-enter this
			// branch — a busy spin holding the lock for the rest of the 5s
			// debounce window, after which the callback falls through to the
			// normal path and enqueues a duplicate anyway, defeating the
			// debounce entirely. Mirrors the post-enqueue reschedule below.
			next, err := NextTime(job.CronExpr, job.NextFire)
			if err != nil {
				// Broken expression: same disposition as the post-enqueue path.
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
			// Still reschedule the next timer — without this, a debounced
			// fire would permanently break the timer chain and the job would
			// never fire again.
			s.scheduleJobLocked(job)
		}
		// Non-recurring: the fire that set lastEnqueue owns this job's
		// lifecycle (its post-enqueue path deletes the one-shot). Rescheduling
		// here would resurrect the job forever; deleting here would race the
		// owner. Just drop this duplicate fire.
		s.mu.Unlock()
		return
	}
	prompt := job.Prompt
	queueIfBusy := job.QueueIfBusy
	s.lastEnqueue[job.ID] = time.Now()
	s.mu.Unlock()

	s.enqueue(prompt, queueIfBusy)

	s.mu.Lock()
	// Re-check generation after enqueue: Update may have run during the
	// unlocked window, scheduling a newer timer. If so, this stale callback
	// must NOT reschedule (that would create an orphaned duplicate timer).
	if s.generations[job.ID] != gen {
		s.mu.Unlock()
		return
	}
	// Check if job was deleted while we were enqueueing (TOCTOU fix).
	// Without this check, a deleted recurring job would be re-scheduled
	// here, creating an infinite loop of phantom firings.
	if _, exists := s.jobs[job.ID]; !exists {
		s.mu.Unlock()
		return
	}
	if job.Recurring {
		// Reschedule from job.NextFire (the intended fire time), NOT time.Now().
		// If the timer fired slightly early (e.g., NextFire=08:55:00 but fired at
		// 08:54:59), using time.Now() would cause NextTime to return 08:55:00
		// again - the same slot - resulting in a double-fire. Using NextFire
		// guarantees we always advance past the current slot.
		next, err := NextTime(job.CronExpr, job.NextFire)
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
	// Bump ALL generation counters so that any in-flight timer callback
	// from the old session detects a stale generation and aborts before
	// calling enqueue. Without this, Load() could recreate a job with the
	// same ID before an old callback checks s.jobs[job.ID], allowing the
	// old callback to pass both the generation and existence checks and
	// fire alongside the new timer — causing a double-fire.
	s.mu.Lock()
	for id, timer := range s.timers {
		timer.Stop()
		delete(s.timers, id)
	}
	for id := range s.jobs {
		delete(s.jobs, id)
	}
	// Bump generations for ALL known jobs so old callbacks abort.
	for id := range s.generations {
		s.generations[id]++
	}
	// Do NOT clear lastEnqueue HERE — if a timer from the old session is
	// still executing its callback (Stop doesn't wait for in-flight callbacks),
	// clearing lastEnqueue mid-callback would be racy. It is reset AFTER Load()
	// below, which is the earliest safe point: the new generation counters
	// already force any old in-flight callback to abort before enqueue, so the
	// debounce map can be safely rebuilt for the new session (issue #554 G).
	s.nextID = 0
	s.storePath = storePath
	s.mu.Unlock()

	debug.Log("cron", "SwitchSession: cleared old jobs, bumped generations, rebinding to %s", storePath)

	// Migrate from old workspace-scoped store if present.
	MigrateWorkspaceJobs(oldStorePath, storePath, workspaceDir)

	s.Load()

	// Reset debounce timestamps after Load(). Job IDs are NOT unique across
	// sessions (both sessions typically number their first job "cron-1"), so
	// a stale lastEnqueue["cron-1"] from the old session makes fireJob's 5s
	// debounce silently swallow the NEW session's first fire — the ver-41
	// probe observed fired==0 exactly this way (issue #554 G). Pruning by
	// "exists in new job set" would be useless for those same-named IDs, so
	// clear the whole map instead: the generation bump above already forces
	// any still-running old-session callback to abort before enqueue, meaning
	// there is no legitimate old-session fire left to debounce against.
	s.mu.Lock()
	s.lastEnqueue = make(map[string]time.Time)
	s.mu.Unlock()
}

// Shutdown stops all timers and clears all jobs. The scheduler cannot be
// reused after shutdown.
func (s *Scheduler) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.patrolStop != nil {
		close(s.patrolStop)
		s.patrolStop = nil
	}
	for id, timer := range s.timers {
		timer.Stop()
		delete(s.timers, id)
	}
	for id := range s.jobs {
		delete(s.jobs, id)
	}
	for id := range s.lastEnqueue {
		delete(s.lastEnqueue, id)
	}
}
