package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/permission"
)

// ModeSwitcher switches the agent's permission mode and remembers the
// previous mode so that exit_plan_mode can restore it.
type ModeSwitcher interface {
	SetMode(mode permission.PermissionMode)
	// RememberMode saves the given mode as the "previous" mode so it
	// can be restored by a later mode switch. Returns the mode that
	// was previously remembered (SupervisedMode if none).
	RememberMode(mode permission.PermissionMode) permission.PermissionMode
	// RestoreMode returns the remembered mode, or the given fallback.
	RestoreMode(fallback permission.PermissionMode) permission.PermissionMode
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

	// Remember the current mode (by asking the switcher what it has)
	// before switching to plan mode, so exit_plan_mode can restore it.
	previous := t.Switcher.RememberMode(permission.PlanMode)

	t.Switcher.SetMode(permission.PlanMode)

	modeInfo := ""
	if previous != permission.PlanMode && previous != permission.SupervisedMode {
		modeInfo = fmt.Sprintf(" (will restore %s mode on exit)", previous)
	}

	return Result{Content: fmt.Sprintf("Entered plan mode%s. All tools are now read-only. Explore the codebase, design your approach, then call exit_plan_mode with the plan.\n", modeInfo)}, nil
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
		"Optionally specify which mode to return to (supervised, auto, bypass, autopilot). If not specified, " +
		"restores the mode that was active before entering plan mode. " +
		"After exiting, break the plan into structured tasks using task_create with dependencies (addBlocks/addBlockedBy) " +
		"to track progress, then execute each task step by step."
}
func (t ExitPlanModeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"plan": {"type": "string", "description": "The implementation plan content generated during plan mode"},
				"mode": {"type": "string", "enum": ["supervised", "auto", "bypass", "autopilot"], "description": "Permission mode to switch back to. If omitted, restores the mode from before entering plan mode."}
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

	var mode permission.PermissionMode
	if args.Mode != "" {
		if !permission.IsValidPermissionMode(args.Mode) {
			return Result{IsError: true, Content: fmt.Sprintf("unknown mode %q (use supervised, plan, auto, bypass, or autopilot)", args.Mode)}, nil
		}
		mode = permission.ParsePermissionMode(args.Mode)
	} else {
		// Restore the mode from before entering plan mode.
		// Fall back to DefaultMode if nothing was remembered.
		mode = t.Switcher.RestoreMode(t.DefaultMode)
	}

	t.Switcher.SetMode(mode)

	return Result{Content: fmt.Sprintf("Exited plan mode. Resumed in %s mode.\n\nPlan:\n%s\n\nUse task_create to break this plan into structured tasks with dependencies, then execute step by step.\n", mode, args.Plan)}, nil
}
