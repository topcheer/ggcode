package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

func TestSwitchModeTool(t *testing.T) {
	policy := permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.SupervisedMode)
	tool := NewSwitchModeTool(policy)

	// Switch from supervised to auto
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"mode":"auto","description":"switch to auto"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if policy.Mode() != permission.AutoMode {
		t.Fatalf("expected auto mode, got %s", policy.Mode())
	}

	// Switch to autopilot
	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"mode":"autopilot","description":"switch to autopilot"}`))
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if policy.Mode() != permission.AutopilotMode {
		t.Fatalf("expected autopilot mode, got %s", policy.Mode())
	}

	// Switch to same mode (idempotent)
	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"mode":"autopilot","description":"already autopilot"}`))
	if result.IsError {
		t.Fatalf("expected success on same mode, got error: %s", result.Content)
	}

	// Invalid mode
	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"mode":"yolo","description":"invalid"}`))
	if !result.IsError {
		t.Fatal("expected error for invalid mode")
	}

	// Missing mode
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
	if policy.Mode() != permission.AutoMode {
		t.Fatalf("expected auto mode, got %s", policy.Mode())
	}
}
