package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/permission"
)

// ModeSwitcher switches the agent's permission mode (plan ↔ supervised etc).
type ModeSwitcher interface {
	SetMode(mode permission.PermissionMode)
}

// ————————————————————————————————————————
// EnterPlanMode
// ————————————————————————————————————————

type EnterPlanModeTool struct {
	Switcher ModeSwitcher
}

func (t EnterPlanModeTool) Name() string { return "enter_plan_mode" }
func (t EnterPlanModeTool) Description() string {
	return "Switch into plan mode. In plan mode, only read-only tools (read_file, search_files, grep, glob, list_directory) are available. " +
		"Explore the codebase and design an approach, then call exit_plan_mode to present the plan for approval. " +
		"Prefer this for non-trivial implementation tasks to get user alignment before writing code."
}
func (t EnterPlanModeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}
func (t EnterPlanModeTool) Execute(_ context.Context, _ json.RawMessage) (Result, error) {
	if t.Switcher == nil {
		return Result{IsError: true, Content: "enter_plan_mode: mode switcher not available"}, nil
	}
	t.Switcher.SetMode(permission.PlanMode)
	return Result{Content: "Entered plan mode. All tools are now read-only. Explore the codebase, design your approach, then call exit_plan_mode with the plan.\n"}, nil
}

// ————————————————————————————————————————
// ExitPlanMode
// ————————————————————————————————————————

type ExitPlanModeTool struct {
	Switcher    ModeSwitcher
	DefaultMode permission.PermissionMode
}

func (t ExitPlanModeTool) Name() string { return "exit_plan_mode" }
func (t ExitPlanModeTool) Description() string {
	return "Exit plan mode and return to normal coding mode. Provide the plan content generated during exploration. " +
		"Optionally specify which mode to return to (supervised, auto, bypass, autopilot)."
}
func (t ExitPlanModeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"plan": {"type": "string", "description": "The implementation plan content generated during plan mode"},
			"mode": {"type": "string", "enum": ["supervised", "auto", "bypass", "autopilot"], "description": "Permission mode to switch back to (default: supervised)"}
		},
		"required": ["plan"]
	}`)
}
func (t ExitPlanModeTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Switcher == nil {
		return Result{IsError: true, Content: "exit_plan_mode: mode switcher not available"}, nil
	}
	var args struct {
		Plan string `json:"plan"`
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.Plan == "" {
		return Result{IsError: true, Content: "plan content is required"}, nil
	}

	mode := t.DefaultMode
	if args.Mode != "" {
		if !permission.IsValidPermissionMode(args.Mode) {
			return Result{IsError: true, Content: fmt.Sprintf("unknown mode %q (use supervised, plan, auto, bypass, or autopilot)", args.Mode)}, nil
		}
		mode = permission.ParsePermissionMode(args.Mode)
	}

	t.Switcher.SetMode(mode)

	return Result{Content: fmt.Sprintf("Exited plan mode. Resumed in %s mode.\n\nPlan:\n%s\n", mode, args.Plan)}, nil
}
