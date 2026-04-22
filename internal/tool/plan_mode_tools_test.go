package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

func TestEnterPlanMode_Basic(t *testing.T) {
	ep := EnterPlanModeTool{Switcher: noopModeSwitcher{}}
	result, err := ep.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestExitPlanMode_Basic(t *testing.T) {
	ep := ExitPlanModeTool{Switcher: noopModeSwitcher{}, DefaultMode: 0}
	input, _ := json.Marshal(map[string]interface{}{
		"plan": "1. Step one\n2. Step two",
	})
	result, err := ep.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestExitPlanMode_MissingPlan(t *testing.T) {
	ep := ExitPlanModeTool{Switcher: noopModeSwitcher{}, DefaultMode: 0}
	result, err := ep.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing plan")
	}
}

type noopModeSwitcher struct{}

func (noopModeSwitcher) SetMode(permission.PermissionMode) {}
