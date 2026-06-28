package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestCreateEveryNMinutes(t *testing.T) {
	var called atomic.Int32
	s := NewScheduler(func(prompt string, _ bool) {
		called.Add(1)
	}, "")

	job, err := s.Create("*/1 * * * *", "test prompt", true, false)
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
	s := NewScheduler(nil, "")
	_, err := s.Create("invalid", "test", true, false)
	if err == nil {
		t.Error("expected error for invalid cron expression")
	}
}

func TestOneShotJob(t *testing.T) {
	var called atomic.Int32
	s := NewScheduler(func(prompt string, _ bool) {
		called.Add(1)
	}, "")

	// Use a very short interval for testing: */1 minute
	job, err := s.Create("*/1 * * * *", "test", false, false)
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
	s := NewScheduler(nil, "")
	_, ok := s.Get("cron-999")
	if ok {
		t.Error("expected not found")
	}
}

// --- Persistence tests ---

func withTestStore(t *testing.T) (storePath string, cleanup func()) {
	t.Helper()
	tmpDir := t.TempDir()
	storePath = filepath.Join(tmpDir, "cron-jobs.json")
	return storePath, func() {}
}

func TestCreatePersists(t *testing.T) {
	storePath, _ := withTestStore(t)
	wsDir := t.TempDir()

	s := NewScheduler(nil, storePath)
	s.Load(wsDir)

	_, err := s.Create("*/5 * * * *", "check CI", true, false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify file was written
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("expected store file to exist: %v", err)
	}

	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("failed to parse store file: %v", err)
	}

	key := workspaceKey(wsDir)
	bucket, ok := sf[key]
	if !ok {
		t.Fatal("expected workspace key in store file")
	}
	if bucket.Workspace != wsDir {
		t.Errorf("expected workspace %s, got %s", wsDir, bucket.Workspace)
	}
	if len(bucket.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(bucket.Jobs))
	}
	if bucket.Jobs[0].CronExpr != "*/5 * * * *" {
		t.Errorf("expected cron expr */5 * * * *, got %s", bucket.Jobs[0].CronExpr)
	}
	if bucket.Jobs[0].Prompt != "check CI" {
		t.Errorf("expected prompt 'check CI', got %s", bucket.Jobs[0].Prompt)
	}
}

func TestOneShotNotPersisted(t *testing.T) {
	storePath, _ := withTestStore(t)
	wsDir := t.TempDir()

	s := NewScheduler(nil, storePath)
	s.Load(wsDir)

	_, err := s.Create("*/1 * * * *", "one-shot reminder", false, false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify file was NOT written (or has no jobs)
	data, err := os.ReadFile(storePath)
	if err != nil {
		// File doesn't exist — perfect, one-shot shouldn't persist
		return
	}

	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("failed to parse store file: %v", err)
	}

	key := workspaceKey(wsDir)
	if _, ok := sf[key]; ok {
		t.Error("expected no workspace entry for one-shot job")
	}
}

func TestDeletePersists(t *testing.T) {
	storePath, _ := withTestStore(t)
	wsDir := t.TempDir()

	s := NewScheduler(nil, storePath)
	s.Load(wsDir)

	job, err := s.Create("*/5 * * * *", "check CI", true, false)
	if err != nil {
		t.Fatal(err)
	}

	// Delete
	if !s.Delete(job.ID) {
		t.Fatal("expected delete to succeed")
	}

	// Verify file has no jobs
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("expected store file to exist: %v", err)
	}

	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("failed to parse store file: %v", err)
	}

	key := workspaceKey(wsDir)
	if _, ok := sf[key]; ok {
		t.Error("expected workspace entry to be removed after deleting all jobs")
	}
}

func TestLoadRestoresJobs(t *testing.T) {
	storePath, _ := withTestStore(t)
	wsDir := t.TempDir()

	// Create a scheduler, add a recurring job
	s1 := NewScheduler(nil, storePath)
	s1.Load(wsDir)
	job1, err := s1.Create("*/5 * * * *", "check CI", true, false)
	if err != nil {
		t.Fatal(err)
	}
	s1.Shutdown()

	// Create a new scheduler and load from the same store
	s2 := NewScheduler(nil, storePath)
	s2.Load(wsDir)

	jobs := s2.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job after load, got %d", len(jobs))
	}
	if jobs[0].CronExpr != "*/5 * * * *" {
		t.Errorf("expected cron expr */5 * * * *, got %s", jobs[0].CronExpr)
	}
	if jobs[0].Prompt != "check CI" {
		t.Errorf("expected prompt 'check CI', got %s", jobs[0].Prompt)
	}
	if jobs[0].ID != job1.ID {
		t.Errorf("expected ID %s, got %s", job1.ID, jobs[0].ID)
	}
}

func TestLoadNoFileIsNoop(t *testing.T) {
	storePath := "/nonexistent/path/cron-jobs.json"
	wsDir := t.TempDir()

	s := NewScheduler(nil, storePath)
	s.Load(wsDir)

	jobs := s.List()
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs, got %d", len(jobs))
	}
}

func TestLoadCorruptedFileIsNoop(t *testing.T) {
	storePath, _ := withTestStore(t)
	wsDir := t.TempDir()

	// Write corrupted JSON
	os.WriteFile(storePath, []byte("not json"), 0644)

	s := NewScheduler(nil, storePath)
	s.Load(wsDir)

	jobs := s.List()
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs from corrupted file, got %d", len(jobs))
	}
}

func TestMultipleWorkspaces(t *testing.T) {
	storePath, _ := withTestStore(t)
	wsA := filepath.Join(t.TempDir(), "project-a")
	wsB := filepath.Join(t.TempDir(), "project-b")
	os.MkdirAll(wsA, 0755)
	os.MkdirAll(wsB, 0755)

	// Workspace A creates a job
	sA := NewScheduler(nil, storePath)
	sA.Load(wsA)
	_, err := sA.Create("*/5 * * * *", "task A", true, false)
	if err != nil {
		t.Fatal(err)
	}
	sA.Shutdown()

	// Workspace B creates a different job
	sB := NewScheduler(nil, storePath)
	sB.Load(wsB)
	_, err = sB.Create("*/10 * * * *", "task B", true, false)
	if err != nil {
		t.Fatal(err)
	}
	sB.Shutdown()

	// Verify file has both workspaces
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}

	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatal(err)
	}

	if len(sf) != 2 {
		t.Errorf("expected 2 workspaces, got %d", len(sf))
	}

	// Workspace A loads — should only see its own job
	sA2 := NewScheduler(nil, storePath)
	sA2.Load(wsA)
	jobsA := sA2.List()
	if len(jobsA) != 1 {
		t.Fatalf("expected 1 job for workspace A, got %d", len(jobsA))
	}
	if jobsA[0].Prompt != "task A" {
		t.Errorf("expected prompt 'task A', got %s", jobsA[0].Prompt)
	}

	// Workspace B loads — should only see its own job
	sB2 := NewScheduler(nil, storePath)
	sB2.Load(wsB)
	jobsB := sB2.List()
	if len(jobsB) != 1 {
		t.Fatalf("expected 1 job for workspace B, got %d", len(jobsB))
	}
	if jobsB[0].Prompt != "task B" {
		t.Errorf("expected prompt 'task B', got %s", jobsB[0].Prompt)
	}
}

func TestWorkspaceKeyDeterministic(t *testing.T) {
	dir := "/Users/user/projects/my-project"
	k1 := workspaceKey(dir)
	k2 := workspaceKey(dir)
	if k1 != k2 {
		t.Error("workspace key should be deterministic")
	}
	if len(k1) != 64 {
		t.Errorf("expected 64-char hex SHA256, got %d chars", len(k1))
	}

	// Different dirs produce different keys
	k3 := workspaceKey("/Users/user/projects/other")
	if k1 == k3 {
		t.Error("different dirs should produce different keys")
	}
}
