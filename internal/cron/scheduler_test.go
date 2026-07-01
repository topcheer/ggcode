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

// --- Persistence tests (session-scoped) ---

func withTestStore(t *testing.T) (storePath string, cleanup func()) {
	t.Helper()
	tmpDir := t.TempDir()
	storePath = filepath.Join(tmpDir, "cron-jobs", "session-1.json")
	return storePath, func() {}
}

func TestCreatePersists(t *testing.T) {
	storePath, _ := withTestStore(t)

	s := NewScheduler(nil, storePath)
	s.Load()

	_, err := s.Create("*/5 * * * *", "check CI", true, false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify file was written
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("expected store file to exist: %v", err)
	}

	var ss sessionStore
	if err := json.Unmarshal(data, &ss); err != nil {
		t.Fatalf("failed to parse store file: %v", err)
	}

	if len(ss.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(ss.Jobs))
	}
	if ss.Jobs[0].CronExpr != "*/5 * * * *" {
		t.Errorf("expected cron expr */5 * * * *, got %s", ss.Jobs[0].CronExpr)
	}
	if ss.Jobs[0].Prompt != "check CI" {
		t.Errorf("expected prompt 'check CI', got %s", ss.Jobs[0].Prompt)
	}
}

func TestOneShotNotPersisted(t *testing.T) {
	storePath, _ := withTestStore(t)

	s := NewScheduler(nil, storePath)
	s.Load()

	_, err := s.Create("*/1 * * * *", "one-shot reminder", false, false)
	if err != nil {
		t.Fatal(err)
	}

	// File should not exist (no recurring jobs to persist)
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Error("expected store file to not exist for one-shot job")
	}
}

func TestDeletePersists(t *testing.T) {
	storePath, _ := withTestStore(t)

	s := NewScheduler(nil, storePath)
	s.Load()

	job, err := s.Create("*/5 * * * *", "check CI", true, false)
	if err != nil {
		t.Fatal(err)
	}

	// Delete
	if !s.Delete(job.ID) {
		t.Fatal("expected delete to succeed")
	}

	// File should be removed (no jobs remaining)
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Error("expected store file to be removed after deleting all jobs")
	}
}

func TestLoadRestoresJobs(t *testing.T) {
	storePath, _ := withTestStore(t)

	// Create a scheduler, add a recurring job
	s1 := NewScheduler(nil, storePath)
	s1.Load()
	job1, err := s1.Create("*/5 * * * *", "check CI", true, false)
	if err != nil {
		t.Fatal(err)
	}
	s1.Shutdown()

	// Create a new scheduler and load from the same store
	s2 := NewScheduler(nil, storePath)
	s2.Load()

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

	s := NewScheduler(nil, storePath)
	s.Load()

	jobs := s.List()
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs, got %d", len(jobs))
	}
}

func TestLoadCorruptedFileIsNoop(t *testing.T) {
	storePath, _ := withTestStore(t)

	// Write corrupted JSON
	os.MkdirAll(filepath.Dir(storePath), 0755)
	os.WriteFile(storePath, []byte("not json"), 0644)

	s := NewScheduler(nil, storePath)
	s.Load()

	jobs := s.List()
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs from corrupted file, got %d", len(jobs))
	}
}

func TestMultipleJobsInSession(t *testing.T) {
	storePath, _ := withTestStore(t)

	// Create two jobs in the same session
	s := NewScheduler(nil, storePath)
	s.Load()

	_, err := s.Create("*/5 * * * *", "task A", true, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Create("*/10 * * * *", "task B", true, false)
	if err != nil {
		t.Fatal(err)
	}
	s.Shutdown()

	// Load — should see both jobs
	s2 := NewScheduler(nil, storePath)
	s2.Load()

	jobs := s2.List()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
}

// --- Migration tests ---

func TestMigrateWorkspaceJobs(t *testing.T) {
	tmpDir := t.TempDir()
	oldPath := filepath.Join(tmpDir, "cron-jobs.json")
	newPath := filepath.Join(tmpDir, "cron-jobs", "session-new.json")
	wsDir := "/test/workspace"

	// Write old-format file with this workspace's jobs
	wsKey := workspaceKey(wsDir)
	oldData := oldStoreFile{
		wsKey: {
			Workspace: wsDir,
			Jobs: []jobJSON{
				{ID: "cron-1", CronExpr: "*/5 * * * *", Prompt: "check CI", Recurring: true, CreatedAt: "2025-01-01T00:00:00Z"},
				{ID: "cron-2", CronExpr: "*/10 * * * *", Prompt: "cleanup", Recurring: true, CreatedAt: "2025-01-01T01:00:00Z"},
			},
		},
		"other-key": {
			Workspace: "/other/workspace",
			Jobs:      []jobJSON{{ID: "cron-3", CronExpr: "*/1 * * * *", Prompt: "other", Recurring: true}},
		},
	}
	out, _ := json.MarshalIndent(oldData, "", "  ")
	os.WriteFile(oldPath, out, 0644)

	// Migrate
	MigrateWorkspaceJobs(oldPath, newPath, wsDir)

	// Verify new session file has the migrated jobs
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("expected new session file: %v", err)
	}
	var ss sessionStore
	if err := json.Unmarshal(data, &ss); err != nil {
		t.Fatal(err)
	}
	if len(ss.Jobs) != 2 {
		t.Fatalf("expected 2 migrated jobs, got %d", len(ss.Jobs))
	}

	// Verify old file no longer has this workspace's bucket
	data2, _ := os.ReadFile(oldPath)
	var oldAfter oldStoreFile
	json.Unmarshal(data2, &oldAfter)
	if _, ok := oldAfter[wsKey]; ok {
		t.Error("workspace bucket should have been removed from old file")
	}
	if _, ok := oldAfter["other-key"]; !ok {
		t.Error("other workspace bucket should still exist")
	}
}

func TestMigrateSkipsIfSessionFileExists(t *testing.T) {
	tmpDir := t.TempDir()
	oldPath := filepath.Join(tmpDir, "cron-jobs.json")
	newPath := filepath.Join(tmpDir, "cron-jobs", "session-new.json")
	wsDir := "/test/workspace"

	// Create old file
	wsKey := workspaceKey(wsDir)
	oldData := oldStoreFile{
		wsKey: {Workspace: wsDir, Jobs: []jobJSON{{ID: "cron-1", CronExpr: "*/5 * * * *", Prompt: "check CI", Recurring: true}}},
	}
	out, _ := json.MarshalIndent(oldData, "", "  ")
	os.WriteFile(oldPath, out, 0644)

	// Pre-create the new session file (simulates already-migrated)
	os.MkdirAll(filepath.Dir(newPath), 0755)
	os.WriteFile(newPath, []byte(`{"jobs":[]}`), 0644)

	// Migrate should be a no-op
	MigrateWorkspaceJobs(oldPath, newPath, wsDir)

	// Old file should still have the bucket (migration skipped)
	data, _ := os.ReadFile(oldPath)
	var oldAfter oldStoreFile
	json.Unmarshal(data, &oldAfter)
	if _, ok := oldAfter[wsKey]; !ok {
		t.Error("workspace bucket should still exist (migration was skipped)")
	}
}

func TestMigrateNoOldFile(t *testing.T) {
	tmpDir := t.TempDir()
	oldPath := filepath.Join(tmpDir, "nonexistent.json")
	newPath := filepath.Join(tmpDir, "cron-jobs", "session-new.json")
	wsDir := "/test/workspace"

	// Should be a no-op, no panic
	MigrateWorkspaceJobs(oldPath, newPath, wsDir)

	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Error("expected no new file created")
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
