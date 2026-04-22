package cron

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestCreateEveryNMinutes(t *testing.T) {
	var called atomic.Int32
	s := NewScheduler(func(prompt string) {
		called.Add(1)
	})

	job, err := s.Create("*/1 * * * *", "test prompt", true)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != "cron-1" {
		t.Errorf("expected cron-1, got %s", job.ID)
	}
	if job.CronExpr != "*/1 * * * *" {
		t.Errorf("unexpected cron expr: %s", job.CronExpr)
	}
	if !job.Recurring {
		t.Error("expected recurring")
	}

	// Check the job is listed
	jobs := s.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	// Delete
	if !s.Delete(job.ID) {
		t.Error("expected delete to succeed")
	}
	if s.Delete(job.ID) {
		t.Error("expected second delete to fail")
	}
}

func TestCreateInvalidExpr(t *testing.T) {
	s := NewScheduler(nil)
	_, err := s.Create("invalid", "test", true)
	if err == nil {
		t.Error("expected error for invalid cron expression")
	}
}

func TestOneShotJob(t *testing.T) {
	var called atomic.Int32
	s := NewScheduler(func(prompt string) {
		called.Add(1)
	})

	// Use a very short interval for testing: */1 minute
	job, err := s.Create("*/1 * * * *", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if job.Recurring {
		t.Error("expected non-recurring")
	}

	// Verify job exists
	if _, ok := s.Get(job.ID); !ok {
		t.Error("job should exist")
	}
}

func TestNextTimeEveryN(t *testing.T) {
	base := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	next, err := NextTime("*/5 * * * *", base)
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2025, 1, 1, 10, 5, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestNextTimeEvery1(t *testing.T) {
	base := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	next, err := NextTime("*/1 * * * *", base)
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2025, 1, 1, 10, 1, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestNextTimeInvalid(t *testing.T) {
	_, err := NextTime("invalid", time.Now())
	if err == nil {
		t.Error("expected error for invalid expression")
	}
}

func TestGetNonexistent(t *testing.T) {
	s := NewScheduler(nil)
	_, ok := s.Get("cron-999")
	if ok {
		t.Error("expected not found")
	}
}
