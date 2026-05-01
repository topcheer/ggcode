package harness

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestAutoRunFlow_OnMode_RoutesCodeTask verifies the full flow from
// ShouldAutoRun → RunService → CTA for a typical code task in "on" mode.
func TestAutoRunFlow_OnMode_RoutesCodeTask(t *testing.T) {
	dir := t.TempDir()
	createMinimalHarnessProject(t, dir)

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "on"}}
	ctx := RouteContext{
		Input:      "Refactor the authentication module to use JWT tokens",
		WorkingDir: dir,
	}

	// Step 1: Route
	result, err := ShouldAutoRun(cfg, ctx.Input, ctx)
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}
	if result.Decision != RouteHarness {
		t.Fatalf("decision = %v, want RouteHarness", result.Decision)
	}
	if result.Project == nil {
		t.Fatal("project should be resolved")
	}

	// Step 2: Simulate CTA generation (without actually running)
	task := &Task{
		ID:                 "t-flow-test",
		Status:             TaskCompleted,
		VerificationStatus: VerificationPassed,
		ReviewStatus:       ReviewPending,
		Goal:               "Refactor auth module",
		ChangedFiles:       []string{"auth/jwt.go", "auth/middleware.go"},
	}
	cta, msg := generateCTA(&RunSummary{Task: task}, nil)
	if cta != CTAReview {
		t.Errorf("CTA = %q, want CTAReview", cta)
	}
	if msg == "" {
		t.Error("CTA message should not be empty")
	}

	// Step 3: Verify promotion safety — PromoteTask would reject
	// without approval (this is tested elsewhere, just confirming the invariant)
	if taskPromotionReady(task) {
		t.Error("task should not be promotion-ready without approval")
	}
}

// TestAutoRunFlow_StrictMode_EnforcesIsolation verifies strict mode flow:
// strict config override → write guard flag → worktree required.
func TestAutoRunFlow_StrictMode_EnforcesIsolation(t *testing.T) {
	dir := t.TempDir()
	createMinimalHarnessProject(t, dir)

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "strict"}}
	ctx := RouteContext{
		Input:      "Fix the race condition in worker pool",
		WorkingDir: dir,
	}

	result, err := ShouldAutoRun(cfg, ctx.Input, ctx)
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}

	// Strict mode should override config
	if result.Config == nil {
		t.Fatal("strict mode should return overridden config")
	}
	if result.Config.Run.WorktreeMode != "required" {
		t.Errorf("worktree_mode = %q, want 'required'", result.Config.Run.WorktreeMode)
	}

	// Write guard should be set
	if !result.StrictWriteGuard {
		t.Error("strict mode should set StrictWriteGuard")
	}
}

// TestAutoRunFlow_SuggestMode_AsksUser verifies suggest mode returns
// RouteSuggest with a message.
func TestAutoRunFlow_SuggestMode_AsksUser(t *testing.T) {
	dir := t.TempDir()
	createMinimalHarnessProject(t, dir)

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "suggest"}}
	ctx := RouteContext{
		Input:      "Add error handling to the API layer",
		WorkingDir: dir,
	}

	result, err := ShouldAutoRun(cfg, ctx.Input, ctx)
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}
	if result.Decision != RouteSuggest {
		t.Fatalf("decision = %v, want RouteSuggest", result.Decision)
	}
	if result.Message == "" {
		t.Error("suggest mode should return a message")
	}
}

// TestAutoRunFlow_Question_NotRouted verifies pure questions are not routed.
func TestAutoRunFlow_Question_NotRouted(t *testing.T) {
	dir := t.TempDir()
	createMinimalHarnessProject(t, dir)

	for _, mode := range []string{"on", "strict", "suggest"} {
		t.Run(mode, func(t *testing.T) {
			cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: mode}}
			ctx := RouteContext{
				Input:      "What does this function do?",
				WorkingDir: dir,
			}
			result, err := ShouldAutoRun(cfg, ctx.Input, ctx)
			if err != nil {
				t.Fatalf("ShouldAutoRun() error = %v", err)
			}
			if result.Decision == RouteHarness {
				t.Errorf("questions should not be routed to harness (mode=%s)", mode)
			}
		})
	}
}
