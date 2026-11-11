package cron

import (
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
	runtimedebug "runtime/debug"
)

type Job struct {
	ID          string
	CronExpr    string
	Prompt      string
	Recurring   bool
	QueueIfBusy bool
	CreatedAt   time.Time
	NextFire    time.Time
	Paused      bool
	// Persist marks a non-recurring job as durable across process restarts
	// (persisted with an explicit NextFire and re-adopted on Load while still
	// in the future). Zero-value false keeps ordinary one-shots volatile,
	// preserving the historic session-scoped semantics.
	Persist bool
}

func (j *Job) Snapshot() Job {
	return *j
}

type jobJSON struct {
	ID          string `json:"id"`
	CronExpr    string `json:"cron_expr"`
	Prompt      string `json:"prompt"`
	Recurring   bool   `json:"recurring"`
	QueueIfBusy bool   `json:"queue_if_busy"`
	Paused      bool   `json:"paused,omitempty"`
	CreatedAt   string `json:"created_at"`
	// Persist + NextFire are only meaningful for durable one-shot jobs
	// (see Job.Persist); recurring jobs recompute NextFire from CronExpr.
	Persist  bool   `json:"persist,omitempty"`
	NextFire string `json:"next_fire,omitempty"`
}

type sessionStore struct {
	Jobs []jobJSON `json:"jobs"`
}

type Scheduler struct {
	mu          sync.Mutex
	jobs        map[string]*Job
	nextID      int
	enqueue     func(prompt string, queueIfBusy bool)
	timers      map[string]*time.Timer
	generations map[string]uint64
	lastEnqueue map[string]time.Time
	storePath   string
	knownIDs    map[string]bool

	patrolInterval time.Duration
	patrolStop     chan struct{}
}

const defaultPatrolInterval = 30 * time.Second

const patrolGrace = 2 * time.Second

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

func (s *Scheduler) startPatrol() {
	s.mu.Lock()
	if s.patrolStop != nil {
		s.mu.Unlock()
		return
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

		if t, ok := s.timers[id]; ok {
			t.Stop()
			delete(s.timers, id)
		}
		s.generations[id]++

		if job.Recurring {
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

func (s *Scheduler) Load() {
	if s.storePath == "" {
		return
	}

	data, err := os.ReadFile(s.storePath)
	if err != nil {
		return
	}

	var ss sessionStore
	if err := json.Unmarshal(data, &ss); err != nil {
		debug.Log("cron", "Load: failed to parse store file %s: %v", s.storePath, err)
		return
	}

	sort.Slice(ss.Jobs, func(i, j int) bool {
		return ss.Jobs[i].CreatedAt < ss.Jobs[j].CreatedAt
	})

	for _, jj := range ss.Jobs {
		if !jj.Recurring {
			// Ordinary one-shots never persisted: skip silently. Durable
			// one-shots (restart nudges) are adopted while still pending.
			if !jj.Persist {
				continue
			}
			fireAt, err := time.Parse(time.RFC3339, jj.NextFire)
			if err != nil || fireAt.IsZero() {
				debug.Log("cron", "Load: dropped persisted one-shot %s (missing/invalid next_fire)", jj.ID)
				continue
			}
			if !time.Now().Before(fireAt) {
				debug.Log("cron", "Load: dropped expired persisted one-shot %s (was due %s)", jj.ID, fireAt.Format(time.RFC3339))
				continue
			}
			createdAt, _ := time.Parse(time.RFC3339, jj.CreatedAt)
			s.mu.Lock()
			if _, exists := s.jobs[jj.ID]; exists {
				s.mu.Unlock()
				continue
			}
			s.nextID++
			job := &Job{
				ID:          jj.ID,
				Prompt:      jj.Prompt,
				Recurring:   false,
				QueueIfBusy: jj.QueueIfBusy,
				CreatedAt:   createdAt,
				NextFire:    fireAt,
				Persist:     true,
			}
			s.jobs[job.ID] = job
			s.knownIDs[job.ID] = true
			var n int
			fmt.Sscanf(job.ID, "cron-%d", &n)
			if n > s.nextID {
				s.nextID = n
			}
			s.mu.Unlock()
			s.scheduleJob(job)
			debug.Log("cron", "Load: adopted pending one-shot %s (fires %s)", job.ID, fireAt.Format(time.RFC3339))
			continue
		}

		now := time.Now()
		next, err := NextTime(jj.CronExpr, now)
		if err != nil {
			continue
		}

		createdAt, _ := time.Parse(time.RFC3339, jj.CreatedAt)

		s.mu.Lock()
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
		if !job.Paused {
			job.NextFire = next
		}
		s.jobs[job.ID] = job
		s.knownIDs[job.ID] = true
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

func (s *Scheduler) save() error {
	if s.storePath == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var unlock func()
	if lock, err := util.FileLock(s.storePath + ".flock"); err == nil {
		unlock = lock
		defer unlock()
	} else {
		debug.Log("cron", "save: cross-process lock unavailable for %s: %v", s.storePath, err)
	}

	jobs := make([]jobJSON, 0)
	for _, j := range s.jobs {
		if !j.Recurring && !j.Persist {
			continue
		}
		nextFire := ""
		if j.Persist && !j.NextFire.IsZero() {
			nextFire = j.NextFire.Format(time.RFC3339)
		}
		jobs = append(jobs, jobJSON{
			ID:          j.ID,
			CronExpr:    j.CronExpr,
			Prompt:      j.Prompt,
			Recurring:   j.Recurring,
			QueueIfBusy: j.QueueIfBusy,
			Paused:      j.Paused,
			CreatedAt:   j.CreatedAt.Format(time.RFC3339),
			Persist:     j.Persist,
			NextFire:    nextFire,
		})
	}

	if data, err := os.ReadFile(s.storePath); err == nil {
		var onDisk sessionStore
		if json.Unmarshal(data, &onDisk) == nil {
			inMemory := make(map[string]bool, len(jobs))
			for _, j := range jobs {
				inMemory[j.ID] = true
			}
			for _, jj := range onDisk.Jobs {
				if (jj.Recurring || jj.Persist) && !inMemory[jj.ID] && !s.knownIDs[jj.ID] {
					jobs = append(jobs, jj)
				}
			}
		}
	}

	if len(jobs) == 0 {
		if err := os.Remove(s.storePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove cron store %s: %w", s.storePath, err)
		}
		if err := os.Remove(s.storePath + ".flock"); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove cron store lock %s: %w", s.storePath+".flock", err)
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

func cronIDSuffix() string {
	var b [4]byte
	if _, err := crand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

const minCronIntervalMinutes = 5

// MaxOneShotHorizon is the maximum distance from creation time at which a
// one-shot (non-recurring) job is allowed to fire. A HH:MM-style reminder
// whose time has already passed today resolves to the same time NEXT YEAR,
// silently postponing the reminder for months; one-shot schedules farther
// out than this horizon are rejected at creation so the mistake surfaces
// immediately. Users who genuinely need a far-future one-shot reminder
// should spell out the full date (month and day) in the schedule.
// Recurring jobs (daily/weekly/...) are never subject to this limit.
const MaxOneShotHorizon = 90 * 24 * time.Hour

// checkOneShotHorizon rejects a one-shot (non-recurring) job whose next
// fire time lies farther than horizon from now. The horizon is a parameter
// (production callers pass MaxOneShotHorizon) so tests can probe the exact
// boundary. Recurring jobs pass unconditionally.
func checkOneShotHorizon(recurring bool, cronExpr string, next, now time.Time, horizon time.Duration) error {
	if recurring || next.IsZero() || horizon <= 0 {
		return nil
	}
	if d := next.Sub(now); d > horizon {
		return fmt.Errorf(
			"one-shot schedule %q would first fire at %s (%s from now, limit %s): "+
				"the intended time has most likely already passed and rolled over to next year; "+
				"if you really want a reminder that far ahead, specify the full date (month and day) explicitly",
			cronExpr, next.Format("2006-01-02 15:04"), d.Round(time.Minute), horizon)
	}
	return nil
}

func (s *Scheduler) Create(cronExpr, prompt string, recurring bool, queueIfBusy bool) (Job, error) {
	return s.CreateAcknowledgedFar(cronExpr, prompt, recurring, queueIfBusy, false)
}

// CreateOneShotAt creates a durable one-shot job firing exactly at fireAt
// (no cron expression; NextFire is authoritative). The job is persisted with
// its explicit NextFire and survives process restarts: on Load, a still-
// pending instance is re-adopted and an expired one is dropped without
// firing. Intended for cross-restart nudges such as the restart tool's
// resume prompt. queueIfBusy should be true so the prompt still enqueues
// while the resumed session is busy.
func (s *Scheduler) CreateOneShotAt(fireAt time.Time, prompt string, queueIfBusy bool) (Job, error) {
	if strings.TrimSpace(prompt) == "" {
		return Job{}, fmt.Errorf("one-shot prompt is required")
	}
	if fireAt.IsZero() {
		return Job{}, fmt.Errorf("one-shot fire time is required")
	}

	now := time.Now()
	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("cron-%d-%s", s.nextID, cronIDSuffix())
	job := &Job{
		ID:          id,
		Prompt:      prompt,
		Recurring:   false,
		QueueIfBusy: queueIfBusy,
		CreatedAt:   now,
		NextFire:    fireAt,
		Persist:     true,
	}
	s.jobs[id] = job
	s.knownIDs[id] = true
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
		debug.Log("cron", "CreateOneShotAt: failed to persist job %s: %v", id, err)
		return Job{}, err
	}

	debug.Log("cron", "CreateOneShotAt: added durable one-shot %s (fires %s)", id, fireAt.Format(time.RFC3339))
	return job.Snapshot(), nil
}

// CreateAcknowledgedFar behaves like Create but lets the caller explicitly
// acknowledge a one-shot first fire beyond MaxOneShotHorizon (the far=false
// default rejects it). When acknowledged and the horizon is exceeded, the
// job is still created but a WARNING is logged so the far-future fire is
// visible in diagnostics (the year+1 rollover trap is opt-in, not silent).
func (s *Scheduler) CreateAcknowledgedFar(cronExpr, prompt string, recurring, queueIfBusy, farAcknowledged bool) (Job, error) {
	now := time.Now()
	next, err := NextTime(cronExpr, now)
	if err != nil {
		return Job{}, err
	}

	if err := checkOneShotHorizon(recurring, cronExpr, next, now, MaxOneShotHorizon); err != nil {
		if !farAcknowledged {
			debug.Log("cron", "Create: rejected one-shot job (expr=%s): %v", cronExpr, err)
			return Job{}, err
		}
		debug.Log("cron", "WARNING: one-shot job acknowledged to fire far in the future (expr=%s): %v", cronExpr, err)
	}

	second, _ := NextTime(cronExpr, next)
	interval := second.Sub(next)
	if interval > 0 && interval < time.Duration(minCronIntervalMinutes)*time.Minute {
		return Job{}, fmt.Errorf("cron interval too short: %v (minimum %d minutes). Use a larger interval to avoid flooding the agent with repeated prompts.", interval, minCronIntervalMinutes)
	}

	s.mu.Lock()
	s.nextID++
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
	s.knownIDs[id] = true
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

func (s *Scheduler) Delete(id string) bool {
	deleted, err := s.DeleteWithError(id)
	return deleted && err == nil
}

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

func (s *Scheduler) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j.Snapshot())
	}
	return out
}

func (s *Scheduler) Get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	return j.Snapshot(), true
}

func (s *Scheduler) Update(id string, cronExpr *string, prompt *string, queueIfBusy *bool) (Job, error) {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return Job{}, fmt.Errorf("job %q not found", id)
	}

	if cronExpr != nil {
		next, err := NextTime(*cronExpr, time.Now())
		if err != nil {
			s.mu.Unlock()
			return Job{}, fmt.Errorf("invalid cron expression %q: %w", *cronExpr, err)
		}
		second, _ := NextTime(*cronExpr, next)
		interval := second.Sub(next)
		if interval > 0 && interval < time.Duration(minCronIntervalMinutes)*time.Minute {
			s.mu.Unlock()
			return Job{}, fmt.Errorf("cron interval too short: %v (minimum %d minutes)", interval, minCronIntervalMinutes)
		}
	}

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

	if cronExpr != nil {
		next, err := NextTime(job.CronExpr, time.Now())
		if err != nil {
			job.CronExpr = origCron
			s.mu.Unlock()
			return Job{}, err
		}
		if !job.Paused {
			job.NextFire = next
		}
	}
	newSnapshot := job.Snapshot()

	if timer, ok := s.timers[id]; ok {
		timer.Stop()
		delete(s.timers, id)
	}
	if !job.Paused {
		s.scheduleJobLocked(job)
	}
	s.mu.Unlock()

	if err := s.save(); err != nil {
		s.mu.Lock()
		job.CronExpr = origCron
		job.Prompt = origPrompt
		job.QueueIfBusy = origQueue
		if cronExpr != nil {
			if job.Paused {
				job.NextFire = time.Time{}
			} else if next, nerr := NextTime(origCron, time.Now()); nerr != nil {
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

func (s *Scheduler) Pause(id string) error {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("job %q not found", id)
	}
	if job.Paused {
		s.mu.Unlock()
		return nil
	}
	job.Paused = true
	origNextFire := job.NextFire
	job.NextFire = time.Time{}
	if timer, ok := s.timers[id]; ok {
		timer.Stop()
		delete(s.timers, id)
	}
	s.mu.Unlock()

	if err := s.save(); err != nil {
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

func (s *Scheduler) Resume(id string) error {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("job %q not found", id)
	}
	if !job.Paused {
		s.mu.Unlock()
		return nil
	}
	job.Paused = false

	next, err := NextTime(job.CronExpr, time.Now())
	if err != nil {
		job.Paused = true
		s.mu.Unlock()
		return fmt.Errorf("invalid cron expression in job %s: %w", id, err)
	}
	job.NextFire = next
	s.scheduleJobLocked(job)
	s.mu.Unlock()

	if err := s.save(); err != nil {
		s.mu.Lock()
		job.Paused = true
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

func (s *Scheduler) SetEnqueue(fn func(prompt string, queueIfBusy bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fn != nil {
		s.enqueue = fn
	}
}

func (s *Scheduler) Emit(prompt string, queueIfBusy bool) {
	s.mu.Lock()
	fn := s.enqueue
	s.mu.Unlock()
	if fn != nil {
		fn(prompt, queueIfBusy)
	}
}

func (s *Scheduler) scheduleJob(job *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduleJobLocked(job)
}

func (s *Scheduler) scheduleJobLocked(job *Job) {
	if old, ok := s.timers[job.ID]; ok {
		old.Stop()
		delete(s.timers, job.ID)
	}
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

func (s *Scheduler) fireJob(job *Job, gen uint64) {
	defer func() {
		if r := recover(); r != nil {
			debug.Log("cron", "panic in timer callback for job %s: %v\n%s", job.ID, r, runtimedebug.Stack())
		}
	}()

	s.mu.Lock()
	if s.generations[job.ID] != gen {
		s.mu.Unlock()
		return
	}
	if _, exists := s.jobs[job.ID]; !exists {
		s.mu.Unlock()
		return
	}
	if last, ok := s.lastEnqueue[job.ID]; ok && time.Since(last) < 5*time.Second {
		debug.Log("cron", "debounced duplicate fire for job %s (last enqueue %s ago)", job.ID, time.Since(last).Round(time.Millisecond))
		if job.Recurring {
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
		}
		s.mu.Unlock()
		return
	}
	prompt := job.Prompt
	queueIfBusy := job.QueueIfBusy
	s.lastEnqueue[job.ID] = time.Now()
	s.mu.Unlock()

	s.enqueue(prompt, queueIfBusy)

	s.mu.Lock()
	if s.generations[job.ID] != gen {
		s.mu.Unlock()
		return
	}
	if _, exists := s.jobs[job.ID]; !exists {
		s.mu.Unlock()
		return
	}
	if job.Recurring {
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
		persistRemoval := job.Persist
		delete(s.jobs, job.ID)
		delete(s.timers, job.ID)
		if persistRemoval {
			s.mu.Unlock()
			if err := s.save(); err != nil {
				debug.Log("cron", "failed to persist removal of one-shot job %s: %v", job.ID, err)
			}
			return
		}
	}
	s.mu.Unlock()
}

func (s *Scheduler) SetSession(storePath, oldStorePath, workspaceDir string) {
	if storePath == "" {
		return
	}

	s.mu.Lock()
	if s.storePath != "" {
		s.mu.Unlock()
		return
	}
	s.storePath = storePath
	s.mu.Unlock()

	MigrateWorkspaceJobs(oldStorePath, storePath, workspaceDir)

	s.Load()
}

func (s *Scheduler) SwitchSession(storePath, oldStorePath, workspaceDir string) {
	if storePath == "" {
		return
	}

	s.mu.Lock()
	for id, timer := range s.timers {
		timer.Stop()
		delete(s.timers, id)
	}
	for id := range s.jobs {
		delete(s.jobs, id)
	}
	for id := range s.generations {
		s.generations[id]++
	}
	s.nextID = 0
	s.storePath = storePath
	s.mu.Unlock()

	debug.Log("cron", "SwitchSession: cleared old jobs, bumped generations, rebinding to %s", storePath)

	MigrateWorkspaceJobs(oldStorePath, storePath, workspaceDir)

	s.Load()

	s.mu.Lock()
	s.lastEnqueue = make(map[string]time.Time)
	s.mu.Unlock()
}

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
