//go:build integration

package harness

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// ---------------------------------------------------------------------------
// E2E: Auto-Run Full Lifecycle
// ---------------------------------------------------------------------------

// TestE2EAutoRun_FullLifecycle tests the complete auto-run flow:
// user input → router decides RouteHarness → RunService executes →
// task completes → review approved → promoted.
func TestE2EAutoRun_FullLifecycle(t *testing.T) {
	root, initResult := e2eInitRepo(t)
	ctx := context.Background()

	// Step 1: Router decides "fix the auth bug" is a code task
	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "on"}}
	routeCtx := RouteContext{
		Input:      "fix the auth bug in auth.go",
		WorkingDir: root,
	}

	result, err := ShouldAutoRun(cfg, "fix the auth bug in auth.go", routeCtx)
	if err != nil {
		t.Fatalf("ShouldAutoRun: %v", err)
	}
	if result == nil {
		t.Fatal("ShouldAutoRun returned nil result")
	}
	if result.Decision != RouteHarness {
		t.Fatalf("decision = %v, want RouteHarness", result.Decision)
	}
	if result.Project == nil {
		t.Fatal("result.Project is nil")
	}

	// Step 2: Execute via RunService with fake runner
	svc := NewRunService()
	runResult := svc.Run(ctx, RunServiceInput{
		Project: *result.Project,
		Config:  initResult.Config,
		Goal:    "fix the auth bug in auth.go",
		Runner: fakeRunner{result: &RunResult{
			Output: "Fixed auth bug: added proper session validation",
		}},
		Options: RunTaskOptions{},
	})

	if runResult.Error != nil {
		t.Fatalf("RunService.Run error: %v", runResult.Error)
	}
	if runResult.Summary == nil || runResult.Summary.Task == nil {
		t.Fatal("RunService returned no task")
	}
	task := runResult.Summary.Task

	// Step 3: Verify task completed and review pending
	if task.Status != TaskCompleted {
		t.Fatalf("task status = %s, want completed", task.Status)
	}
	if task.ReviewStatus != ReviewPending {
		t.Fatalf("review status = %s, want pending", task.ReviewStatus)
	}

	// Step 4: Verify CTA suggests review
	if runResult.CTA != CTAReview {
		t.Fatalf("CTA = %v, want CTAReview", runResult.CTA)
	}

	// Step 5: Approve the review
	approved, err := ApproveTaskReview(*result.Project, task.ID, "LGTM - auth fix looks good")
	if err != nil {
		t.Fatalf("ApproveTaskReview: %v", err)
	}
	if approved.ReviewStatus != ReviewApproved {
		t.Fatalf("review status = %s, want approved", approved.ReviewStatus)
	}

	// Step 6: Promote the task
	promoted, err := PromoteTask(ctx, *result.Project, task.ID, "promoted auth fix")
	if err != nil {
		t.Fatalf("PromoteTask: %v", err)
	}
	if promoted.PromotionStatus != PromotionApplied {
		t.Fatalf("promotion status = %s, want applied", promoted.PromotionStatus)
	}

	// Step 7: Verify task is no longer reviewable
	reviewable, err := ListReviewableTasks(*result.Project)
	if err != nil {
		t.Fatalf("ListReviewableTasks: %v", err)
	}
	for _, r := range reviewable {
		if r.ID == task.ID {
			t.Fatal("promoted task should not appear in reviewable list")
		}
	}
}

// TestE2EAutoRun_SuggestMode tests that suggest mode returns RouteSuggest
// and does NOT auto-execute.
func TestE2EAutoRun_SuggestMode(t *testing.T) {
	root, _ := e2eInitRepo(t)

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "suggest"}}
	routeCtx := RouteContext{
		Input:      "fix the auth bug in auth.go",
		WorkingDir: root,
	}

	result, err := ShouldAutoRun(cfg, "fix the auth bug in auth.go", routeCtx)
	if err != nil {
		t.Fatalf("ShouldAutoRun: %v", err)
	}
	if result == nil {
		t.Fatal("ShouldAutoRun returned nil result")
	}
	if result.Decision != RouteSuggest {
		t.Fatalf("decision = %v, want RouteSuggest in suggest mode", result.Decision)
	}
}

// TestE2EAutoRun_StrictMode_EnforcesWorktree tests that strict mode
// propagates worktree_mode=required through AutoRunResult.Config.
func TestE2EAutoRun_StrictMode_EnforcesWorktree(t *testing.T) {
	root, _ := e2eInitRepo(t)

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "strict"}}
	routeCtx := RouteContext{
		Input:      "fix the auth bug in auth.go",
		WorkingDir: root,
	}

	result, err := ShouldAutoRun(cfg, "fix the auth bug in auth.go", routeCtx)
	if err != nil {
		t.Fatalf("ShouldAutoRun: %v", err)
	}
	if result == nil {
		t.Fatal("ShouldAutoRun returned nil result")
	}

	// Strict mode should set write guard
	if !result.StrictWriteGuard {
		t.Error("StrictWriteGuard should be true in strict mode")
	}

	// Config should enforce worktree
	if result.Config == nil {
		t.Fatal("Config should not be nil in strict mode")
	}
	if result.Config.Run.WorktreeMode != "required" {
		t.Errorf("worktree_mode = %q, want 'required'", result.Config.Run.WorktreeMode)
	}
}

// TestE2EAutoRun_QuestionNotRouted tests that questions are not routed
// to harness even in "on" mode.
func TestE2EAutoRun_QuestionNotRouted(t *testing.T) {
	root, _ := e2eInitRepo(t)

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "on"}}
	routeCtx := RouteContext{
		Input:      "What does this function do?",
		WorkingDir: root,
	}

	result, err := ShouldAutoRun(cfg, "What does this function do?", routeCtx)
	if err != nil {
		t.Fatalf("ShouldAutoRun: %v", err)
	}
	if result == nil {
		t.Fatal("ShouldAutoRun should return non-nil result")
	}
	if result.Decision == RouteHarness {
		t.Fatal("questions should not be routed to harness")
	}
}

// TestE2EAutoRun_DirtyWorkspace_RejectedByConfirmer tests that RunService
// fails fast when the workspace has uncommitted changes and the confirmer
// rejects the checkpoint.
func TestE2EAutoRun_DirtyWorkspace_RejectedByConfirmer(t *testing.T) {
	root, initResult := e2eInitRepo(t)

	// Make workspace dirty
	dirtyFile := filepath.Join(root, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("uncommitted changes"), 0644); err != nil {
		t.Fatal(err)
	}

	svc := NewRunService()
	runResult := svc.Run(context.Background(), RunServiceInput{
		Project: initResult.Project,
		Config:  initResult.Config,
		Goal:    "fix a bug",
		Runner:  fakeRunner{result: &RunResult{Output: "fixed"}},
		Options: RunTaskOptions{
			ConfirmDirtyWorkspace: func(checkpoint DirtyWorkspaceCheckpoint) (bool, error) {
				return false, nil // Reject the checkpoint
			},
		},
	})

	if runResult.Error == nil {
		t.Fatal("RunService should fail when confirmer rejects dirty workspace")
	}
}

// TestE2EAutoRun_OffModeSkipsEverything tests that off mode returns
// RouteNone regardless of input.
func TestE2EAutoRun_OffModeSkipsEverything(t *testing.T) {
	root, _ := e2eInitRepo(t)

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "off"}}
	routeCtx := RouteContext{
		Input:      "fix the auth bug in auth.go",
		WorkingDir: root,
	}

	result, err := ShouldAutoRun(cfg, "fix the auth bug in auth.go", routeCtx)
	if err != nil {
		t.Fatalf("ShouldAutoRun: %v", err)
	}
	if result == nil {
		t.Fatal("ShouldAutoRun should return non-nil result")
	}
	if result.Decision != RouteNone {
		t.Fatalf("decision = %v, want RouteNone in off mode", result.Decision)
	}
}

// TestE2EAutoRun_MultipleTasks_IndependentPromotion tests that promoting
// one auto-run task does not affect other tasks.
func TestE2EAutoRun_MultipleTasks_IndependentPromotion(t *testing.T) {
	_, initResult := e2eInitRepo(t)
	ctx := context.Background()
	initResult.Config.Checks.Commands = nil

	// Execute two tasks
	svc := NewRunService()
	for i, goal := range []string{"fix bug A", "fix bug B"} {
		runResult := svc.Run(ctx, RunServiceInput{
			Project: initResult.Project,
			Config:  initResult.Config,
			Goal:    goal,
			Runner:  fakeRunner{result: &RunResult{Output: "fixed"}},
			Options: RunTaskOptions{},
		})
		if runResult.Error != nil {
			t.Fatalf("RunService task %d error: %v", i, runResult.Error)
		}
	}

	// Find both tasks
	tasks, err := ListTasks(initResult.Project)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) < 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	task1 := tasks[0]
	task2 := tasks[1]

	// Approve both
	_, err = ApproveTaskReview(initResult.Project, task1.ID, "ok")
	if err != nil {
		t.Fatalf("ApproveTaskReview task1: %v", err)
	}
	_, err = ApproveTaskReview(initResult.Project, task2.ID, "ok")
	if err != nil {
		t.Fatalf("ApproveTaskReview task2: %v", err)
	}

	// Promote only task1
	promoted, err := PromoteTask(ctx, initResult.Project, task1.ID, "promoted")
	if err != nil {
		t.Fatalf("PromoteTask task1: %v", err)
	}
	if promoted.PromotionStatus != PromotionApplied {
		t.Fatalf("task1 promotion = %s, want applied", promoted.PromotionStatus)
	}

	// Verify task2 is NOT promoted
	task2Reloaded, err := LoadTask(initResult.Project, task2.ID)
	if err != nil {
		t.Fatalf("LoadTask task2: %v", err)
	}
	if task2Reloaded.PromotionStatus == PromotionApplied {
		t.Error("task2 should NOT be promoted — only task1 was targeted")
	}
}

// TestE2EAutoRun_LLMClassifierOverrides tests that the LLM classifier
// can override a low-scoring input to RouteHarness.
func TestE2EAutoRun_LLMClassifierOverrides(t *testing.T) {
	root, _ := e2eInitRepo(t)

	// "the login page is broken" — no action verb, no file path, low structural score
	// But with LLM classifier, it should be classified as code change
	prov := &mockClassifierProvider{
		response: `{"classification": "code_change", "confidence": 0.92, "reason": "bug report implying code fix"}`,
	}

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "on"}}
	routeCtx := RouteContext{
		Input:                 "the login page is broken",
		WorkingDir:            root,
		LLMClassifierProvider: prov,
	}

	result, err := ShouldAutoRun(cfg, "the login page is broken", routeCtx)
	if err != nil {
		t.Fatalf("ShouldAutoRun: %v", err)
	}
	if result == nil {
		t.Fatal("ShouldAutoRun should route via LLM classifier")
	}
	if result.Decision != RouteHarness {
		t.Fatalf("decision = %v, want RouteHarness (LLM override)", result.Decision)
	}
}

// TestE2EAutoRun_AutoInit tests that auto-run can auto-initialize a
// harness project when auto_init is enabled.
func TestE2EAutoRun_AutoInit(t *testing.T) {
	root := t.TempDir()
	e2eGit(t, root, "init")
	e2eGit(t, root, "config", "user.name", "E2E Test")
	e2eGit(t, root, "config", "user.email", "e2e@test.com")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# test"), 0644)
	e2eGit(t, root, "add", "README.md")
	e2eGit(t, root, "commit", "--no-verify", "-m", "initial")

	// No harness init yet — auto_init should create it
	cfg := &config.Config{Harness: config.HarnessConfig{
		AutoRun:  "on",
		AutoInit: true,
	}}
	routeCtx := RouteContext{
		Input:      "fix the auth bug in auth.go",
		WorkingDir: root,
	}

	result, err := ShouldAutoRun(cfg, "fix the auth bug in auth.go", routeCtx)
	if err != nil {
		t.Fatalf("ShouldAutoRun: %v", err)
	}
	if result == nil {
		t.Fatal("ShouldAutoRun should auto-init and route")
	}
	if !result.AutoInitPerformed {
		t.Error("AutoInitPerformed should be true")
	}
	if result.Project == nil {
		t.Fatal("Project should be initialized")
	}

	// Verify harness directory exists
	harnessDir := filepath.Join(root, ".ggcode", "harness")
	if stat, err := os.Stat(harnessDir); err != nil || !stat.IsDir() {
		t.Errorf("harness dir should exist at %s", harnessDir)
	}
}
