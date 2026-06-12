package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestShouldAutoRun_OffMode(t *testing.T) {
	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "off"}}
	result, err := ShouldAutoRun(cfg, "Fix the bug in auth.go", RouteContext{})
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}
	if result.Decision != RouteNone {
		t.Errorf("off mode: decision = %v, want RouteNone", result.Decision)
	}
}

func TestShouldAutoRun_NilConfig(t *testing.T) {
	result, err := ShouldAutoRun(nil, "Fix the bug", RouteContext{})
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}
	if result.Decision != RouteNone {
		t.Errorf("nil config: decision = %v, want RouteNone", result.Decision)
	}
}

func TestShouldAutoRun_QuestionNotRouted(t *testing.T) {
	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "on"}}
	result, err := ShouldAutoRun(cfg, "What is a closure?", RouteContext{})
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}
	if result.Decision != RouteNormal {
		t.Errorf("question: decision = %v, want RouteNormal", result.Decision)
	}
}

func TestShouldAutoRun_CodeTaskOnMode(t *testing.T) {
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
	if result.Decision != RouteHarness {
		t.Errorf("code task on mode: decision = %v, want RouteHarness", result.Decision)
	}
	if result.Project == nil {
		t.Error("expected project to be resolved")
	}
}

func TestShouldAutoRun_CodeTaskSuggestMode(t *testing.T) {
	cfg := &config.Config{Harness: config.HarnessConfig{AutoRun: "suggest"}}
	result, err := ShouldAutoRun(cfg, "Fix the bug in auth.go", RouteContext{})
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}
	if result.Decision != RouteSuggest {
		t.Errorf("suggest mode: decision = %v, want RouteSuggest", result.Decision)
	}
	if result.Message == "" {
		t.Error("suggest mode should have a message")
	}
}

func TestShouldAutoRun_AutoInit(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.Config{Harness: config.HarnessConfig{
		AutoRun:  "on",
		AutoInit: true,
	}}

	// No harness.yaml exists — auto-init should create one
	ctx := RouteContext{
		Input:      "Fix the bug in auth.go",
		WorkingDir: dir,
	}
	result, err := ShouldAutoRun(cfg, "Fix the bug in auth.go", ctx)
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}

	// After auto-init, harness.yaml should exist
	harnessYaml := filepath.Join(dir, ".ggcode", "harness.yaml")
	if _, err := os.Stat(harnessYaml); os.IsNotExist(err) {
		t.Error("auto-init should have created harness.yaml")
	}

	// Should have auto-initialized and routed
	if !result.AutoInitPerformed {
		t.Error("expected AutoInitPerformed to be true")
	}
	_ = result
}

func TestShouldAutoRun_NoAutoInit(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.Config{Harness: config.HarnessConfig{
		AutoRun:  "on",
		AutoInit: false,
	}}

	ctx := RouteContext{
		Input:      "Fix the bug in auth.go",
		WorkingDir: dir,
	}
	result, err := ShouldAutoRun(cfg, "Fix the bug in auth.go", ctx)
	if err != nil {
		t.Fatalf("ShouldAutoRun() error = %v", err)
	}

	// Without auto-init, no harness.yaml
	harnessYaml := filepath.Join(dir, ".ggcode", "harness.yaml")
	if _, err := os.Stat(harnessYaml); err == nil {
		t.Error("harness.yaml should NOT be created when AutoInit is false")
	}

	// Decision should be downgraded to RouteSuggest because no project
	if result.Decision == RouteHarness {
		t.Errorf("should not be RouteHarness without a project, got %v", result.Decision)
	}
}

func TestShouldAutoRun_StrictModeConfigOverride(t *testing.T) {
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
	if result.Decision != RouteHarness {
		t.Errorf("strict mode: decision = %v, want RouteHarness", result.Decision)
	}
	if result.Config == nil {
		t.Fatal("strict mode should return overridden config")
	}
	if result.Config.Run.WorktreeMode != "required" {
		t.Errorf("strict mode config worktree_mode = %q, want 'required'", result.Config.Run.WorktreeMode)
	}
}

func TestShouldAutoRun_OnModeDoesNotSetWriteGuard(t *testing.T) {
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
	if result.StrictWriteGuard {
		t.Error("on mode should NOT set StrictWriteGuard")
	}
}

// createMinimalHarnessProject creates a minimal harness project in the given directory.
func createMinimalHarnessProject(t *testing.T, dir string) {
	t.Helper()
	_, err := AutoInit(dir)
	if err != nil {
		t.Fatalf("createMinimalHarnessProject: %v", err)
	}
}
