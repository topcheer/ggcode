package harness

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestStrictMode_WorktreeRequiredFailsWithoutGit(t *testing.T) {
	dir := t.TempDir() // No .git dir

	project := Project{RootDir: dir, WorktreesDir: dir + "/.ggcode/worktrees"}
	cfg := &Config{Run: RunConfig{WorktreeMode: "required"}}
	task := &Task{ID: "test-strict"}

	_, err := PrepareWorkspace(context.Background(), project, cfg, task)
	if err == nil {
		t.Error("expected error when worktree required but no git repo")
	}
}

func TestStrictMode_WorktreeAutoFallsBackWithoutGit(t *testing.T) {
	dir := t.TempDir() // No .git dir

	project := Project{RootDir: dir, WorktreesDir: dir + "/.ggcode/worktrees"}
	cfg := &Config{Run: RunConfig{WorktreeMode: "auto"}}
	task := &Task{ID: "test-auto"}

	ws, err := PrepareWorkspace(context.Background(), project, cfg, task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws.Mode != "root" {
		t.Errorf("expected root fallback, got mode=%s", ws.Mode)
	}
}

func TestAutoRunResult_ConfigPropagation(t *testing.T) {
	dir := t.TempDir()
	createMinimalHarnessProject(t, dir)

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "strict"}}
	ctx := RouteContext{
		Input:      "Fix the bug in auth.go",
		WorkingDir: dir,
	}
	result, err := ShouldAutoRun(cfg, "Fix the bug in auth.go", ctx)
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}

	// Config should have worktree_mode overridden to "required"
	if result.Config == nil {
		t.Fatal("strict mode should return config")
	}
	if result.Config.Run.WorktreeMode != "required" {
		t.Errorf("worktree_mode = %q, want 'required'", result.Config.Run.WorktreeMode)
	}

	// StrictWriteGuard should be set
	if !result.StrictWriteGuard {
		t.Error("strict mode should set StrictWriteGuard")
	}
}

func TestAutoRunResult_OnModeNoConfigOverride(t *testing.T) {
	dir := t.TempDir()
	createMinimalHarnessProject(t, dir)

	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "on"}}
	ctx := RouteContext{
		Input:      "Fix the bug in auth.go",
		WorkingDir: dir,
	}
	result, err := ShouldAutoRun(cfg, "Fix the bug in auth.go", ctx)
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}

	// "on" mode should NOT override config
	if result.Config != nil {
		t.Error("on mode should not override config")
	}
	if result.StrictWriteGuard {
		t.Error("on mode should not set StrictWriteGuard")
	}
}
