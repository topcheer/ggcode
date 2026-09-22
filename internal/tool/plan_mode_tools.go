package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
)

// ModeSwitcher switches the agent's permission mode and remembers the
// previous mode so that exit_plan_mode can restore it.
type ModeSwitcher interface {
	// Mode returns the current permission mode.
	Mode() permission.PermissionMode
	// SetMode switches to the given mode and notifies the UI.
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
	return "Switch into plan mode for read-only codebase exploration. Only read-only tools (file/search/LSP/git inspection/web) are allowed; writes and shell execution are denied. Explore and design an approach, then call exit_plan_mode to present the plan for approval."
}
func (t EnterPlanModeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"description"
	]
}`)
}
func (t EnterPlanModeTool) Execute(_ context.Context, _ json.RawMessage) (Result, error) {
	if t.Switcher == nil {
		return Result{IsError: true, Content: "enter_plan_mode: mode switcher not available"}, nil
	}

	// Remember the current mode (by asking the switcher what it has)
	// before switching to plan mode, so exit_plan_mode can restore it.
	// #858: RememberMode saves its ARGUMENT as the mode to restore — passing
	// the TARGET mode meant plan->exit always restored the hardcoded fallback
	// on daemon/desktop. Query the real current mode instead.
	current := t.Switcher.Mode()
	previous := t.Switcher.RememberMode(current)

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
	// PlansDir overrides where plans are persisted. Empty means the
	// default: .ggcode/plans under the current working directory.
	PlansDir string
}

func (t ExitPlanModeTool) Name() string { return "exit_plan_mode" }
func (t ExitPlanModeTool) Description() string {
	return "Exit plan mode and return to normal coding mode. Provide the plan content generated during exploration. " +
		"The plan is persisted to .ggcode/plans/ so it survives compaction and session restarts and can be referenced in later sessions. " +
		"After exiting, break the plan into structured tasks using task_create with dependencies (addBlocks/addBlockedBy) " +
		"to track progress, then execute each task step by step."
}
func (t ExitPlanModeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"plan": {
			"type": "string",
			"description": "The implementation plan content generated during plan mode"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"plan",
		"description"
	]
}`)
}
func (t ExitPlanModeTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Switcher == nil {
		return Result{IsError: true, Content: "exit_plan_mode: mode switcher not available"}, nil
	}
	var args struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.Plan == "" {
		return Result{IsError: true, Content: "plan content is required"}, nil
	}
	// #1697 case 3: guard on PlanMode - without it, a call OUTSIDE any plan
	// period still "restored" (previous plan cycle's Supervised, silently
	// DOWNGRADING a user who had switched to bypass in between; or the
	// zero-value "" for a first-ever call, feeding SetMode an invalid
	// mode).
	if t.Switcher.Mode() != permission.PlanMode {
		return Result{IsError: true, Content: "exit_plan_mode: not in plan mode (nothing to exit)"}, nil
	}

	// Always restore the mode from before entering plan mode.
	mode := t.Switcher.RestoreMode(t.DefaultMode)

	t.Switcher.SetMode(mode)

	result := fmt.Sprintf("Exited plan mode. Resumed in %s mode.\n\nPlan:\n%s\n\nUse task_create to break this plan into structured tasks with dependencies, then execute step by step.\n", mode, args.Plan)
	if savedPath := t.persistPlan(args.Plan); savedPath != "" {
		result += fmt.Sprintf("\nPlan saved to %s — it survives compaction and session restarts; reference it in later sessions if work is interrupted.\n", savedPath)
	}
	return Result{Content: result}, nil
}

// persistPlan writes the approved plan to a markdown file so it becomes a
// durable, resumable artifact instead of living only in the conversation.
// ESEM 2026 (arXiv:2608.04661) found 91.8% of agent plan files are ephemeral
// single-commit artifacts; persisting on approval closes that gap. Failure is
// non-fatal: the plan is still returned in the tool result.
func (t ExitPlanModeTool) persistPlan(plan string) string {
	dir := t.PlansDir
	if dir == "" {
		dir = filepath.Join(".ggcode", "plans")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		debug.Log("tool", "exit_plan_mode: create plans dir %s: %v", dir, err)
		return ""
	}
	name := fmt.Sprintf("plan-%s.md", time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	content := fmt.Sprintf("# Plan — %s\n\n%s\n", time.Now().Format("2006-01-02 15:04:05"), plan)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		debug.Log("tool", "exit_plan_mode: write plan %s: %v", path, err)
		return ""
	}
	return path
}
