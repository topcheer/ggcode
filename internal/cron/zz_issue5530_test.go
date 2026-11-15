package cron

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the deadline expires.
func waitFor5530(t *testing.T, deadline time.Duration, cond func() bool) bool {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// rearmForTest pulls a job's next fire to now+delay and rearms its timer,
// simulating the passage of time without waiting out the 5-minute minimum
// cron interval. Returns the pinned fire time so the caller can observe the
// fire path's post-enqueue re-arm (NextFire moves strictly past it).
func rearmForTest(s *Scheduler, id string, delay time.Duration) time.Time {
	s.mu.Lock()
	job := s.jobs[id]
	fireAt := time.Now().Add(delay)
	job.NextFire = fireAt
	s.scheduleJobLocked(job)
	s.mu.Unlock()
	return fireAt
}

// waitFired5530 waits for the scheduler to deliver a prompt through the real
// timer fire path (fireJob -> enqueue) and for fireJob to finish its
// post-enqueue re-arm. In production the consuming surface executes the run
// asynchronously and reports the outcome only after the run completes, i.e.
// always after this point - the test mirrors that ordering.
func waitFired5530(t *testing.T, s *Scheduler, id string, fired <-chan string, pinned time.Time) string {
	t.Helper()
	var prompt string
	select {
	case prompt = <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler never fired the round")
	}
	if !waitFor5530(t, 5*time.Second, func() bool {
		j, _ := s.Get(id)
		return j.NextFire.After(pinned)
	}) {
		t.Fatal("fire path did not re-arm after enqueue")
	}
	return prompt
}

// TestIssue5530RealFireRecordsNoDiffOutcomeAndAutoPauses is the integration
// test the tracking issue demands: it drives real fires through the
// scheduler timer path with a production-shaped consumer that reports each
// completed run's produced-diff outcome back via RecordRunOutcome, and
// observes the streak increment and the auto-pause engage.
func TestIssue5530RealFireRecordsNoDiffOutcomeAndAutoPauses(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "cron-jobs.json")
	fired := make(chan string, 4)

	s := NewScheduler(func(prompt string, queueIfBusy bool) {
		// Production-shape consumer (#5530): the surface receives the
		// prompt, executes the run, and reports the produced-diff
		// outcome once the run completes (done by the test goroutine
		// below, after the fire path finishes re-arming).
		fired <- prompt
	}, storePath)
	defer s.Shutdown()

	job, err := s.Create("*/5 * * * *", "sentinel: dependency sweep", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureNoDiff(job.ID, 2); err != nil {
		t.Fatal(err)
	}

	// Round 1: real timer fire; the run produced no diff.
	pinned := rearmForTest(s, job.ID, 20*time.Millisecond)
	prompt := waitFired5530(t, s, job.ID, fired, pinned)
	if prompt != "sentinel: dependency sweep" {
		t.Fatalf("unexpected fired prompt %q", prompt)
	}
	if id := s.RecordRunOutcome(prompt, false); id != job.ID {
		t.Fatalf("run outcome must correlate to job %s, got %q", job.ID, id)
	}
	if !waitFor5530(t, 5*time.Second, func() bool {
		j, _ := s.Get(job.ID)
		return j.NoDiffStreak == 1
	}) {
		j, _ := s.Get(job.ID)
		t.Fatalf("round 1 must increment the streak to 1, got %+v", j)
	}
	if j, _ := s.Get(job.ID); j.Paused {
		t.Fatal("a single no-diff round below threshold must not pause")
	}

	// Round 2: real timer fire; again no diff -> the streak reaches the
	// threshold and the job auto-pauses. Clear the 5s duplicate-fire
	// debounce stamp so this is a fresh fire, exactly like a later
	// production round (production rounds are >= 5 minutes apart).
	s.mu.Lock()
	delete(s.lastEnqueue, job.ID)
	s.mu.Unlock()
	pinned = rearmForTest(s, job.ID, 20*time.Millisecond)
	prompt = waitFired5530(t, s, job.ID, fired, pinned)
	s.RecordRunOutcome(prompt, false)

	if !waitFor5530(t, 5*time.Second, func() bool {
		j, _ := s.Get(job.ID)
		return j.Paused
	}) {
		j, _ := s.Get(job.ID)
		t.Fatalf("reaching the threshold (2) must auto-pause: %+v", j)
	}
	j, _ := s.Get(job.ID)
	if j.NoDiffStreak != 2 || !strings.Contains(j.PausedReason, "no-diff") {
		t.Fatalf("paused job must carry streak + reason: %+v", j)
	}
	if !j.NextFire.IsZero() {
		t.Fatalf("auto-paused job must have NextFire zeroed: %+v", j)
	}
	s.mu.Lock()
	_, hasTimer := s.timers[job.ID]
	s.mu.Unlock()
	if hasTimer {
		t.Fatal("auto-paused job must have its timer stopped")
	}

	// The auto-pause is persisted: a fresh scheduler over the same store
	// adopts the job still paused, with streak and reason intact.
	s2 := NewScheduler(func(string, bool) {}, storePath)
	defer s2.Shutdown()
	s2.Load()
	j2, ok := s2.Get(job.ID)
	if !ok || !j2.Paused || j2.NoDiffStreak != 2 || !strings.Contains(j2.PausedReason, "no-diff") {
		t.Fatalf("auto-pause must persist across Load: ok=%v %+v", ok, j2)
	}
}

// TestIssue5530RealFireDiffRoundResetsStreak drives a no-diff fire followed
// by a producing fire through the real timer path: the producing round must
// reset the streak, not pause the job.
func TestIssue5530RealFireDiffRoundResetsStreak(t *testing.T) {
	fired := make(chan string, 4)
	s := NewScheduler(func(prompt string, queueIfBusy bool) {
		fired <- prompt
	}, "")
	defer s.Shutdown()

	job, err := s.Create("*/5 * * * *", "sentinel: weekly report", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureNoDiff(job.ID, 2); err != nil {
		t.Fatal(err)
	}

	pinned := rearmForTest(s, job.ID, 20*time.Millisecond)
	prompt := waitFired5530(t, s, job.ID, fired, pinned)
	s.RecordRunOutcome(prompt, false)
	if !waitFor5530(t, 5*time.Second, func() bool {
		j, _ := s.Get(job.ID)
		return j.NoDiffStreak == 1
	}) {
		j, _ := s.Get(job.ID)
		t.Fatalf("no-diff round must be recorded, got %+v", j)
	}

	s.mu.Lock()
	delete(s.lastEnqueue, job.ID)
	s.mu.Unlock()
	pinned = rearmForTest(s, job.ID, 20*time.Millisecond)
	prompt = waitFired5530(t, s, job.ID, fired, pinned)
	s.RecordRunOutcome(prompt, true)
	if !waitFor5530(t, 5*time.Second, func() bool {
		j, _ := s.Get(job.ID)
		return j.NoDiffStreak == 0
	}) {
		j, _ := s.Get(job.ID)
		t.Fatalf("producing round must reset the streak, got %+v", j)
	}
	if j, _ := s.Get(job.ID); j.Paused {
		t.Fatalf("a producing round must never pause the job: %+v", j)
	}
}

// TestIssue5530RecordRunOutcomeIgnoresUnknownPrompt pins the safety contract
// that lets surfaces call the correlation entry unconditionally: ordinary
// user prompts resolve to no job and change no state.
func TestIssue5530RecordRunOutcomeIgnoresUnknownPrompt(t *testing.T) {
	s := NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	job, err := s.Create("*/5 * * * *", "sentinel prompt", true, false)
	if err != nil {
		t.Fatal(err)
	}

	if id := s.RecordRunOutcome("an ordinary user prompt", false); id != "" {
		t.Fatalf("unknown prompt must resolve to no job, got %q", id)
	}
	j, _ := s.Get(job.ID)
	if j.NoDiffStreak != 0 {
		t.Fatalf("unknown prompt must not touch job state: %+v", j)
	}

	// A matched prompt returns the job ID (trimmed/case-insensitive, per
	// JobIDForPrompt's documented exact-match semantics).
	if id := s.RecordRunOutcome("  Sentinel Prompt  ", true); id != job.ID {
		t.Fatalf("matched prompt must resolve the job, got %q", id)
	}
	j, _ = s.Get(job.ID)
	if j.NoDiffStreak != 0 {
		t.Fatalf("producing outcome must keep the streak reset: %+v", j)
	}
}
