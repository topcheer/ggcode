package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

// mockModeSwitcher implements ModeSwitcher for testing
type mockModeSwitcher struct {
	currentMode  permission.PermissionMode
	previousMode permission.PermissionMode
}

func (m *mockModeSwitcher) Mode() permission.PermissionMode {
	return m.currentMode
}

func (m *mockModeSwitcher) SetMode(mode permission.PermissionMode) {
	m.currentMode = mode
}

func (m *mockModeSwitcher) RememberMode(currentMode permission.PermissionMode) permission.PermissionMode {
	prev := m.currentMode
	m.previousMode = prev
	return prev
}

func (m *mockModeSwitcher) RestoreMode(fallback permission.PermissionMode) permission.PermissionMode {
	if m.previousMode != 0 && m.previousMode != permission.PlanMode {
		return m.previousMode
	}
	return fallback
}

// ---- Enter Plan Mode Tests ----

func TestEnterPlanMode_Basic(t *testing.T) {
	switcher := &mockModeSwitcher{currentMode: permission.BypassMode}
	tool := EnterPlanModeTool{Switcher: switcher}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if switcher.currentMode != permission.PlanMode {
		t.Errorf("mode = %v, want PlanMode", switcher.currentMode)
	}
	// Should have remembered BypassMode
	if switcher.previousMode != permission.BypassMode {
		t.Errorf("previousMode = %v, want BypassMode", switcher.previousMode)
	}
}

func TestEnterPlanMode_NilSwitcher(t *testing.T) {
	tool := EnterPlanModeTool{}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for nil switcher")
	}
}

func TestEnterPlanMode_FromAutopilot(t *testing.T) {
	switcher := &mockModeSwitcher{currentMode: permission.AutopilotMode}
	tool := EnterPlanModeTool{Switcher: switcher}

	_, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	if switcher.previousMode != permission.AutopilotMode {
		t.Errorf("previousMode = %v, want AutopilotMode", switcher.previousMode)
	}
}

func TestEnterPlanModeDescriptionMatchesReadOnlyPolicy(t *testing.T) {
	tool := EnterPlanModeTool{}
	desc := tool.Description()
	for _, want := range []string{"read-only", "LSP", "git", "web", "writes and shell execution are denied"} {
		if !contains(desc, want) {
			t.Fatalf("enter_plan_mode description should mention %q, got %q", want, desc)
		}
	}
}

// ---- Exit Plan Mode Tests ----

func TestExitPlanMode_RestoresPreviousMode(t *testing.T) {
	switcher := &mockModeSwitcher{
		currentMode:  permission.PlanMode,
		previousMode: permission.BypassMode, // simulated: was bypass before plan
	}
	tool := ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode, PlansDir: t.TempDir()}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"plan":"do something"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Should restore to BypassMode, NOT SupervisedMode
	if switcher.currentMode != permission.BypassMode {
		t.Errorf("mode = %v, want BypassMode", switcher.currentMode)
	}
}

func TestExitPlanMode_NoPreviousModeUsesDefault(t *testing.T) {
	switcher := &mockModeSwitcher{
		currentMode:  permission.PlanMode,
		previousMode: 0, // no previous mode remembered
	}
	tool := ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode, PlansDir: t.TempDir()}

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"plan":"do something"}`))
	if err != nil {
		t.Fatal(err)
	}
	if switcher.currentMode != permission.SupervisedMode {
		t.Errorf("mode = %v, want SupervisedMode (default)", switcher.currentMode)
	}
}

func TestExitPlanMode_NilSwitcher(t *testing.T) {
	tool := ExitPlanModeTool{}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"plan":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nil switcher")
	}
}

func TestExitPlanMode_EmptyPlan(t *testing.T) {
	switcher := &mockModeSwitcher{}
	tool := ExitPlanModeTool{Switcher: switcher}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"plan":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for empty plan")
	}
}

// ---- Full Round-Trip Test ----

func TestPlanModeRoundTrip_Bypass(t *testing.T) {
	// Simulate: user is in bypass mode → enters plan → exits plan → should be back in bypass
	switcher := &mockModeSwitcher{currentMode: permission.BypassMode}

	enterTool := EnterPlanModeTool{Switcher: switcher}
	exitTool := ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode, PlansDir: t.TempDir()}

	// 1. Enter plan mode
	result, _ := enterTool.Execute(context.Background(), json.RawMessage(`{}`))
	if switcher.currentMode != permission.PlanMode {
		t.Fatalf("step 1: mode = %v, want PlanMode", switcher.currentMode)
	}
	t.Logf("enter result: %s", result.Content)

	// 2. Exit plan mode (no explicit mode → should restore bypass)
	result, _ = exitTool.Execute(context.Background(), json.RawMessage(`{"plan":"refactor the module"}`))
	if switcher.currentMode != permission.BypassMode {
		t.Errorf("step 2: mode = %v, want BypassMode (restored)", switcher.currentMode)
	}
	t.Logf("exit result: %s", result.Content)
}

func TestPlanModeRoundTrip_Autopilot(t *testing.T) {
	switcher := &mockModeSwitcher{currentMode: permission.AutopilotMode}

	enterTool := EnterPlanModeTool{Switcher: switcher}
	exitTool := ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode, PlansDir: t.TempDir()}

	// Enter
	enterTool.Execute(context.Background(), json.RawMessage(`{}`))
	if switcher.currentMode != permission.PlanMode {
		t.Fatal("should be in plan mode")
	}

	// Exit → should restore AutopilotMode
	exitTool.Execute(context.Background(), json.RawMessage(`{"plan":"plan content"}`))
	if switcher.currentMode != permission.AutopilotMode {
		t.Errorf("mode = %v, want AutopilotMode (restored)", switcher.currentMode)
	}
}

func TestPlanModeRoundTrip_Supervised(t *testing.T) {
	switcher := &mockModeSwitcher{currentMode: permission.SupervisedMode}

	enterTool := EnterPlanModeTool{Switcher: switcher}
	exitTool := ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode, PlansDir: t.TempDir()}

	enterTool.Execute(context.Background(), json.RawMessage(`{}`))
	exitTool.Execute(context.Background(), json.RawMessage(`{"plan":"plan content"}`))

	// Supervised → Plan → exit (no explicit mode) → should use default (supervised)
	if switcher.currentMode != permission.SupervisedMode {
		t.Errorf("mode = %v, want SupervisedMode", switcher.currentMode)
	}
}

// ---- Plan Persistence Tests ----

func TestExitPlanMode_PersistsPlan(t *testing.T) {
	switcher := &mockModeSwitcher{currentMode: permission.PlanMode}
	dir := t.TempDir()
	tool := ExitPlanModeTool{Switcher: switcher, PlansDir: dir}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"plan":"1. update foo\n2. run tests"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 plan file in %s, got %d", dir, len(entries))
	}
	if filepath.Ext(entries[0].Name()) != ".md" {
		t.Errorf("plan file name = %q, want .md extension", entries[0].Name())
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "1. update foo") {
		t.Errorf("plan file missing plan content: %q", string(data))
	}
	if !strings.Contains(result.Content, entries[0].Name()) {
		t.Errorf("result should reference saved plan file %q, got: %s", entries[0].Name(), result.Content)
	}
}

func TestExitPlanMode_PersistFailureNonFatal(t *testing.T) {
	switcher := &mockModeSwitcher{currentMode: permission.PlanMode}
	// PlansDir points at a regular file, so MkdirAll must fail.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := ExitPlanModeTool{Switcher: switcher, PlansDir: blocker}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"plan":"still works"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("persist failure must be non-fatal, got error: %s", result.Content)
	}
	if strings.Contains(result.Content, "Plan saved to") {
		t.Errorf("result must not claim the plan was saved: %s", result.Content)
	}
	if !strings.Contains(result.Content, "still works") {
		t.Errorf("plan content must still be returned: %s", result.Content)
	}
}

// ---- Parameter Schema Test ----

func TestExitPlanMode_Description(t *testing.T) {
	tool := ExitPlanModeTool{}
	desc := tool.Description()
	if !contains(desc, "Exit plan mode") {
		t.Errorf("description should mention exiting plan mode: %q", desc)
	}
	if contains(desc, "mode") && contains(desc, "restore") {
		t.Errorf("description should not expose internal mode restore logic: %q", desc)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
