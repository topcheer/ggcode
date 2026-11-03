package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

func TestSwitchModeTool(t *testing.T) {
	policy := permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.SupervisedMode)
	tool := NewSwitchModeTool(policy)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"mode":"auto","description":"switch to auto"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if policy.Mode() != permission.SupervisedMode {
		t.Fatalf("expected supervised mode, got %s", policy.Mode())
	}
	if !strings.Contains(result.Content, "supervised") {
		t.Fatalf("expected normalized mode name in message, got: %s", result.Content)
	}

	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"mode":"autopilot","description":"switch to autopilot"}`))
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if policy.Mode() != permission.BypassMode {
		t.Fatalf("expected bypass mode, got %s", policy.Mode())
	}
	if !strings.Contains(result.Content, "bypass") {
		t.Fatalf("expected normalized mode name in message, got: %s", result.Content)
	}

	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"mode":"plan","description":"switch to plan"}`))
	if !result.IsError {
		t.Fatal("expected error for plan mode")
	}
	if !strings.Contains(result.Content, "plan mode cannot be switched into directly") {
		t.Fatalf("expected plan-mode rejection message, got: %s", result.Content)
	}
	if policy.Mode() != permission.BypassMode {
		t.Fatalf("expected mode unchanged after plan rejection, got %s", policy.Mode())
	}

	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"mode":"autopilot","description":"already autopilot"}`))
	if result.IsError {
		t.Fatalf("expected success on same mode, got error: %s", result.Content)
	}

	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"mode":"yolo","description":"invalid"}`))
	if !result.IsError {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(result.Content, "Valid modes: supervised, bypass") {
		t.Fatalf("expected two-mode validation message, got: %s", result.Content)
	}

	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"description":"missing mode"}`))
	if !result.IsError {
		t.Fatal("expected error for missing mode")
	}
}

func TestSwitchModeToolNoPolicy(t *testing.T) {
	tool := NewSwitchModeTool(nil)
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"mode":"auto","description":"test"}`))
	if !result.IsError {
		t.Fatal("expected error when policy is nil")
	}
}

func TestSwitchModeToolCaseInsensitive(t *testing.T) {
	policy := permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.SupervisedMode)
	tool := NewSwitchModeTool(policy)

	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"mode":"AUTO","description":"uppercase"}`))
	if result.IsError {
		t.Fatalf("expected success with uppercase mode, got: %s", result.Content)
	}
	if policy.Mode() != permission.SupervisedMode {
		t.Fatalf("expected supervised mode, got %s", policy.Mode())
	}
}

func TestSwitchModeToolWithSwitcher(t *testing.T) {
	policy := permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.SupervisedMode)
	tool := NewSwitchModeTool(policy)
	switcher := &mockModeSwitcher{currentMode: permission.SupervisedMode}
	tool.SetSwitcher(switcher)

	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"mode":"auto","description":"switch"}`))
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if switcher.currentMode != permission.SupervisedMode {
		t.Fatalf("expected switcher mode to be supervised, got %s", switcher.currentMode)
	}

	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"mode":"auto","description":"same"}`))
	if result.IsError {
		t.Fatalf("expected success on same mode, got error: %s", result.Content)
	}
	if switcher.currentMode != permission.SupervisedMode {
		t.Fatalf("expected switcher mode to still be supervised, got %s", switcher.currentMode)
	}
}
