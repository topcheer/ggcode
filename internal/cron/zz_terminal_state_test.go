package cron

import (
	"strings"
	"testing"
)

func TestConfigureAndRecordNoDiffStreak(t *testing.T) {
	s := NewScheduler(func(string, bool) {}, "")
	team := ""
	_ = team
	job, err := s.Create("*/5 * * * *", "sentinel prompt", true, false)
	if err != nil {
		t.Fatal(err)
	}

	// Disabled by default: streaks accumulate but never pause.
	for i := 0; i < 10; i++ {
		if err := s.RecordFireOutcome(job.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	j, _ := s.Get(job.ID)
	if j.Paused || j.NoDiffStreak != 10 {
		t.Fatalf("disabled job must count streak but not pause: %+v", j)
	}

	// Enable at 3, then fire 2 no-diff + 1 diff (reset) + 3 no-diff -> pause.
	if err := s.ConfigureNoDiff(job.ID, 3); err != nil {
		t.Fatal(err)
	}
	_ = s.RecordFireOutcome(job.ID, false)
	_ = s.RecordFireOutcome(job.ID, false)
	if err := s.RecordFireOutcome(job.ID, true); err != nil {
		t.Fatal(err)
	}
	j, _ = s.Get(job.ID)
	if j.NoDiffStreak != 0 {
		t.Fatalf("diff round must reset streak, got %d", j.NoDiffStreak)
	}
	for i := 0; i < 3; i++ {
		_ = s.RecordFireOutcome(job.ID, false)
	}
	j, _ = s.Get(job.ID)
	if !j.Paused || !strings.Contains(j.PausedReason, "no-diff") {
		t.Fatalf("threshold must auto-pause with reason: %+v", j)
	}

	// Explicit resume (wake channel) clears streak + reason.
	if err := s.Resume(job.ID); err != nil {
		t.Fatal(err)
	}
	j, _ = s.Get(job.ID)
	if j.Paused || j.NoDiffStreak != 0 || j.PausedReason != "" {
		t.Fatalf("resume must wake cleanly: %+v", j)
	}
}

func TestJobIDForPromptExactMatch(t *testing.T) {
	s := NewScheduler(func(string, bool) {}, "")
	prompt := "unique sentinel prompt"
	job, err := s.Create("0 * * * *", prompt, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.JobIDForPrompt(prompt); got != job.ID {
		t.Fatalf("exact prompt must resolve to job, got %q want %q", got, job.ID)
	}
	if got := s.JobIDForPrompt("not a known prompt"); got != "" {
		t.Fatalf("unknown prompt must resolve empty, got %q", got)
	}
}

func TestNoDiffPersistsAcrossLoad(t *testing.T) {
	path := ""
	dir := t.TempDir()
	path = dir + "/jobs.json"
	s := NewScheduler(func(string, bool) {}, path)
	job, err := s.Create("*/10 * * * *", "persist me", true, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.ConfigureNoDiff(job.ID, 5)
	_ = s.RecordFireOutcome(job.ID, false)
	_ = s.RecordFireOutcome(job.ID, false)

	// Fresh scheduler over the same store must inherit streak + threshold.
	s2 := NewScheduler(func(string, bool) {}, path)
	s2.Load()
	j, ok := s2.Get(job.ID)
	if !ok {
		t.Fatal("job must survive Load roundtrip")
	}
	if j.NoDiffStreak != 2 || j.MaxNoDiffRounds != 5 {
		t.Fatalf("persistence roundtrip failed: streak=%d max=%d", j.NoDiffStreak, j.MaxNoDiffRounds)
	}
}

func TestRecordFireOutcomeUnknownJob(t *testing.T) {
	s := NewScheduler(func(string, bool) {}, "")
	if err := s.RecordFireOutcome("cron-999", false); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown job must error, got %v", err)
	}
}
