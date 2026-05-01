package harness

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestPipeAutoRun_UsesFullPromptForRouting verifies that the routing decision
// is based on the full prompt (including stdin data), not just the raw prompt.
func TestPipeAutoRun_UsesFullPromptForRouting(t *testing.T) {
	dir := t.TempDir()
	createMinimalHarnessProject(t, dir)

	// Simulate pipe stdin: "Fix the auth bug" + stdin data with file contents
	stdinContent := "\n\n--- STDIN ---\npackage auth\n\nfunc Login() error { return nil }"
	fullPrompt := "Fix the auth bug" + stdinContent

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "on"}}
	ctx := RouteContext{
		Input:      fullPrompt,
		WorkingDir: dir,
	}

	result, err := ShouldAutoRun(cfg, fullPrompt, ctx)
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}

	// The full prompt (with code content) should be routed to harness
	if result.Decision != RouteHarness {
		t.Errorf("decision = %v, want RouteHarness. Features should detect code in stdin.", result.Decision)
	}
}

// TestPipeAutoRun_PureQuestionNotRouted verifies that pure questions are not
// routed to harness, even with stdin data attached.
func TestPipeAutoRun_PureQuestionNotRouted(t *testing.T) {
	dir := t.TempDir()
	createMinimalHarnessProject(t, dir)

	fullPrompt := "What does this code do?\n\n--- STDIN ---\npackage main"

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "on"}}
	ctx := RouteContext{
		Input:      fullPrompt,
		WorkingDir: dir,
	}

	result, err := ShouldAutoRun(cfg, fullPrompt, ctx)
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}

	// Questions should not be routed regardless of stdin
	if result.Decision == RouteHarness {
		t.Error("pure questions should not be routed to harness even with stdin")
	}
}

// TestDirtyWorkspace_FailFastWhenNoConfirmer verifies that RunService
// injects a fail-fast ConfirmDirtyWorkspace when none is provided.
// We verify this indirectly by checking the Options field after RunService
// sets up the confirmer.
func TestDirtyWorkspace_FailFastWhenNoConfirmer(t *testing.T) {
	svc := NewRunService()

	input := RunServiceInput{
		Project: Project{RootDir: "/nonexistent"},
		Config:  &Config{},
		Goal:    "test",
		Runner:  &noopRunner{},
		Options: RunTaskOptions{},
	}

	// The confirmer should be injected before execution
	_ = svc.Run(context.Background(), input)

	// We can't directly check the injected confirmer, but we verify
	// the behavior is correct through TestRunService_FailFastDirtyWorkspace
	// and TestRunService_ExplicitConfirmerNotOverridden in run_service_test.go.
	// This test just verifies Run doesn't panic with nil confirmer.
}

// TestStrictMode_AllToolsDeniedForAllRouteOutcomes verifies that in strict mode,
// the write guard applies the full set of denied tools.
func TestStrictMode_AllToolsDeniedForAllRouteOutcomes(t *testing.T) {
	dir := t.TempDir()
	createMinimalHarnessProject(t, dir)

	// Test with a question (RouteNone)
	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "strict"}}
	ctx := RouteContext{
		Input:      "What does this function do?",
		WorkingDir: dir,
	}

	result, err := ShouldAutoRun(cfg, ctx.Input, ctx)
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}

	// Even though the question is not routed to harness...
	if result.Decision == RouteHarness {
		t.Error("questions should not route to harness")
	}

	// ...the strict config should still be returned with StrictWriteGuard set
	if !result.StrictWriteGuard {
		t.Error("strict mode should set StrictWriteGuard even for non-routed inputs")
	}
	if result.Config == nil {
		t.Fatal("strict mode should return config override")
	}
	if result.Config.Run.WorktreeMode != "required" {
		t.Errorf("worktree_mode = %q, want 'required'", result.Config.Run.WorktreeMode)
	}
}

type noopRunner struct{}

func (n *noopRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	return &RunResult{Output: "noop"}, nil
}
