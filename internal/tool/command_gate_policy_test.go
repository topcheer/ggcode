package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunCommand_GateAskNeverBlocks(t *testing.T) {
	rc := RunCommand{}

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
