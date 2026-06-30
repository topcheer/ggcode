package tool

import (
	"context"
	"encoding/json"
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
	for _, want := range []string{"multi_file_read", "LSP", "git", "web_fetch", "list_commands", "read_command_output", "wait_command", "Writes and shell execution are denied"} {
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
	tool := ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode}

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
	tool := ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode}

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
	exitTool := ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode}

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
	exitTool := ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode}

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
	exitTool := ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode}

	enterTool.Execute(context.Background(), json.RawMessage(`{}`))
	exitTool.Execute(context.Background(), json.RawMessage(`{"plan":"plan content"}`))

	// Supervised → Plan → exit (no explicit mode) → should use default (supervised)
	if switcher.currentMode != permission.SupervisedMode {
		t.Errorf("mode = %v, want SupervisedMode", switcher.currentMode)
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
