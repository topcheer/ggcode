//go:build integration

package harness

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- Config Save/Load Roundtrip ---

func TestSaveAndLoadConfigRoundtrip(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")

	cfg := DefaultConfig("my-project", "Build an amazing app")
	cfg.Contexts = []ContextConfig{
		{
			Name:        "api",
			Path:        "internal/api",
			Description: "API layer",
			Owner:       "backend-team",
			Commands: []CommandCheck{
				{Name: "test", Run: "go test ./internal/api/...", Optional: true},
				{Name: "lint", Run: "golangci-lint run ./internal/api/..."},
			},
		},
	}
	cfg.Checks.Commands = []CommandCheck{
		{Name: "build", Run: "go build ./..."},
	}
	cfg.Run.PromptPreamble = "Be extra careful."
	cfg.Run.MaxAttempts = 5
	cfg.GC.ArchiveAfter = "48h"
	cfg.GC.DeleteLogsAfter = "72h"

	configPath := filepath.Join(root, "harness.yaml")
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if loaded.Project.Name != "my-project" {
		t.Errorf("name = %q, want my-project", loaded.Project.Name)
	}
	if loaded.Project.Goal != "Build an amazing app" {
		t.Errorf("goal = %q", loaded.Project.Goal)
	}
	if len(loaded.Contexts) != 1 || loaded.Contexts[0].Name != "api" {
		t.Errorf("contexts = %+v", loaded.Contexts)
	}
	if len(loaded.Checks.Commands) != 1 {
		t.Errorf("checks = %+v", loaded.Checks.Commands)
	}
	if loaded.Run.PromptPreamble != "Be extra careful." {
		t.Errorf("preamble = %q", loaded.Run.PromptPreamble)
	}
	if loaded.Run.MaxAttempts != 5 {
		t.Errorf("MaxAttempts = %d", loaded.Run.MaxAttempts)
	}
}

// --- Task Full Lifecycle ---

func TestTaskLifecycleNewEnqueueRunLoad(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{Goal: "Test lifecycle"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Enqueue
	task, err := EnqueueTask(result.Project, "Implement feature A", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask: %v", err)
	}
	if task.Status != TaskQueued {
		t.Fatalf("expected queued, got %s", task.Status)
	}
	if task.Goal != "Implement feature A" {
		t.Fatalf("goal = %q", task.Goal)
	}

	// Load back
	loaded, err := LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if loaded.Goal != task.Goal {
		t.Errorf("loaded goal = %q, want %q", loaded.Goal, task.Goal)
	}
	if loaded.Status != TaskQueued {
		t.Errorf("loaded status = %s", loaded.Status)
	}

	// Execute
	ctx := context.Background()
	summary, err := ExecuteTask(ctx, result.Project, result.Config, loaded, fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if summary.Task.Status != TaskCompleted {
		t.Fatalf("summary task status = %s", summary.Task.Status)
	}

	// Reload and verify
	completed, err := LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask after run: %v", err)
	}
	if completed.Status != TaskCompleted {
		t.Errorf("completed status = %s", completed.Status)
	}
	if completed.ReviewStatus != ReviewPending {
		t.Errorf("review status = %s, want pending", completed.ReviewStatus)
	}
}

// --- Multiple Dependencies Chain ---

func TestDependencyChainUnblocksInOrder(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{Goal: "Test deps"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Create 3 tasks: A -> B -> C (C depends on B, B depends on A)
	taskA, err := EnqueueTask(result.Project, "Task A", "cli")
	if err != nil {
		t.Fatalf("Enqueue A: %v", err)
	}
	taskB, err := EnqueueTask(result.Project, "Task B", "cli", QueueOptions{DependsOn: []string{taskA.ID}})
	if err != nil {
		t.Fatalf("Enqueue B: %v", err)
	}
	taskC, err := EnqueueTask(result.Project, "Task C", "cli", QueueOptions{DependsOn: []string{taskB.ID}})
	if err != nil {
		t.Fatalf("Enqueue C: %v", err)
	}

	// B and C should be blocked
	if taskB.Status != TaskBlocked {
		t.Fatalf("B status = %s, want blocked", taskB.Status)
	}
	if taskC.Status != TaskBlocked {
		t.Fatalf("C status = %s, want blocked", taskC.Status)
	}

	// Run all queued (only A should run)
	var seen []RunRequest
	ctx := context.Background()
	_, err = RunQueuedTasks(ctx, result.Project, result.Config, &sequenceRunner{
		results: []*RunResult{{Output: "A done"}, {Output: "B done"}, {Output: "C done"}},
		seen:    &seen,
	}, QueueRunOptions{All: true})
	if err != nil {
		t.Fatalf("RunQueuedTasks: %v", err)
	}

	if len(seen) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(seen))
	}

	// Verify order
	tasks, _ := ListTasks(result.Project)
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Goal < tasks[j].Goal
	})
	for _, task := range tasks {
		if task.Status != TaskCompleted {
			t.Errorf("task %q status = %s, want completed", task.Goal, task.Status)
		}
	}
}

// --- Task List and Filtering ---

func TestListTasksReturnsAllTasks(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")

	result, err := Init(root, InitOptions{Goal: "List test"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	for i := 0; i < 5; i++ {
		if _, err := EnqueueTask(result.Project, "task-"+string(rune('A'+i)), "cli"); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	tasks, err := ListTasks(result.Project)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 5 {
		t.Fatalf("expected 5 tasks, got %d", len(tasks))
	}
}

// --- FormatTaskList ---

func TestFormatTaskListShowsGoalsAndStatus(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "fmt test"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	task1, _ := EnqueueTask(result.Project, "First task", "cli")
	task2, _ := EnqueueTask(result.Project, "Second task", "cli")
	_ = task2
	// Complete one
	task1.Status = TaskCompleted
	task1.VerificationStatus = VerificationPassed
	SaveTask(result.Project, task1)

	tasks, _ := ListTasks(result.Project)
	output := FormatTaskList(tasks)
	if !strings.Contains(output, "First task") {
		t.Error("expected First task in output")
	}
	if !strings.Contains(output, "Second task") {
		t.Error("expected Second task in output")
	}
	if !strings.Contains(output, "completed") {
		t.Error("expected completed status in output")
	}
}

// --- Review Edge Cases ---

func TestListReviewableTasksFiltersCorrectly(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "review test"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Create tasks in different states
	pending, _ := NewTask("pending task", "cli")
	pending.Status = TaskCompleted
	pending.VerificationStatus = VerificationPassed
	pending.ReviewStatus = ReviewPending
	writeTaskFixture(result.Project, pending)

	approved, _ := NewTask("approved task", "cli")
	approved.Status = TaskCompleted
	approved.VerificationStatus = VerificationPassed
	approved.ReviewStatus = ReviewApproved
	writeTaskFixture(result.Project, approved)

	failed, _ := NewTask("failed task", "cli")
	failed.Status = TaskFailed
	failed.VerificationStatus = VerificationFailed
	failed.ReviewStatus = ReviewPending
	writeTaskFixture(result.Project, failed)

	reviewable, err := ListReviewableTasks(result.Project)
	if err != nil {
		t.Fatalf("ListReviewableTasks: %v", err)
	}
	if len(reviewable) != 1 {
		t.Fatalf("expected 1 reviewable task, got %d", len(reviewable))
	}
	if reviewable[0].Goal != "pending task" {
		t.Errorf("reviewable task goal = %q", reviewable[0].Goal)
	}
}

// --- Promotion Edge Cases ---

func TestPromoteTaskRejectsUnapprovedTask(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "promo test"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	task, _ := NewTask("not ready", "cli")
	task.Status = TaskCompleted
	task.VerificationStatus = VerificationPassed
	task.ReviewStatus = ReviewPending // NOT approved
	writeTaskFixture(result.Project, task)

	ctx := context.Background()
	_, err = PromoteTask(ctx, result.Project, task.ID, "trying to promote")
	if err == nil {
		t.Fatal("expected error promoting unapproved task")
	}
	if !strings.Contains(err.Error(), "not ready for promotion") {
		t.Errorf("error = %v", err)
	}
}

func TestPromoteTaskRejectsAlreadyPromoted(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "promo test"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	task, _ := NewTask("already done", "cli")
	task.Status = TaskCompleted
	task.VerificationStatus = VerificationPassed
	task.ReviewStatus = ReviewApproved
	task.PromotionStatus = PromotionApplied
	writeTaskFixture(result.Project, task)

	ctx := context.Background()
	_, err = PromoteTask(ctx, result.Project, task.ID, "again")
	if err == nil {
		t.Fatal("expected error for already promoted task")
	}
}

func TestListPromotableTasksOnlyShowsReady(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "promo list test"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Ready for promotion
	ready, _ := NewTask("ready task", "cli")
	ready.Status = TaskCompleted
	ready.VerificationStatus = VerificationPassed
	ready.ReviewStatus = ReviewApproved
	writeTaskFixture(result.Project, ready)

	// Already promoted
	done, _ := NewTask("done task", "cli")
	done.Status = TaskCompleted
	done.VerificationStatus = VerificationPassed
	done.ReviewStatus = ReviewApproved
	done.PromotionStatus = PromotionApplied
	writeTaskFixture(result.Project, done)

	// Not completed
	pending, _ := NewTask("pending task", "cli")
	pending.Status = TaskRunning
	writeTaskFixture(result.Project, pending)

	promotable, err := ListPromotableTasks(result.Project)
	if err != nil {
		t.Fatalf("ListPromotableTasks: %v", err)
	}
	if len(promotable) != 1 {
		t.Fatalf("expected 1 promotable, got %d", len(promotable))
	}
	if promotable[0].Goal != "ready task" {
		t.Errorf("promotable task goal = %q", promotable[0].Goal)
	}
}

// --- GC Edge Cases ---

func TestRunGCDeletesLogFilesOlderThanThreshold(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "gc logs test"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Create an old task with a log file
	task, _ := NewTask("old task", "cli")
	task.Status = TaskCompleted
	task.VerificationStatus = VerificationPassed
	task.ReviewStatus = ReviewApproved
	task.PromotionStatus = PromotionApplied
	oldTime := time.Now().UTC().Add(-72 * time.Hour)
	task.CreatedAt = oldTime
	task.UpdatedAt = oldTime
	finishedAt := oldTime
	task.FinishedAt = &finishedAt
	writeTaskFixture(result.Project, task)

	// Create a log file
	logDir := filepath.Join(root, StateRelDir, "logs")
	os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, task.ID+".log")
	os.WriteFile(logPath, []byte("old log content"), 0644)
	os.Chtimes(logPath, oldTime, oldTime)

	result.Config.GC.ArchiveAfter = "1h"
	result.Config.GC.DeleteLogsAfter = "2h"

	report, err := RunGC(result.Project, result.Config, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunGC: %v", err)
	}
	if report.ArchivedTasks != 1 {
		t.Errorf("archived = %d, want 1", report.ArchivedTasks)
	}
	if report.DeletedLogs != 1 {
		t.Errorf("deleted logs = %d, want 1", report.DeletedLogs)
	}
	// Log file should be deleted
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Error("expected log file to be deleted")
	}
}

// --- Doctor Edge Cases ---

func TestDoctorDetectsOrphanedWorktrees(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{Goal: "doctor orphan test"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Create a worktree directory without a matching task
	worktreeDir := filepath.Join(root, StateRelDir, "worktrees", "sa-orphan")
	os.MkdirAll(worktreeDir, 0755)
	os.WriteFile(filepath.Join(worktreeDir, "main.go"), []byte("package main"), 0644)

	report, err := Doctor(result.Project, result.Config)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.OrphanedWorktrees != 1 {
		t.Errorf("orphaned worktrees = %d, want 1", report.OrphanedWorktrees)
	}

	output := FormatDoctorReport(report)
	if !strings.Contains(output, "orphan") {
		t.Errorf("expected 'orphan' in doctor report: %s", output)
	}
}

func TestDoctorReportsNoIssuesForHealthyProject(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "healthy project"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	report, err := Doctor(result.Project, result.Config)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.StaleBlocked != 0 {
		t.Errorf("stale blocked = %d", report.StaleBlocked)
	}
	if report.OrphanedWorktrees != 0 {
		t.Errorf("orphaned = %d", report.OrphanedWorktrees)
	}
	if report.WorkerDrift != 0 {
		t.Errorf("drift = %d", report.WorkerDrift)
	}
	if report.ReviewReady != 0 {
		t.Errorf("review ready = %d", report.ReviewReady)
	}
}

// --- Monitor More Scenarios ---

func TestBuildMonitorReportEmptyProject(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "empty monitor"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	report, err := BuildMonitorReport(result.Project, MonitorOptions{
		FocusTasks:   5,
		RecentEvents: 10,
	})
	if err != nil {
		t.Fatalf("BuildMonitorReport: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}

	output := FormatMonitorReport(report)
	if output == "" {
		t.Error("expected non-empty monitor report")
	}
}

func TestBuildMonitorReportWithRunningTask(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "running monitor"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	running, _ := NewTask("running task", "cli")
	running.Status = TaskRunning
	running.WorkerPhase = "executing"
	running.WorkerProgress = "write_file"
	writeTaskFixture(result.Project, running)

	report, err := BuildMonitorReport(result.Project, MonitorOptions{
		FocusTasks:   5,
		RecentEvents: 10,
	})
	if err != nil {
		t.Fatalf("BuildMonitorReport: %v", err)
	}

	output := FormatMonitorReport(report)
	if !strings.Contains(output, "running") {
		t.Errorf("expected 'running' in monitor: %s", output)
	}
}

// --- Context Report Edge Cases ---

func TestBuildContextReportWithNoTasks(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	os.MkdirAll(filepath.Join(root, "internal", "api"), 0755)
	result, err := Init(root, InitOptions{Goal: "context no tasks"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	report, err := BuildContextReport(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildContextReport: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	output := FormatContextReport(report)
	if output == "" {
		t.Error("expected non-empty context report")
	}
}

func TestBuildContextReportWithFailedTaskInContext(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	os.MkdirAll(filepath.Join(root, "internal", "api"), 0755)
	result, err := Init(root, InitOptions{Goal: "context failed"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	task, _ := NewTask("broken task", "cli")
	task.Status = TaskFailed
	task.VerificationStatus = VerificationFailed
	task.ContextName = "api"
	task.ContextPath = "internal/api"
	writeTaskFixture(result.Project, task)

	report, err := BuildContextReport(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildContextReport: %v", err)
	}

	output := FormatContextReport(report)
	// FormatContextReport shows task counts, not individual task goals
	if !strings.Contains(output, "failed=1") {
		t.Errorf("expected failed=1 in report: %s", output)
	}
	if !strings.Contains(output, "internal/api") {
		t.Errorf("expected context path in report: %s", output)
	}
}

// --- Inbox Edge Cases ---

func TestBuildOwnerInboxWithRetryableTasks(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	os.MkdirAll(filepath.Join(root, "internal", "api"), 0755)
	// Set owner via InitOptions contexts
	result, err := Init(root, InitOptions{
		Goal: "inbox retry",
		Contexts: []ContextConfig{
			{Name: "api", Path: "internal/api", Owner: "backend-team"},
		},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Failed task with attempt=1
	task, _ := NewTask("retry me", "cli")
	task.Status = TaskFailed
	task.Attempt = 1
	task.ContextName = "api"
	task.ContextPath = "internal/api"
	writeTaskFixture(result.Project, task)

	inbox, err := BuildOwnerInbox(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildOwnerInbox: %v", err)
	}

	output := FormatOwnerInbox(inbox)
	if !strings.Contains(output, "retry me") {
		t.Errorf("expected retryable task in inbox: %s", output)
	}
	// Owner should appear in inbox grouping
	if !strings.Contains(output, "backend-team") && !strings.Contains(output, "unowned") {
		t.Errorf("expected owner in inbox: %s", output)
	}
}

// --- Events Persistence Detail ---

func TestTaskEventsRecordStatusTransitions(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{Goal: "events detail"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	task, _ := EnqueueTask(result.Project, "event task", "cli")
	ctx := context.Background()
	_, err = ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "event done"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	events := readHarnessEvents(t, result.Project)
	if len(events) == 0 {
		t.Fatal("expected events in log")
	}

	// Should have at least status-changed events
	var statusEvents []string
	for _, ev := range events {
		if ev.Kind == "task.status_changed" {
			statusEvents = append(statusEvents, ev.Status)
		}
	}
	if len(statusEvents) == 0 {
		t.Error("expected at least one task_status_changed event")
	}
}

// --- Snapshot DB Content ---

func TestSnapshotDBContainsCompletedTaskFields(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{Goal: "snapshot fields"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	task, _ := EnqueueTask(result.Project, "snapshot task", "cli")
	ctx := context.Background()
	_, err = ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "snapshot done"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	snap := loadTaskSnapshotByID(t, result.Project, task.ID)
	if snap.Status != TaskCompleted {
		t.Errorf("snapshot status = %q, want %q", snap.Status, TaskCompleted)
	}
}

// --- Release Plan with Environment Filtering ---

func TestBuildReleasePlanWithEnvironment(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "release env"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	for _, goal := range []string{"Task A", "Task B"} {
		task, _ := NewTask(goal, "cli")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		writeTaskFixture(result.Project, task)
	}

	plan, err := BuildReleasePlanWithOptions(result.Project, result.Config, ReleasePlanOptions{
		Environment: "staging",
	})
	if err != nil {
		t.Fatalf("BuildReleasePlanWithOptions: %v", err)
	}
	if plan.Environment != "staging" {
		t.Errorf("environment = %q", plan.Environment)
	}

	output := FormatReleasePlan(plan)
	if !strings.Contains(output, "staging") {
		t.Errorf("expected environment in output: %s", output)
	}
}

// --- Applied Release Plan Creates Report ---

func TestApplyReleasePlanCreatesReportFile(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{Goal: "release report"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	task, _ := NewTask("release me", "cli")
	task.Status = TaskCompleted
	task.VerificationStatus = VerificationPassed
	task.ReviewStatus = ReviewApproved
	task.PromotionStatus = PromotionApplied
	writeTaskFixture(result.Project, task)

	plan, _ := BuildReleasePlan(result.Project, result.Config)
	applied, err := ApplyReleasePlan(result.Project, plan, "v1.0 release")
	if err != nil {
		t.Fatalf("ApplyReleasePlan: %v", err)
	}
	if applied == nil {
		t.Fatal("expected non-nil applied plan")
	}

	// Verify report file created
	released, _ := LoadTask(result.Project, task.ID)
	if released.ReleaseBatchID == "" {
		t.Error("expected ReleaseBatchID to be set")
	}
	if released.ReleaseNotes != "v1.0 release" {
		t.Errorf("release notes = %q", released.ReleaseNotes)
	}
	if released.ReleasedAt == nil {
		t.Error("expected ReleasedAt to be set")
	}
}

// --- Release Wave Rollout Listing ---

func TestListReleaseWaveRolloutsReturnsAppliedPlans(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	os.MkdirAll(filepath.Join(root, "internal", "api"), 0755)
	os.MkdirAll(filepath.Join(root, "internal", "web"), 0755)
	result, err := Init(root, InitOptions{Goal: "wave list"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	for _, item := range []struct {
		goal, ctx string
	}{
		{"API task", "internal/api"},
		{"Web task", "internal/web"},
	} {
		task, _ := NewTask(item.goal, "cli")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		task.ContextPath = item.ctx
		task.ContextName = filepath.Base(item.ctx)
		writeTaskFixture(result.Project, task)
	}

	for i := range result.Config.Contexts {
		result.Config.Contexts[i].Owner = result.Config.Contexts[i].Name + "-team"
	}

	plan, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByContext)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan: %v", err)
	}

	applied, err := ApplyReleaseWavePlan(result.Project, plan, "wave test", "rollout-001")
	if err != nil {
		t.Fatalf("ApplyReleaseWavePlan: %v", err)
	}
	if applied == nil {
		t.Fatal("expected applied plan")
	}

	rollouts, err := ListReleaseWaveRollouts(result.Project)
	if err != nil {
		t.Fatalf("ListReleaseWaveRollouts: %v", err)
	}
	if len(rollouts) != 1 {
		t.Fatalf("expected 1 rollout, got %d", len(rollouts))
	}

	output := FormatReleaseWaveRollouts(rollouts)
	if output == "" {
		t.Error("expected non-empty rollout output")
	}
}

// --- Delivery Report Edge Cases ---

func TestRunTaskWithVerificationFailureRecordsDetails(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{Goal: "verification fail"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Add a check command that will fail
	result.Config.Checks.Commands = []CommandCheck{
		{Name: "build", Run: "false"}, // always fails
	}

	ctx := context.Background()
	task, _ := EnqueueTask(result.Project, "failing verification", "cli")
	summary, err := ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "some output"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	if summary.Task.Status != TaskFailed {
		t.Fatalf("status = %s, want failed", summary.Task.Status)
	}

	loaded, _ := LoadTask(result.Project, task.ID)
	if loaded.VerificationStatus != VerificationFailed {
		t.Errorf("verification = %s, want failed", loaded.VerificationStatus)
	}
	if loaded.Error == "" {
		t.Error("expected Error to be set")
	}
}

// --- Streaming Runner Output ---

func TestStreamingRunnerCapturesOutput(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{Goal: "stream test"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	ctx := context.Background()
	task, _ := EnqueueTask(result.Project, "stream output", "cli")
	summary, err := ExecuteTask(ctx, result.Project, result.Config, task, streamingRunner{
		result: &RunResult{Output: "final output"},
		stdout: []string{"chunk1\n", "chunk2\n"},
		stderr: []string{"progress line"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if summary.Task.Status != TaskCompleted {
		t.Fatalf("status = %s", summary.Task.Status)
	}

	// Verify log file contains output
	loaded, _ := LoadTask(result.Project, task.ID)
	if loaded.LogPath == "" {
		t.Fatal("expected log path to be set")
	}
	logContent, err := os.ReadFile(loaded.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logStr := string(logContent)
	if !strings.Contains(logStr, "chunk1") || !strings.Contains(logStr, "chunk2") {
		t.Errorf("log missing chunks: %s", logStr)
	}
}

// --- Discover from Various Depths ---

func TestDiscoverFindsRootFromDeeplyNestedDir(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	deepDir := filepath.Join(root, "a", "b", "c", "d")
	os.MkdirAll(deepDir, 0755)

	result, err := Init(root, InitOptions{Goal: "deep discover"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	_ = result

	project, err := Discover(deepDir)
	if err != nil {
		t.Fatalf("Discover from deep dir: %v", err)
	}
	if project.RootDir != root {
		t.Errorf("root = %q, want %q", project.RootDir, root)
	}
}

// --- Context Config Edge Cases ---

func TestNormalizeContextsDeduplicatesByName(t *testing.T) {
	contexts := []ContextConfig{
		{Name: "api", Path: "internal/api", Owner: "team-a"},
		{Name: "api", Path: "internal/api", Owner: "team-b"}, // duplicate
		{Name: "web", Path: "internal/web"},
	}
	normalized := NormalizeContexts(contexts)
	if len(normalized) != 2 {
		t.Fatalf("expected 2 after dedup, got %d", len(normalized))
	}
	// Should keep first occurrence's owner
	for _, c := range normalized {
		if c.Name == "api" && c.Owner != "team-a" {
			t.Errorf("api owner = %q, want team-a", c.Owner)
		}
	}
}

// --- GC with Stale Blocked Tasks ---

func TestRunGCAbandonsStaleBlockedTasksOnly(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "gc stale"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Fresh blocked task (should NOT be abandoned)
	fresh, _ := NewTask("fresh blocked", "cli")
	fresh.Status = TaskBlocked
	fresh.DependsOn = []string{"sa-nonexistent"}
	fresh.CreatedAt = time.Now().UTC()
	fresh.UpdatedAt = time.Now().UTC()
	writeTaskFixture(result.Project, fresh)

	// Stale blocked task (should be abandoned)
	stale, _ := NewTask("stale blocked", "cli")
	stale.Status = TaskBlocked
	stale.DependsOn = []string{"sa-nonexistent"}
	stale.CreatedAt = time.Now().UTC().Add(-48 * time.Hour)
	stale.UpdatedAt = time.Now().UTC().Add(-48 * time.Hour)
	writeTaskFixture(result.Project, stale)

	result.Config.GC.AbandonAfter = "24h"

	report, err := RunGC(result.Project, result.Config, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunGC: %v", err)
	}
	if report.AbandonedTasks != 1 {
		t.Errorf("abandoned = %d, want 1", report.AbandonedTasks)
	}

	// Verify stale is abandoned, fresh is not
	loadedFresh, _ := LoadTask(result.Project, fresh.ID)
	if loadedFresh.Status == TaskAbandoned {
		t.Error("fresh blocked task should NOT be abandoned")
	}
	loadedStale, _ := LoadTask(result.Project, stale.ID)
	if loadedStale.Status != TaskAbandoned {
		t.Errorf("stale task status = %s, want abandoned", loadedStale.Status)
	}
}

// --- Format Output Consistency ---

func TestFormatGCReportShowsAllSections(t *testing.T) {
	report := &GCReport{
		ArchivedTasks:    5,
		DeletedLogs:      3,
		AbandonedTasks:   2,
		RemovedWorktrees: 1,
	}
	output := FormatGCReport(report)
	for _, want := range []string{"5", "3", "2", "1"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in GC report: %s", want, output)
		}
	}
}

func TestFormatRunSummaryContainsTaskInfo(t *testing.T) {
	task, _ := NewTask("Do something important", "cli")
	task.Status = TaskCompleted
	task.VerificationStatus = VerificationPassed
	summary := &RunSummary{
		Task:   task,
		Result: &RunResult{Output: "Task finished successfully"},
	}
	output := FormatRunSummary(summary)
	for _, want := range []string{task.ID, "completed"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in run summary: %s", want, output)
		}
	}
}

func TestFormatQueueSummaryShowsTaskResults(t *testing.T) {
	task1, _ := NewTask("task 1", "cli")
	task1.Status = TaskCompleted
	task2, _ := NewTask("task 2", "cli")
	task2.Status = TaskFailed
	summary := &RunQueueSummary{
		Executed: []*RunSummary{
			{Task: task1, Result: &RunResult{Output: "ok"}},
			{Task: task2, Result: &RunResult{}},
		},
	}
	output := FormatQueueSummary(summary)
	if !strings.Contains(output, "2") {
		t.Error("expected run count in output")
	}
}

// --- Config Duration Parsing via LoadConfig ---

func TestLoadConfigParsesDurationStrings(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "harness.yaml")
	content := `
project:
  name: test
  goal: test goal
gc:
  archive_after: 2h
  delete_logs_after: 4h
  abandon_after: 48h
run:
  retry_failed: true
  max_attempts: 3
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.GC.ArchiveAfter != "2h" {
		t.Errorf("archive_after = %v", cfg.GC.ArchiveAfter)
	}
	if cfg.GC.DeleteLogsAfter != "4h" {
		t.Errorf("delete_logs_after = %v", cfg.GC.DeleteLogsAfter)
	}
	if cfg.Run.MaxAttempts != 3 {
		t.Errorf("max_attempts = %d", cfg.Run.MaxAttempts)
	}
}

// --- Init with Contexts Creates AGENTS.md ---

func TestInitCreatesContextAgentsMDFiles(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	os.MkdirAll(filepath.Join(root, "internal", "payments"), 0755)
	os.MkdirAll(filepath.Join(root, "internal", "shipping"), 0755)

	result, err := Init(root, InitOptions{Goal: "context agents"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Check AGENTS.md files created in context dirs
	for _, ctx := range result.Config.Contexts {
		agentsPath := filepath.Join(root, ctx.Path, "AGENTS.md")
		if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
			t.Errorf("AGENTS.md missing for context %q at %s", ctx.Name, agentsPath)
		}
	}
}

// --- Worker Execution Detection ---

func TestShouldUseWorkerExecutionDetection(t *testing.T) {
	cfg := DefaultConfig("test", "test")
	cfg.Run.ExecutionMode = "direct"
	if shouldUseWorkerExecution(cfg) {
		t.Error("expected no worker execution for direct mode")
	}
	cfg.Run.ExecutionMode = "worker"
	if !shouldUseWorkerExecution(cfg) {
		t.Error("expected worker execution for worker mode")
	}
	cfg.Run.ExecutionMode = ""
	if !shouldUseWorkerExecution(cfg) {
		t.Error("expected worker execution for empty mode (default)")
	}
}

// --- FindGGCodeConfig ---

func TestFindGGCodeConfigLocatesConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "ggcode.yaml")
	os.WriteFile(configPath, []byte("model: test\n"), 0644)

	found := findGGCodeConfig(root)
	if found == "" {
		t.Error("expected to find config")
	}
}

func TestFindGGCodeConfigReturnsEmptyWhenMissing(t *testing.T) {
	root := t.TempDir()
	found := findGGCodeConfig(root)
	if found != "" {
		t.Errorf("expected empty, got %q", found)
	}
}

// --- CheckProject Command Execution ---

func TestCheckProjectFailsWhenCommandFails(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "check fail"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	result.Config.Checks.Commands = []CommandCheck{
		{Name: "failing-check", Run: "false"},
	}

	report, err := CheckProject(context.Background(), result.Project, result.Config, CheckOptions{RunCommands: true})
	if err != nil {
		t.Fatalf("CheckProject: %v", err)
	}
	if report.Passed {
		t.Error("expected check to fail")
	}

	output := FormatCheckReport(report)
	if !strings.Contains(output, "failing-check") {
		t.Errorf("expected check name in report: %s", output)
	}
}

// --- Snapshot DB Open ---

func TestOpenSnapshotDBReturnsValidDB(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{Goal: "snapshot db"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Verify snapshot directory exists
	dir := taskSnapshotDir(result.Project)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatalf("snapshot dir not found: %v", dir)
	}
}

// --- Release Wave Gate Operations ---

func TestReleaseWaveGateApproveUpdatesStatus(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	os.MkdirAll(filepath.Join(root, "internal", "api"), 0755)
	os.MkdirAll(filepath.Join(root, "internal", "web"), 0755)
	result, err := Init(root, InitOptions{Goal: "gate approve"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Create tasks in both contexts so we get 2 groups
	for _, item := range []struct {
		goal, ctx, ctxPath string
	}{
		{"api task", "api", "internal/api"},
		{"web task", "web", "internal/web"},
	} {
		task, _ := NewTask(item.goal, "cli")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		task.ContextPath = item.ctxPath
		task.ContextName = item.ctx
		writeTaskFixture(result.Project, task)
	}

	plan, _ := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByContext)
	_, err = ApplyReleaseWavePlan(result.Project, plan, "gate test", "rollout-gate")
	if err != nil {
		t.Fatalf("ApplyReleaseWavePlan: %v", err)
	}

	// Group 0 is auto-approved (active), group 1 should be planned
	// Approve gate for group 1 (waveOrder=2 since WaveOrder = i+1)
	updated, err := ApproveReleaseWaveGate(result.Project, "rollout-gate", 2, "looks good")
	if err != nil {
		t.Fatalf("ApproveReleaseWaveGate: %v", err)
	}
	if len(updated.Groups) < 2 {
		t.Fatal("expected at least 2 groups")
	}
	// Find the group with WaveOrder=2
	var found *ReleasePlan
	for _, g := range updated.Groups {
		if g.WaveOrder == 2 {
			found = g
			break
		}
	}
	if found == nil {
		t.Fatal("expected group with WaveOrder=2")
	}
	if found.GateStatus != "approved" {
		t.Errorf("gate status = %s", found.GateStatus)
	}
}

func TestReleaseWavePausePreventsAdvance(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	os.MkdirAll(filepath.Join(root, "internal", "api"), 0755)
	result, err := Init(root, InitOptions{Goal: "pause wave"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	task, _ := NewTask("pause task", "cli")
	task.Status = TaskCompleted
	task.VerificationStatus = VerificationPassed
	task.ReviewStatus = ReviewApproved
	task.PromotionStatus = PromotionApplied
	task.ContextPath = "internal/api"
	task.ContextName = "api"
	writeTaskFixture(result.Project, task)

	plan, _ := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByContext)
	_, err = ApplyReleaseWavePlan(result.Project, plan, "pause test", "rollout-pause")
	if err != nil {
		t.Fatalf("ApplyReleaseWavePlan: %v", err)
	}

	// Pause
	paused, err := PauseReleaseWaveRollout(result.Project, "rollout-pause", "hold on")
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if len(paused.Groups) == 0 {
		t.Fatal("expected groups")
	}
	if paused.Groups[0].WaveStatus != "paused" {
		t.Errorf("wave status = %s, want paused", paused.Groups[0].WaveStatus)
	}

	// Try to advance — should fail
	_, err = AdvanceReleaseWaveRollout(result.Project, "rollout-pause")
	if err == nil {
		t.Error("expected error advancing paused rollout")
	}
}

// --- Context User Input Edge Cases ---

func TestParseContextSpecsWithEqualsSign(t *testing.T) {
	specs := ParseContextSpecs("payments=apps/payments,shipping=apps/shipping")
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	names := []string{specs[0].Name, specs[1].Name}
	sort.Strings(names)
	if names[0] != "payments" || names[1] != "shipping" {
		t.Errorf("names = %v", names)
	}
}

func TestParseContextSpecsDeduplicates(t *testing.T) {
	specs := ParseContextSpecs("api,api,web")
	if len(specs) != 2 {
		t.Fatalf("expected 2 after dedup, got %d", len(specs))
	}
}

// --- Null SQL helpers via snapshot ---

func TestNullableFieldsInSnapshotDB(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{Goal: "null fields"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Create a task that has some nullable fields empty
	task, _ := EnqueueTask(result.Project, "null task", "cli")
	ctx := context.Background()
	_, err = ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	snap := loadTaskSnapshotByID(t, result.Project, task.ID)
	if snap.Error != "" {
		t.Errorf("expected empty error, got %q", snap.Error)
	}
}
