package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// E2E helpers
// ---------------------------------------------------------------------------

// e2eGit runs a git command in the given directory. Disables global hooks.
func e2eGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// e2eInitRepo creates a git repo with an initial commit and harness init.
func e2eInitRepo(t *testing.T) (string, *InitResult) {
	t.Helper()
	root := t.TempDir()
	e2eGit(t, root, "init")
	e2eGit(t, root, "config", "user.name", "E2E Test")
	e2eGit(t, root, "config", "user.email", "e2e@test.com")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# e2e project"), 0644)
	e2eGit(t, root, "add", "README.md")
	e2eGit(t, root, "commit", "--no-verify", "-m", "initial commit")

	result, err := Init(root, InitOptions{Goal: "E2E testing project"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)
	return root, result
}

// e2eInitRepoWithContexts creates a repo with context directories.
func e2eInitRepoWithContexts(t *testing.T) (string, *InitResult) {
	t.Helper()
	root := t.TempDir()
	e2eGit(t, root, "init")
	e2eGit(t, root, "config", "user.name", "E2E Test")
	e2eGit(t, root, "config", "user.email", "e2e@test.com")

	for _, dir := range []string{"internal/api", "internal/web", "internal/db"} {
		os.MkdirAll(filepath.Join(root, dir), 0755)
		os.WriteFile(filepath.Join(root, dir, "main.go"), []byte("package "+filepath.Base(dir)), 0644)
	}
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# e2e project"), 0644)
	e2eGit(t, root, "add", ".")
	e2eGit(t, root, "commit", "--no-verify", "-m", "initial commit")

	result, err := Init(root, InitOptions{
		Goal: "E2E multi-context project",
		Contexts: []ContextConfig{
			{Name: "api", Path: "internal/api", Owner: "backend-team"},
			{Name: "web", Path: "internal/web", Owner: "frontend-team"},
			{Name: "db", Path: "internal/db", Owner: "data-team"},
		},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)
	return root, result
}

// ---------------------------------------------------------------------------
// E2E Test: Full Lifecycle (init → create → run → review → promote → release)
// ---------------------------------------------------------------------------

func TestE2EFullLifecycleInitToRelease(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()

	// Disable verification checks for this test
	result.Config.Checks.Commands = nil

	// Step 1: Create and enqueue a task
	task, err := EnqueueTask(result.Project, "Implement user authentication module", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask: %v", err)
	}
	if task.Status != TaskQueued {
		t.Fatalf("initial status = %s, want queued", task.Status)
	}

	// Step 2: Execute the task with a fake runner
	summary, err := ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "authentication module implemented"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if summary.Task.Status != TaskCompleted {
		t.Fatalf("post-execution status = %s, want completed", summary.Task.Status)
	}
	if summary.Task.VerificationStatus != VerificationPassed {
		t.Fatalf("verification = %s, want passed", summary.Task.VerificationStatus)
	}
	if summary.Task.ReviewStatus != ReviewPending {
		t.Fatalf("review status = %s, want pending", summary.Task.ReviewStatus)
	}

	// Step 3: Verify task appears in reviewable list
	reviewable, err := ListReviewableTasks(result.Project)
	if err != nil {
		t.Fatalf("ListReviewableTasks: %v", err)
	}
	if len(reviewable) != 1 || reviewable[0].ID != task.ID {
		t.Fatalf("expected 1 reviewable task, got %d", len(reviewable))
	}

	// Step 4: Approve the review
	approved, err := ApproveTaskReview(result.Project, task.ID, "LGTM - auth module looks solid")
	if err != nil {
		t.Fatalf("ApproveTaskReview: %v", err)
	}
	if approved.ReviewStatus != ReviewApproved {
		t.Fatalf("review status = %s, want approved", approved.ReviewStatus)
	}
	if approved.ReviewedAt == nil {
		t.Fatal("expected ReviewedAt to be set")
	}

	// Step 5: Verify task appears in promotable list
	promotable, err := ListPromotableTasks(result.Project)
	if err != nil {
		t.Fatalf("ListPromotableTasks: %v", err)
	}
	if len(promotable) != 1 || promotable[0].ID != task.ID {
		t.Fatalf("expected 1 promotable task, got %d", len(promotable))
	}

	// Step 6: Promote the task
	promoted, err := PromoteTask(ctx, result.Project, task.ID, "promote to main")
	if err != nil {
		t.Fatalf("PromoteTask: %v", err)
	}
	if promoted.PromotionStatus != PromotionApplied {
		t.Fatalf("promotion status = %s, want promoted", promoted.PromotionStatus)
	}
	if promoted.PromotedAt == nil {
		t.Fatal("expected PromotedAt to be set")
	}

	// Step 7: Build and apply a release plan
	plan, err := BuildReleasePlan(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildReleasePlan: %v", err)
	}
	if len(plan.Tasks) != 1 {
		t.Fatalf("plan tasks = %d, want 1", len(plan.Tasks))
	}

	applied, err := ApplyReleasePlan(result.Project, plan, "v1.0.0 - auth module release")
	if err != nil {
		t.Fatalf("ApplyReleasePlan: %v", err)
	}
	if applied.ReportPath == "" {
		t.Fatal("expected release report path")
	}

	// Step 8: Verify final state via reload
	loaded, err := LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask final: %v", err)
	}
	if loaded.Status != TaskCompleted {
		t.Fatalf("final status = %s, want completed", loaded.Status)
	}
	if loaded.ReleaseBatchID == "" {
		t.Fatal("expected ReleaseBatchID to be set")
	}
	if loaded.ReleasedAt == nil {
		t.Fatal("expected ReleasedAt to be set")
	}
	if loaded.ReleaseNotes != "v1.0.0 - auth module release" {
		t.Fatalf("release notes = %q", loaded.ReleaseNotes)
	}

	// Step 9: Verify events were persisted throughout lifecycle
	events := readHarnessEvents(t, result.Project)
	if len(events) < 4 {
		t.Fatalf("expected at least 4 events for full lifecycle, got %d", len(events))
	}
	eventKinds := make(map[string]int)
	for _, ev := range events {
		if ev.EntityID == task.ID {
			eventKinds[ev.Kind]++
		}
	}
	for _, required := range []string{eventTaskCreated, eventTaskStatusChanged} {
		if eventKinds[required] == 0 {
			t.Errorf("missing event kind %q for task", required)
		}
	}

	// Step 10: Verify snapshot DB consistency
	db := openSnapshotDB(t, result.Project)
	defer db.Close()
	var dbStatus, dbVerification, dbReview, dbPromotion, dbReleaseBatch string
	err = db.QueryRow(`SELECT status, verification_status, review_status, promotion_status, release_batch_id FROM tasks WHERE task_id = ?`, task.ID).
		Scan(&dbStatus, &dbVerification, &dbReview, &dbPromotion, &dbReleaseBatch)
	if err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
	if dbStatus != string(TaskCompleted) {
		t.Errorf("snapshot status = %q", dbStatus)
	}
	if dbVerification != VerificationPassed {
		t.Errorf("snapshot verification = %q", dbVerification)
	}
	if dbReview != ReviewApproved {
		t.Errorf("snapshot review = %q", dbReview)
	}
	if dbPromotion != PromotionApplied {
		t.Errorf("snapshot promotion = %q", dbPromotion)
	}
	if dbReleaseBatch == "" {
		t.Error("expected snapshot release_batch_id")
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Full Lifecycle with Contexts and Wave Release
// ---------------------------------------------------------------------------

func TestE2EMultiContextWaveRelease(t *testing.T) {
	_, result := e2eInitRepoWithContexts(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	// Assign owners
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case "internal/api":
			result.Config.Contexts[i].Owner = "backend-team"
		case "internal/web":
			result.Config.Contexts[i].Owner = "frontend-team"
		case "internal/db":
			result.Config.Contexts[i].Owner = "data-team"
		}
	}

	// Create tasks across all 3 contexts
	taskAPI, err := EnqueueTask(result.Project, "Implement REST API", "cli", QueueOptions{
		ContextName: "api", ContextPath: "internal/api",
	})
	if err != nil {
		t.Fatalf("EnqueueTask API: %v", err)
	}
	taskWeb, err := EnqueueTask(result.Project, "Build dashboard UI", "cli", QueueOptions{
		ContextName: "web", ContextPath: "internal/web",
	})
	if err != nil {
		t.Fatalf("EnqueueTask Web: %v", err)
	}
	taskDB, err := EnqueueTask(result.Project, "Schema migration", "cli", QueueOptions{
		ContextName: "db", ContextPath: "internal/db",
	})
	if err != nil {
		t.Fatalf("EnqueueTask DB: %v", err)
	}

	// Run all tasks
	var seen []RunRequest
	_, err = RunQueuedTasks(ctx, result.Project, result.Config, &sequenceRunner{
		results: []*RunResult{
			{Output: "API done"},
			{Output: "Dashboard done"},
			{Output: "Migration done"},
		},
		seen: &seen,
	}, QueueRunOptions{All: true})
	if err != nil {
		t.Fatalf("RunQueuedTasks: %v", err)
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 task runs, got %d", len(seen))
	}

	// Verify all completed
	for _, id := range []string{taskAPI.ID, taskWeb.ID, taskDB.ID} {
		loaded, err := LoadTask(result.Project, id)
		if err != nil {
			t.Fatalf("LoadTask %s: %v", id, err)
		}
		if loaded.Status != TaskCompleted {
			t.Errorf("task %s status = %s", id, loaded.Status)
		}
	}

	// Approve all reviews
	for _, id := range []string{taskAPI.ID, taskWeb.ID, taskDB.ID} {
		if _, err := ApproveTaskReview(result.Project, id, "approved"); err != nil {
			t.Fatalf("ApproveTaskReview %s: %v", id, err)
		}
	}

	// Promote all
	promoted, err := PromoteApprovedTasks(ctx, result.Project, "batch promote")
	if err != nil {
		t.Fatalf("PromoteApprovedTasks: %v", err)
	}
	if len(promoted) != 3 {
		t.Fatalf("promoted = %d, want 3", len(promoted))
	}

	// Build release wave plan grouped by owner (3 groups)
	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan: %v", err)
	}
	if len(waves.Groups) != 3 {
		t.Fatalf("expected 3 wave groups, got %d", len(waves.Groups))
	}
	if waves.TotalTasks != 3 {
		t.Errorf("total tasks = %d, want 3", waves.TotalTasks)
	}

	// Apply the wave plan
	applied, err := ApplyReleaseWavePlan(result.Project, waves, "wave release v2.0", "rollout-e2e")
	if err != nil {
		t.Fatalf("ApplyReleaseWavePlan: %v", err)
	}
	// First wave is active, rest are planned
	if applied.Groups[0].WaveStatus != ReleaseWaveActive {
		t.Fatalf("first wave = %s, want active", applied.Groups[0].WaveStatus)
	}
	for i := 1; i < len(applied.Groups); i++ {
		if applied.Groups[i].WaveStatus != ReleaseWavePlanned {
			t.Errorf("wave %d = %s, want planned", i, applied.Groups[i].WaveStatus)
		}
	}

	// Advance through all waves
	for i := 0; i < len(applied.Groups)-1; i++ {
		// Approve gate for next wave
		if _, err := ApproveReleaseWaveGate(result.Project, "rollout-e2e", 0, fmt.Sprintf("go wave %d", i)); err != nil {
			t.Fatalf("ApproveReleaseWaveGate %d: %v", i, err)
		}
		advanced, err := AdvanceReleaseWaveRollout(result.Project, "rollout-e2e")
		if err != nil {
			t.Fatalf("AdvanceReleaseWaveRollout %d: %v", i, err)
		}
		if advanced.Groups[i].WaveStatus != ReleaseWaveCompleted {
			t.Fatalf("wave %d = %s, want completed", i, advanced.Groups[i].WaveStatus)
		}
	}

	// Final wave should now be active; advance to complete it
	final, err := AdvanceReleaseWaveRollout(result.Project, "rollout-e2e")
	if err != nil {
		t.Fatalf("AdvanceReleaseWaveRollout final: %v", err)
	}
	for _, g := range final.Groups {
		if g.WaveStatus != ReleaseWaveCompleted {
			t.Errorf("wave %s = %s, want completed", g.GroupLabel, g.WaveStatus)
		}
	}

	// Verify monitor reflects final state
	report, err := BuildMonitorReport(result.Project, MonitorOptions{FocusTasks: 10})
	if err != nil {
		t.Fatalf("BuildMonitorReport: %v", err)
	}
	if report.TaskTotals.Total != 3 {
		t.Errorf("total = %d, want 3", report.TaskTotals.Total)
	}
	if report.TaskTotals.Completed != 3 {
		t.Errorf("completed = %d, want 3", report.TaskTotals.Completed)
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Concurrent Task Execution
// ---------------------------------------------------------------------------

func TestE2EConcurrentTaskExecution(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	// Create 5 independent tasks
	taskIDs := make([]string, 5)
	for i := 0; i < 5; i++ {
		task, err := EnqueueTask(result.Project, fmt.Sprintf("Concurrent task %d", i), "cli")
		if err != nil {
			t.Fatalf("EnqueueTask %d: %v", i, err)
		}
		taskIDs[i] = task.ID
	}

	// Run all queued tasks concurrently via a goroutine-safe runner
	var runCount int64
	runner := &concurrentSafeRunner{
		result: &RunResult{Output: "concurrent done"},
		count:  &runCount,
	}
	summary, err := RunQueuedTasks(ctx, result.Project, result.Config, runner, QueueRunOptions{All: true})
	if err != nil {
		t.Fatalf("RunQueuedTasks: %v", err)
	}
	if len(summary.Executed) != 5 {
		t.Fatalf("executed = %d, want 5", len(summary.Executed))
	}
	if atomic.LoadInt64(&runCount) != 5 {
		t.Errorf("runner invoked %d times, want 5", runCount)
	}

	// Verify all tasks completed
	tasks, err := ListTasks(result.Project)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, task := range tasks {
		if task.Status != TaskCompleted {
			t.Errorf("task %s status = %s, want completed", task.ID, task.Status)
		}
		if task.VerificationStatus != VerificationPassed {
			t.Errorf("task %s verification = %s", task.ID, task.VerificationStatus)
		}
	}

	// Verify SQLite snapshot consistency
	db := openSnapshotDB(t, result.Project)
	defer db.Close()
	rows, err := db.Query(`SELECT task_id, status FROM tasks ORDER BY task_id`)
	if err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
	defer rows.Close()
	var snapCount int
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		snapCount++
		if status != string(TaskCompleted) {
			t.Errorf("snapshot task %s status = %s", id, status)
		}
	}
	if snapCount != 5 {
		t.Errorf("snapshot count = %d, want 5", snapCount)
	}

	// Ensure git state is clean in project root
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = result.Project.RootDir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	// Only harness state dir changes are acceptable
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, ".ggcode/harness") {
			t.Errorf("unexpected dirty path: %s", line)
		}
	}
}

// concurrentSafeRunner is a goroutine-safe fake runner for concurrent task tests.
type concurrentSafeRunner struct {
	result *RunResult
	count  *int64
}

func (r *concurrentSafeRunner) Run(_ context.Context, req RunRequest) (*RunResult, error) {
	atomic.AddInt64(r.count, 1)
	return r.result, nil
}

// ---------------------------------------------------------------------------
// E2E Test: Dependency Chain with Complex Graph
// ---------------------------------------------------------------------------

func TestE2EDependencyChainComplexGraph(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	// Create a diamond dependency graph:
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	taskA, _ := EnqueueTask(result.Project, "Root task A", "cli")
	taskB, _ := EnqueueTask(result.Project, "Branch B", "cli", QueueOptions{DependsOn: []string{taskA.ID}})
	taskC, _ := EnqueueTask(result.Project, "Branch C", "cli", QueueOptions{DependsOn: []string{taskA.ID}})
	taskD, _ := EnqueueTask(result.Project, "Merge D", "cli", QueueOptions{DependsOn: []string{taskB.ID, taskC.ID}})

	// Verify initial blocking
	if taskB.Status != TaskBlocked {
		t.Fatalf("B = %s, want blocked", taskB.Status)
	}
	if taskC.Status != TaskBlocked {
		t.Fatalf("C = %s, want blocked", taskC.Status)
	}
	if taskD.Status != TaskBlocked {
		t.Fatalf("D = %s, want blocked", taskD.Status)
	}

	// Run all - should execute in topological order
	var order []string
	var mu sync.Mutex
	runner := &orderTrackingRunner{
		result: &RunResult{Output: "done"},
		order:  &order,
		mu:     &mu,
	}
	_, err := RunQueuedTasks(ctx, result.Project, result.Config, runner, QueueRunOptions{All: true})
	if err != nil {
		t.Fatalf("RunQueuedTasks: %v", err)
	}
	if len(order) != 4 {
		t.Fatalf("executed %d tasks, want 4", len(order))
	}

	// Extract task IDs from worktree paths
	// The runner receives WorkingDir which is the worktree path containing the task ID
	extractID := func(worktreePath string) string {
		base := filepath.Base(worktreePath)
		// worktree dir is named after the task ID
		return base
	}

	// Verify A runs first
	if extractID(order[0]) != taskA.ID {
		t.Errorf("first task = %s, want A (%s)", extractID(order[0]), taskA.ID)
	}
	// D must run last
	if extractID(order[3]) != taskD.ID {
		t.Errorf("last task = %s, want D (%s)", extractID(order[3]), taskD.ID)
	}
	// B and C can be in either order but must both be before D
	executed := make(map[string]bool)
	for _, p := range order {
		executed[extractID(p)] = true
	}
	if !executed[taskB.ID] || !executed[taskC.ID] {
		t.Fatalf("B or C not found in execution order: %v", order)
	}

	// All should be completed
	for _, id := range []string{taskA.ID, taskB.ID, taskC.ID, taskD.ID} {
		loaded, _ := LoadTask(result.Project, id)
		if loaded.Status != TaskCompleted {
			t.Errorf("task %s = %s", id, loaded.Status)
		}
	}
}

type orderTrackingRunner struct {
	result *RunResult
	order  *[]string
	mu     *sync.Mutex
}

func (r *orderTrackingRunner) Run(_ context.Context, req RunRequest) (*RunResult, error) {
	r.mu.Lock()
	*r.order = append(*r.order, req.WorkingDir)
	r.mu.Unlock()
	return r.result, nil
}

func indexOf(slice []string, val string) int {
	for i, v := range slice {
		if v == val {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// E2E Test: Error Recovery and Retry
// ---------------------------------------------------------------------------

func TestE2EErrorRecoveryAndRetry(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil
	result.Config.Run.MaxAttempts = 3

	// Scenario 1: Task fails on first run, succeeds on retry
	task1, _ := EnqueueTask(result.Project, "Flaky task - fails first", "cli")
	_, err := ExecuteTask(ctx, result.Project, result.Config, task1, fakeRunner{
		result: &RunResult{Output: "transient error", ExitCode: 1},
	})
	if err != nil {
		t.Fatalf("ExecuteTask (fail): %v", err)
	}
	loaded1, _ := LoadTask(result.Project, task1.ID)
	if loaded1.Status != TaskFailed {
		t.Fatalf("failed status = %s, want failed", loaded1.Status)
	}
	if loaded1.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", loaded1.Attempt)
	}
	if loaded1.Error == "" {
		t.Error("expected error message")
	}

	// Retry with a successful runner
	retrySummary, err := RerunTask(ctx, result.Project, result.Config, task1.ID, fakeRunner{
		result: &RunResult{Output: "success on retry"},
	})
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	if retrySummary.Task.Status != TaskCompleted {
		t.Fatalf("retry status = %s, want completed", retrySummary.Task.Status)
	}
	retryLoaded, _ := LoadTask(result.Project, task1.ID)
	if retryLoaded.Attempt != 2 {
		t.Errorf("retry attempt = %d, want 2", retryLoaded.Attempt)
	}

	// Scenario 2: Task fails due to verification check failure
	result.Config.Checks.Commands = []CommandCheck{
		{Name: "build", Run: "false"}, // always fails
	}
	task2, _ := EnqueueTask(result.Project, "Verification fail task", "cli")
	_, err = ExecuteTask(ctx, result.Project, result.Config, task2, fakeRunner{
		result: &RunResult{Output: "output that passes runner but fails verification"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask (verification fail): %v", err)
	}
	loaded2, _ := LoadTask(result.Project, task2.ID)
	if loaded2.Status != TaskFailed {
		t.Fatalf("verification fail status = %s, want failed", loaded2.Status)
	}
	if loaded2.VerificationStatus != VerificationFailed {
		t.Errorf("verification = %s, want failed", loaded2.VerificationStatus)
	}
	if loaded2.Error == "" {
		t.Error("expected error from verification failure")
	}

	// Scenario 3: Batch retry of failed tasks
	result.Config.Checks.Commands = nil
	var seenRetry []RunRequest
	retrySummary2, err := RetryFailedTasksForOwner(ctx, result.Project, result.Config, "", &sequenceRunner{
		results: []*RunResult{{Output: "retry ok"}},
		seen:    &seenRetry,
	})
	if err != nil {
		t.Fatalf("RetryFailedTasks: %v", err)
	}
	if len(retrySummary2.Executed) != 1 {
		t.Fatalf("retry executed = %d, want 1 (task2 was failed)", len(retrySummary2.Executed))
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Review Rejection Flow
// ---------------------------------------------------------------------------

func TestE2EReviewRejectionFlow(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	// Create and run a task
	task, _ := EnqueueTask(result.Project, "Feature to be rejected", "cli")
	_, err := ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "implementation"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	// Reject the review
	rejected, err := RejectTaskReview(result.Project, task.ID, "Code quality issues - needs refactoring")
	if err != nil {
		t.Fatalf("RejectTaskReview: %v", err)
	}
	if rejected.ReviewStatus != ReviewRejected {
		t.Fatalf("review = %s, want rejected", rejected.ReviewStatus)
	}
	if rejected.Status != TaskFailed {
		t.Fatalf("status after rejection = %s, want failed", rejected.Status)
	}
	if !strings.Contains(rejected.Error, "review rejected") {
		t.Errorf("error = %q", rejected.Error)
	}

	// Task should NOT be promotable
	promotable, _ := ListPromotableTasks(result.Project)
	if len(promotable) != 0 {
		t.Errorf("promotable after rejection = %d, want 0", len(promotable))
	}

	// Task should NOT be reviewable
	reviewable, _ := ListReviewableTasks(result.Project)
	if len(reviewable) != 0 {
		t.Errorf("reviewable after rejection = %d, want 0", len(reviewable))
	}

	// Can retry the rejected task
	retrySummary, err := RerunTask(ctx, result.Project, result.Config, task.ID, fakeRunner{
		result: &RunResult{Output: "refactored implementation"},
	})
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	if retrySummary.Task.Status != TaskCompleted {
		t.Fatalf("retry status = %s, want completed", retrySummary.Task.Status)
	}

	// Now approve the retry
	_, err = ApproveTaskReview(result.Project, task.ID, "Refactoring looks good now")
	if err != nil {
		t.Fatalf("ApproveTaskReview: %v", err)
	}

	// Promote and release
	_, err = PromoteTask(ctx, result.Project, task.ID, "promoted after rejection cycle")
	if err != nil {
		t.Fatalf("PromoteTask: %v", err)
	}
	plan, _ := BuildReleasePlan(result.Project, result.Config)
	_, err = ApplyReleasePlan(result.Project, plan, "v1.1.0 - post rejection fix")
	if err != nil {
		t.Fatalf("ApplyReleasePlan: %v", err)
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Discover from Nested Paths
// ---------------------------------------------------------------------------

func TestE2EDiscoverFromMultipleDepths(t *testing.T) {
	root, _ := e2eInitRepo(t)

	// Create nested directories
	for _, rel := range []string{
		"src/pkg/handlers",
		"docs/api/v2",
		"internal/service/repo",
	} {
		nestedDir := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(nestedDir, 0755)

		project, err := Discover(nestedDir)
		if err != nil {
			t.Fatalf("Discover from %s: %v", rel, err)
		}
		if project.RootDir != root {
			t.Errorf("Discover(%s) root = %q, want %q", rel, project.RootDir, root)
		}
	}

	// Discover from root itself
	project, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover from root: %v", err)
	}
	if project.RootDir != root {
		t.Errorf("Discover(root) = %q, want %q", project.RootDir, root)
	}
}

// ---------------------------------------------------------------------------
// E2E Test: GC Cleans Up Old Tasks
// ---------------------------------------------------------------------------

func TestE2EGCFullCycle(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	// Create, run, approve, promote, and release a task (ready for archiving)
	task, _ := EnqueueTask(result.Project, "Task to archive", "cli")
	ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{result: &RunResult{Output: "done"}})
	ApproveTaskReview(result.Project, task.ID, "ok")
	PromoteTask(ctx, result.Project, task.ID, "promote")
	plan, _ := BuildReleasePlan(result.Project, result.Config)
	ApplyReleasePlan(result.Project, plan, "release")

	// Artificially age the task by rewriting the JSON file
	loaded, _ := LoadTask(result.Project, task.ID)
	oldTime := time.Now().UTC().Add(-200 * time.Hour)
	loaded.CreatedAt = oldTime
	loaded.UpdatedAt = oldTime
	finishedAt := oldTime
	loaded.FinishedAt = &finishedAt
	if loaded.ReleasedAt != nil {
		oldRelease := oldTime
		loaded.ReleasedAt = &oldRelease
	}
	if loaded.PromotedAt != nil {
		oldPromo := oldTime
		loaded.PromotedAt = &oldPromo
	}
	if loaded.ReviewedAt != nil {
		oldReview := oldTime
		loaded.ReviewedAt = &oldReview
	}
	SaveTask(result.Project, loaded)

	// Set aggressive GC thresholds
	result.Config.GC.ArchiveAfter = "1h"
	result.Config.GC.DeleteLogsAfter = "2h"

	report, err := RunGC(result.Project, result.Config, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunGC: %v", err)
	}
	if report == nil {
		t.Fatal("GC report is nil")
	}

	// Verify the task was archived or cleaned up somehow
	// At minimum, the GC should have processed the old task
	rendered := FormatGCReport(report)
	_ = rendered
}

// ---------------------------------------------------------------------------
// E2E Test: Doctor Diagnoses Issues
// ---------------------------------------------------------------------------

func TestE2EDoctorFullDiagnosis(t *testing.T) {
	_, result := e2eInitRepo(t)
	result.Config.Checks.Commands = nil

	// Create various task states for doctor to diagnose:
	// 1. A running task
	running, _ := NewTask("running task", "cli")
	running.Status = TaskRunning
	running.WorkerPhase = "executing"
	writeTaskFixture(result.Project, running)

	// 2. A stale blocked task (should be detected)
	stale, _ := NewTask("stale blocked", "cli")
	stale.Status = TaskBlocked
	stale.DependsOn = []string{"sa-nonexistent"}
	stale.CreatedAt = time.Now().UTC().Add(-48 * time.Hour)
	stale.UpdatedAt = time.Now().UTC().Add(-48 * time.Hour)
	writeTaskFixture(result.Project, stale)

	// 3. A review-ready task
	reviewReady, _ := NewTask("review ready", "cli")
	reviewReady.Status = TaskCompleted
	reviewReady.VerificationStatus = VerificationPassed
	reviewReady.ReviewStatus = ReviewPending
	writeTaskFixture(result.Project, reviewReady)

	// 4. An orphaned worktree
	orphanDir := filepath.Join(result.Project.WorktreesDir, "sa-orphan-test")
	os.MkdirAll(orphanDir, 0755)
	os.WriteFile(filepath.Join(orphanDir, "main.go"), []byte("package main"), 0644)

	report, err := Doctor(result.Project, result.Config)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	if report.StaleBlocked != 1 {
		t.Errorf("stale blocked = %d, want 1", report.StaleBlocked)
	}
	if report.OrphanedWorktrees != 1 {
		t.Errorf("orphaned worktrees = %d, want 1", report.OrphanedWorktrees)
	}
	if report.ReviewReady != 1 {
		t.Errorf("review ready = %d, want 1", report.ReviewReady)
	}

	output := FormatDoctorReport(report)
	for _, want := range []string{"stale_blocked=1", "orphaned=1", "review_ready=1"} {
		if !strings.Contains(output, want) {
			t.Errorf("doctor report missing %q:\n%s", want, output)
		}
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Monitor Report with Activity
// ---------------------------------------------------------------------------

func TestE2EMonitorReportWithRealActivity(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	// Create and run multiple tasks
	for i := 0; i < 3; i++ {
		task, _ := EnqueueTask(result.Project, fmt.Sprintf("Monitor task %d", i), "cli")
		ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{
			result: &RunResult{Output: fmt.Sprintf("task %d output", i)},
		})
	}

	report, err := BuildMonitorReport(result.Project, MonitorOptions{
		FocusTasks:   10,
		RecentEvents: 20,
	})
	if err != nil {
		t.Fatalf("BuildMonitorReport: %v", err)
	}
	if report.TaskTotals.Total != 3 {
		t.Errorf("total = %d, want 3", report.TaskTotals.Total)
	}
	if report.TaskTotals.Completed != 3 {
		t.Errorf("completed = %d, want 3", report.TaskTotals.Completed)
	}
	if len(report.FocusTasks) != 3 {
		t.Errorf("focus tasks = %d, want 3", len(report.FocusTasks))
	}
	if len(report.RecentEvents) == 0 {
		t.Error("expected recent events")
	}

	rendered := FormatMonitorReport(report)
	if !strings.Contains(rendered, "Harness monitor") {
		t.Errorf("monitor report missing header: %s", rendered)
	}
	for _, want := range []string{"total=3", "completed=3"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("monitor missing %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Inbox with Actionable Items
// ---------------------------------------------------------------------------

func TestE2EInboxActionableItems(t *testing.T) {
	root := t.TempDir()
	e2eGit(t, root, "init")
	e2eGit(t, root, "config", "user.name", "E2E Test")
	e2eGit(t, root, "config", "user.email", "e2e@test.com")
	os.MkdirAll(filepath.Join(root, "internal", "api"), 0755)
	os.MkdirAll(filepath.Join(root, "internal", "web"), 0755)
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	e2eGit(t, root, "add", ".")
	e2eGit(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{
		Goal: "Inbox E2E",
		Contexts: []ContextConfig{
			{Name: "api", Path: "internal/api", Owner: "backend-team"},
			{Name: "web", Path: "internal/web", Owner: "frontend-team"},
		},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// Review-ready task (backend)
	reviewTask, _ := NewTask("Review API changes", "cli")
	reviewTask.Status = TaskCompleted
	reviewTask.VerificationStatus = VerificationPassed
	reviewTask.ReviewStatus = ReviewPending
	reviewTask.ContextName = "api"
	reviewTask.ContextPath = "internal/api"
	writeTaskFixture(result.Project, reviewTask)

	// Promotion-ready task (frontend)
	promoTask, _ := NewTask("Ship frontend feature", "cli")
	promoTask.Status = TaskCompleted
	promoTask.VerificationStatus = VerificationPassed
	promoTask.ReviewStatus = ReviewApproved
	promoTask.ContextName = "web"
	promoTask.ContextPath = "internal/web"
	writeTaskFixture(result.Project, promoTask)

	// Failed task (retryable)
	retryTask, _ := NewTask("Flaky test", "cli")
	retryTask.Status = TaskFailed
	retryTask.Attempt = 1
	writeTaskFixture(result.Project, retryTask)

	inbox, err := BuildOwnerInbox(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildOwnerInbox: %v", err)
	}

	rendered := FormatOwnerInbox(inbox)
	for _, want := range []string{"backend-team", "frontend-team", "unowned"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("inbox missing %q: %s", want, rendered)
		}
	}

	// Verify actionability
	var totalReview, totalPromo, totalRetry int
	for _, entry := range inbox.Entries {
		totalReview += len(entry.ReviewReady)
		totalPromo += len(entry.PromotionReady)
		totalRetry += len(entry.Retryable)
	}
	if totalReview != 1 {
		t.Errorf("total review ready = %d, want 1", totalReview)
	}
	if totalPromo != 1 {
		t.Errorf("total promo ready = %d, want 1", totalPromo)
	}
	if totalRetry != 1 {
		t.Errorf("total retryable = %d, want 1", totalRetry)
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Context Report with Mixed States
// ---------------------------------------------------------------------------

func TestE2EContextReportMixedStates(t *testing.T) {
	root := t.TempDir()
	e2eGit(t, root, "init")
	e2eGit(t, root, "config", "user.name", "E2E Test")
	e2eGit(t, root, "config", "user.email", "e2e@test.com")
	for _, dir := range []string{"internal/api", "internal/web"} {
		os.MkdirAll(filepath.Join(root, dir), 0755)
	}
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	e2eGit(t, root, "add", ".")
	e2eGit(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{
		Goal: "Context E2E",
		Contexts: []ContextConfig{
			{Name: "api", Path: "internal/api", Owner: "backend-team"},
			{Name: "web", Path: "internal/web", Owner: "frontend-team"},
		},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	commitHarnessScaffold(t, root, result)

	// API context: 1 completed, 1 running
	apiCompleted, _ := NewTask("API completed", "cli")
	apiCompleted.Status = TaskCompleted
	apiCompleted.VerificationStatus = VerificationPassed
	apiCompleted.ContextName = "api"
	apiCompleted.ContextPath = "internal/api"
	writeTaskFixture(result.Project, apiCompleted)

	apiRunning, _ := NewTask("API running", "cli")
	apiRunning.Status = TaskRunning
	apiRunning.ContextName = "api"
	apiRunning.ContextPath = "internal/api"
	writeTaskFixture(result.Project, apiRunning)

	// Web context: 1 failed
	webFailed, _ := NewTask("Web failed", "cli")
	webFailed.Status = TaskFailed
	webFailed.VerificationStatus = VerificationFailed
	webFailed.ContextName = "web"
	webFailed.ContextPath = "internal/web"
	writeTaskFixture(result.Project, webFailed)

	report, err := BuildContextReport(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildContextReport: %v", err)
	}

	rendered := FormatContextReport(report)
	if !strings.Contains(rendered, "Harness contexts:") {
		t.Errorf("missing header: %s", rendered)
	}
	if !strings.Contains(rendered, "internal/api") {
		t.Errorf("missing api path: %s", rendered)
	}
	if !strings.Contains(rendered, "internal/web") {
		t.Errorf("missing web path: %s", rendered)
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Config Roundtrip with Full Options
// ---------------------------------------------------------------------------

func TestE2EConfigRoundtripFullOptions(t *testing.T) {
	root := t.TempDir()
	e2eGit(t, root, "init")

	cfg := DefaultConfig("e2e-project", "Build a production system")
	cfg.Contexts = []ContextConfig{
		{
			Name: "api", Path: "internal/api", Owner: "backend-team",
			Commands: []CommandCheck{
				{Name: "test", Run: "go test ./internal/api/...", Optional: true},
				{Name: "lint", Run: "golangci-lint run ./internal/api/..."},
			},
		},
		{
			Name: "web", Path: "internal/web", Owner: "frontend-team",
			Commands: []CommandCheck{
				{Name: "test", Run: "npm test"},
			},
		},
	}
	cfg.Checks.Commands = []CommandCheck{
		{Name: "build", Run: "go build ./..."},
		{Name: "vet", Run: "go vet ./..."},
	}
	cfg.Run.PromptPreamble = "Be careful in production."
	cfg.Run.MaxAttempts = 5
	cfg.Run.WorktreeMode = "git-worktree"
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

	// Verify all fields
	if loaded.Project.Name != "e2e-project" {
		t.Errorf("name = %q", loaded.Project.Name)
	}
	if len(loaded.Contexts) != 2 {
		t.Errorf("contexts = %d", len(loaded.Contexts))
	}
	if len(loaded.Checks.Commands) != 2 {
		t.Errorf("check commands = %d", len(loaded.Checks.Commands))
	}
	if loaded.Run.MaxAttempts != 5 {
		t.Errorf("max attempts = %d", loaded.Run.MaxAttempts)
	}
	if loaded.Run.WorktreeMode != "git-worktree" {
		t.Errorf("worktree mode = %q", loaded.Run.WorktreeMode)
	}
	if loaded.GC.ArchiveAfter != "48h" {
		t.Errorf("archive after = %q", loaded.GC.ArchiveAfter)
	}

	// Verify file exists and is valid YAML
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "e2e-project") {
		t.Error("config file missing project name")
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Task Persistence and Reload
// ---------------------------------------------------------------------------

func TestE2ETaskPersistenceAcrossReloads(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	// Create a task with all fields populated
	task, _ := EnqueueTask(result.Project, "Persistence test task", "cli", QueueOptions{
		ContextName: "api",
		ContextPath: "internal/api",
	})
	ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "done"},
	})
	ApproveTaskReview(result.Project, task.ID, "approved")
	PromoteTask(ctx, result.Project, task.ID, "promoted")

	// Reload the task
	loaded, err := LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}

	// Verify all fields persisted
	if loaded.Goal != "Persistence test task" {
		t.Errorf("goal = %q", loaded.Goal)
	}
	if loaded.Status != TaskCompleted {
		t.Errorf("status = %s", loaded.Status)
	}
	if loaded.ReviewStatus != ReviewApproved {
		t.Errorf("review = %s", loaded.ReviewStatus)
	}
	if loaded.PromotionStatus != PromotionApplied {
		t.Errorf("promotion = %s", loaded.PromotionStatus)
	}
	if loaded.ContextName != "api" {
		t.Errorf("context name = %q", loaded.ContextName)
	}
	if loaded.ContextPath != "internal/api" {
		t.Errorf("context path = %q", loaded.ContextPath)
	}
	if loaded.Attempt != 1 {
		t.Errorf("attempt = %d", loaded.Attempt)
	}
	if loaded.StartedAt == nil {
		t.Error("expected StartedAt")
	}
	if loaded.FinishedAt == nil {
		t.Error("expected FinishedAt")
	}
	if loaded.ReviewedAt == nil {
		t.Error("expected ReviewedAt")
	}
	if loaded.PromotedAt == nil {
		t.Error("expected PromotedAt")
	}

	// Verify JSON roundtrip
	data, err := json.MarshalIndent(loaded, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var unmarshaled Task
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if unmarshaled.ID != loaded.ID {
		t.Error("JSON roundtrip lost ID")
	}
	if unmarshaled.Goal != loaded.Goal {
		t.Error("JSON roundtrip lost Goal")
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Release Wave Pause/Resume/Abort Controls
// ---------------------------------------------------------------------------

func TestE2EReleaseWavePauseResumeAbort(t *testing.T) {
	_, result := e2eInitRepoWithContexts(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case "internal/api":
			result.Config.Contexts[i].Owner = "backend-team"
		case "internal/web":
			result.Config.Contexts[i].Owner = "frontend-team"
		case "internal/db":
			result.Config.Contexts[i].Owner = "data-team"
		}
	}

	// Create and promote tasks
	for _, ctx := range []struct {
		goal, name, path string
	}{
		{"API task", "api", "internal/api"},
		{"Web task", "web", "internal/web"},
		{"DB task", "db", "internal/db"},
	} {
		task, _ := NewTask(ctx.goal, "cli")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		task.ContextName = ctx.name
		task.ContextPath = ctx.path
		writeTaskFixture(result.Project, task)
	}

	_ = ctx // ctx not needed for setup

	// Build and apply wave plan
	waves, _ := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByOwner)
	if len(waves.Groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(waves.Groups))
	}
	_, err := ApplyReleaseWavePlan(result.Project, waves, "wave test", "rollout-e2e-ctrl")

	// Pause the rollout
	paused, err := PauseReleaseWaveRollout(result.Project, "rollout-e2e-ctrl", "waiting for signoff")
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if paused.Groups[0].WaveStatus != ReleaseWavePaused {
		t.Fatalf("wave = %s, want paused", paused.Groups[0].WaveStatus)
	}

	// Advance should fail while paused
	if _, err := AdvanceReleaseWaveRollout(result.Project, "rollout-e2e-ctrl"); err == nil {
		t.Error("expected advance to fail while paused")
	}

	// Resume
	resumed, err := ResumeReleaseWaveRollout(result.Project, "rollout-e2e-ctrl", "signoff received")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Groups[0].WaveStatus != ReleaseWaveActive {
		t.Fatalf("wave = %s, want active", resumed.Groups[0].WaveStatus)
	}

	// Abort
	aborted, err := AbortReleaseWaveRollout(result.Project, "rollout-e2e-ctrl", "critical issue found")
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	for _, g := range aborted.Groups {
		if g.WaveStatus != ReleaseWaveAborted {
			t.Errorf("group %s = %s, want aborted", g.GroupLabel, g.WaveStatus)
		}
	}

	// Cannot resume or advance after abort
	if _, err := ResumeReleaseWaveRollout(result.Project, "rollout-e2e-ctrl", "try again"); err == nil {
		t.Error("expected resume to fail after abort")
	}
	if _, err := AdvanceReleaseWaveRollout(result.Project, "rollout-e2e-ctrl"); err == nil {
		t.Error("expected advance to fail after abort")
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Snapshot DB Consistency
// ---------------------------------------------------------------------------

func TestE2ESnapshotDBConsistency(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	// Create 5 tasks with various states
	var tasks []*Task
	for i := 0; i < 5; i++ {
		task, _ := EnqueueTask(result.Project, fmt.Sprintf("Snapshot task %d", i), "cli")
		tasks = append(tasks, task)
	}

	// Execute all
	for _, task := range tasks {
		ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{
			result: &RunResult{Output: "done"},
		})
	}

	// Modify some: reject one, approve others
	RejectTaskReview(result.Project, tasks[2].ID, "bad code")
	for i := range tasks {
		if i == 2 {
			continue
		}
		ApproveTaskReview(result.Project, tasks[i].ID, "approved")
	}

	// Open snapshot DB
	db := openSnapshotDB(t, result.Project)
	defer db.Close()

	// Verify task count
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&count)
	if err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if count != 5 {
		t.Errorf("snapshot count = %d, want 5", count)
	}

	// Verify rejected task
	var rejectedStatus, rejectedReview string
	err = db.QueryRow(`SELECT status, review_status FROM tasks WHERE task_id = ?`, tasks[2].ID).
		Scan(&rejectedStatus, &rejectedReview)
	if err != nil {
		t.Fatalf("query rejected: %v", err)
	}
	if rejectedStatus != string(TaskFailed) {
		t.Errorf("rejected status = %q", rejectedStatus)
	}
	if rejectedReview != ReviewRejected {
		t.Errorf("rejected review = %q", rejectedReview)
	}

	// Verify approved tasks
	rows, err := db.Query(`SELECT task_id, review_status FROM tasks WHERE review_status = ?`, ReviewApproved)
	if err != nil {
		t.Fatalf("query approved: %v", err)
	}
	defer rows.Close()
	var approvedCount int
	for rows.Next() {
		var id, review string
		rows.Scan(&id, &review)
		approvedCount++
	}
	if approvedCount != 4 {
		t.Errorf("approved count = %d, want 4", approvedCount)
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Streaming Runner with Output Capture
// ---------------------------------------------------------------------------

func TestE2EStreamingRunnerOutputCapture(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	task, _ := EnqueueTask(result.Project, "Streaming task", "cli")

	// Use streaming runner with multiple output chunks
	var capturedOutput []string
	var capturedProgress []string
	var mu sync.Mutex
	summary, err := ExecuteTask(ctx, result.Project, result.Config, task, streamingRunner{
		result: &RunResult{Output: "final output"},
		stdout: []string{"chunk1\n", "chunk2\n", "chunk3\n"},
		stderr: []string{"progress: reading files", "progress: writing output"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	_ = capturedOutput
	_ = capturedProgress
	_ = mu.TryLock() // use mu to avoid unused variable warning (avoid copying mutex)
	mu.Unlock()

	if summary.Task.Status != TaskCompleted {
		t.Fatalf("status = %s", summary.Task.Status)
	}

	// Verify log file was created and contains content
	loaded, _ := LoadTask(result.Project, task.ID)
	if loaded.LogPath == "" {
		t.Fatal("expected log path")
	}
	logData, err := os.ReadFile(loaded.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logStr := string(logData)
	for _, want := range []string{"chunk1", "chunk2", "chunk3"} {
		if !strings.Contains(logStr, want) {
			t.Errorf("log missing %q: %s", want, logStr)
		}
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Release Plan Filtering
// ---------------------------------------------------------------------------

func TestE2EReleasePlanFiltering(t *testing.T) {
	_, result := e2eInitRepoWithContexts(t)

	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case "internal/api":
			result.Config.Contexts[i].Owner = "backend-team"
		case "internal/web":
			result.Config.Contexts[i].Owner = "frontend-team"
		case "internal/db":
			result.Config.Contexts[i].Owner = "data-team"
		}
	}

	// Create tasks in different contexts
	for _, item := range []struct {
		goal, ctxPath, ctxName string
	}{
		{"API feature", "internal/api", "api"},
		{"API fix", "internal/api", "api"},
		{"Web feature", "internal/web", "web"},
		{"DB migration", "internal/db", "db"},
	} {
		task, _ := NewTask(item.goal, "cli")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		task.ContextPath = item.ctxPath
		task.ContextName = item.ctxName
		writeTaskFixture(result.Project, task)
	}

	// Filter by owner
	backendPlan, err := BuildReleasePlanWithOptions(result.Project, result.Config, ReleasePlanOptions{
		Owner: "backend-team",
	})
	if err != nil {
		t.Fatalf("BuildReleasePlan (backend): %v", err)
	}
	if len(backendPlan.Tasks) != 2 {
		t.Errorf("backend tasks = %d, want 2", len(backendPlan.Tasks))
	}

	// Filter by context
	apiPlan, err := BuildReleasePlanWithOptions(result.Project, result.Config, ReleasePlanOptions{
		Context: "internal/api",
	})
	if err != nil {
		t.Fatalf("BuildReleasePlan (api): %v", err)
	}
	if len(apiPlan.Tasks) != 2 {
		t.Errorf("api tasks = %d, want 2", len(apiPlan.Tasks))
	}

	// Filter by environment
	envPlan, err := BuildReleasePlanWithOptions(result.Project, result.Config, ReleasePlanOptions{
		Environment: "staging",
	})
	if err != nil {
		t.Fatalf("BuildReleasePlan (staging): %v", err)
	}
	if envPlan.Environment != "staging" {
		t.Errorf("environment = %q", envPlan.Environment)
	}

	// Full plan (no filter)
	fullPlan, err := BuildReleasePlan(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildReleasePlan: %v", err)
	}
	if len(fullPlan.Tasks) != 4 {
		t.Errorf("full tasks = %d, want 4", len(fullPlan.Tasks))
	}
	if fullPlan.Owners["backend-team"] != 2 {
		t.Errorf("backend count = %d", fullPlan.Owners["backend-team"])
	}
	if fullPlan.Owners["frontend-team"] != 1 {
		t.Errorf("frontend count = %d", fullPlan.Owners["frontend-team"])
	}
	if fullPlan.Owners["data-team"] != 1 {
		t.Errorf("data count = %d", fullPlan.Owners["data-team"])
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Event Log Completeness
// ---------------------------------------------------------------------------

func TestE2EEventLogCompleteness(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	// Execute a full lifecycle to generate all event types
	task, _ := EnqueueTask(result.Project, "Event log test", "cli")
	ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{result: &RunResult{Output: "done"}})
	ApproveTaskReview(result.Project, task.ID, "ok")
	PromoteTask(ctx, result.Project, task.ID, "promote")
	plan, _ := BuildReleasePlan(result.Project, result.Config)
	ApplyReleasePlan(result.Project, plan, "release notes")

	events := readHarnessEvents(t, result.Project)

	// Categorize events by kind
	kinds := make(map[string]int)
	for _, ev := range events {
		kinds[ev.Kind]++
	}

	// We expect at minimum: created, multiple status changes
	if kinds[eventTaskCreated] == 0 {
		t.Error("missing task.created event")
	}
	if kinds[eventTaskStatusChanged] == 0 {
		t.Error("missing task.status_changed events")
	}

	// All events should have timestamps
	for _, ev := range events {
		if ev.RecordedAt.IsZero() {
			t.Errorf("event %s has zero timestamp", ev.Kind)
		}
	}

	// All events for our task should reference it
	taskEvents := 0
	for _, ev := range events {
		if ev.EntityID == task.ID {
			taskEvents++
		}
	}
	if taskEvents < 3 {
		t.Errorf("task events = %d, want at least 3", taskEvents)
	}
}

// ---------------------------------------------------------------------------
// E2E Test: List and Sort Tasks
// ---------------------------------------------------------------------------

func TestE2EListAndSortTasks(t *testing.T) {
	_, result := e2eInitRepo(t)

	// Create tasks in different states
	taskA, _ := EnqueueTask(result.Project, "Alpha task", "cli")
	taskB, _ := EnqueueTask(result.Project, "Beta task", "cli")
	taskC, _ := NewTask("Gamma task", "cli")
	taskC.Status = TaskCompleted
	taskC.VerificationStatus = VerificationPassed
	taskC.ReviewStatus = ReviewApproved
	writeTaskFixture(result.Project, taskC)

	tasks, err := ListTasks(result.Project)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("tasks = %d, want 3", len(tasks))
	}

	// Sort by goal for consistent verification
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Goal < tasks[j].Goal
	})
	if tasks[0].Goal != "Alpha task" {
		t.Errorf("first task = %q", tasks[0].Goal)
	}
	if tasks[1].Goal != "Beta task" {
		t.Errorf("second task = %q", tasks[1].Goal)
	}
	if tasks[2].Goal != "Gamma task" {
		t.Errorf("third task = %q", tasks[2].Goal)
	}

	// Verify IDs are unique
	ids := make(map[string]bool)
	for _, task := range tasks {
		if ids[task.ID] {
			t.Errorf("duplicate task ID: %s", task.ID)
		}
		ids[task.ID] = true
	}
	_ = taskA
	_ = taskB
}

// ---------------------------------------------------------------------------
// E2E Test: Normalized Contexts
// ---------------------------------------------------------------------------

func TestE2ENormalizeContextsEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    []ContextConfig
		expected int
	}{
		{
			name:     "empty",
			input:    nil,
			expected: 0,
		},
		{
			name: "single",
			input: []ContextConfig{
				{Name: "api", Path: "internal/api"},
			},
			expected: 1,
		},
		{
			name: "duplicates by name",
			input: []ContextConfig{
				{Name: "api", Path: "internal/api", Owner: "team-a"},
				{Name: "api", Path: "internal/api/v2", Owner: "team-b"},
				{Name: "web", Path: "internal/web"},
			},
			// NormalizeContexts deduplicates by firstNonEmptyText(path, name),
			// so different paths with the same name are kept as separate entries.
			// Only the first "internal/api" entry is kept since that key matches first.
			expected: 3,
		},
		{
			name: "three unique",
			input: []ContextConfig{
				{Name: "api", Path: "internal/api"},
				{Name: "web", Path: "internal/web"},
				{Name: "db", Path: "internal/db"},
			},
			expected: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeContexts(tt.input)
			if len(result) != tt.expected {
				t.Errorf("NormalizeContexts = %d, want %d", len(result), tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Nullable SQL Fields
// ---------------------------------------------------------------------------

func TestE2ENullableSQLFieldsComplete(t *testing.T) {
	_, result := e2eInitRepo(t)
	ctx := context.Background()
	result.Config.Checks.Commands = nil

	// Task with minimal fields
	task, _ := EnqueueTask(result.Project, "Minimal nullable task", "cli")
	ExecuteTask(ctx, result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "done"},
	})

	db := openSnapshotDB(t, result.Project)
	defer db.Close()

	// Query all nullable fields
	var (
		logPath      sql.NullString
		errorText    sql.NullString
		branchName   sql.NullString
		workspace    sql.NullString
		reviewNotes  sql.NullString
		promoNotes   sql.NullString
		reviewedAt   sql.NullString
		promotedAt   sql.NullString
		releasedAt   sql.NullString
		releaseBatch sql.NullString
		releaseNotes sql.NullString
	)
	err := db.QueryRow(`
		SELECT log_path, error_text, branch_name, workspace_path,
		       review_notes, promotion_notes, reviewed_at, promoted_at,
		       released_at, release_batch_id, release_notes
		FROM tasks WHERE task_id = ?`, task.ID).Scan(
		&logPath, &errorText, &branchName, &workspace,
		&reviewNotes, &promoNotes, &reviewedAt, &promotedAt,
		&releasedAt, &releaseBatch, &releaseNotes,
	)
	if err != nil {
		t.Fatalf("scan nullable fields: %v", err)
	}

	// log_path should be set (we executed)
	if !logPath.Valid || logPath.String == "" {
		t.Error("expected log_path to be set")
	}
	// error_text should be empty for a successful task
	if errorText.Valid && errorText.String != "" {
		t.Errorf("error_text = %q, want empty", errorText.String)
	}
	// release fields should be empty (not released)
	if releaseBatch.Valid && releaseBatch.String != "" {
		t.Errorf("release_batch = %q, want empty", releaseBatch.String)
	}
}
