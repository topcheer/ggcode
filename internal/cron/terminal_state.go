package cron

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// ConfigureNoDiff enables the terminal-state guard (#5273): after
// maxRounds consecutive fire outcomes with producedDiff=false, a recurring
// job is auto-paused (PausedReason records why; Resume is the wake
// channel). maxRounds <= 0 disables the guard (streaks are still counted
// for observability, never acted on).
func (s *Scheduler) ConfigureNoDiff(id string, maxRounds int) error {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("job %q not found", id)
	}
	job.MaxNoDiffRounds = maxRounds
	s.mu.Unlock()
	return s.save()
}

// RecordFireOutcome feeds the per-fire result back into the job: a
// producing round resets the streak, an empty round increments it. When
// the streak reaches a configured MaxNoDiffRounds on a recurring job, the
// job auto-pauses (persisted) so a sentinel that outlived its workstream
// stops burning rounds forever.
func (s *Scheduler) RecordFireOutcome(id string, producedDiff bool) error {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("job %q not found", id)
	}
	if producedDiff {
		job.NoDiffStreak = 0
		s.mu.Unlock()
		return nil
	}
	job.NoDiffStreak++
	threshold := job.MaxNoDiffRounds
	recurring := job.Recurring
	streak := job.NoDiffStreak
	shouldPause := threshold > 0 && recurring && streak >= threshold
	if !shouldPause {
		s.mu.Unlock()
		return s.save()
	}

	// Auto-pause, mirroring Pause()'s save-failure rollback shape.
	job.Paused = true
	job.PausedReason = fmt.Sprintf("auto-paused after %d consecutive no-diff rounds (threshold %d); resume to wake", streak, threshold)
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
		job.PausedReason = ""
		job.NoDiffStreak = streak
		job.NextFire = origNextFire
		s.scheduleJobLocked(job)
		s.mu.Unlock()
		return err
	}
	debug.Log("cron", "RecordFireOutcome: auto-paused job %s (%s)", id, job.PausedReason)
	return nil
}

// JobIDForPrompt resolves the job a dequeued prompt belongs to (exact
// prompt match). The consumer side uses it after a run completes to call
// RecordFireOutcome with that run's produced-diff signal.
func (s *Scheduler) JobIDForPrompt(prompt string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.jobs))
	for id, j := range s.jobs {
		if strings.EqualFold(strings.TrimSpace(j.Prompt), strings.TrimSpace(prompt)) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	return ids[0]
}
