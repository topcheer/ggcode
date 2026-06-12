package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ============================================================================
// Gate Ask rules - never block, only warn.
// The gate is a safety net for catastrophic (Block-level) commands only.
// Ask-level checks are logged but do not block.
// The agent-layer permission policy (config_policy.go) handles user
// confirmation via onApproval in supervised/auto mode.
// ============================================================================

func TestRunCommand_GateAskNeverBlocks(t *testing.T) {
	rc := RunCommand{}
	// The gate should never block Ask-level commands.
	// Agent-layer permission policy handles user confirmation.
	result, err := rc.Execute(context.Background(), json.RawMessage(
		`{"command":"echo safe"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("gate Ask should not block execution: %s", result.Content)
	}
}

func TestRunCommand_GateBlockStillBlocks(t *testing.T) {
	rc := RunCommand{}
	// Catastrophic (Block-level) commands are always blocked at the gate.
	result, err := rc.Execute(context.Background(), json.RawMessage(
		`{"command":"rm -rf /"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("catastrophic commands should always be blocked by gate")
	}
	if !strings.Contains(result.Content, "Command blocked:") {
		t.Errorf("expected gate block message, got: %s", result.Content)
	}
}

func TestRunCommand_GateWarningDoesNotInterfere(t *testing.T) {
	rc := RunCommand{}
	// git reset --hard is an Ask rule. Gate should warn but not block.
	result, err := rc.Execute(context.Background(), json.RawMessage(
		`{"command":"git reset --hard HEAD"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError && strings.Contains(result.Content, "Command blocked:") {
		t.Errorf("gate Ask should not block git reset: %s", result.Content)
	}
}
