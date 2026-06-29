package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/permission"
)

// modeSwitcher is implemented by permission policies that support
// runtime mode changes (e.g., *permission.ConfigPolicy).
type modeSwitcher interface {
	Mode() permission.PermissionMode
	SetMode(permission.PermissionMode)
}

// SwitchModeTool lets the LLM switch between permission modes at runtime.
type SwitchModeTool struct {
	policy permission.PermissionPolicy
}

// NewSwitchModeTool creates a switch_mode tool bound to the given policy.
// If the policy does not implement modeSwitcher (no SetMode), the tool
// registers as a no-op that always returns an error.
func NewSwitchModeTool(policy permission.PermissionPolicy) SwitchModeTool {
	return SwitchModeTool{policy: policy}
}

func (t SwitchModeTool) Name() string { return "switch_mode" }

func (t SwitchModeTool) Description() string {
	return "Switch the permission mode to control how tool calls are authorized. " +
		"Use this proactively when the situation calls for a different trust level.\n\n" +
		"Modes:\n" +
		"- supervised: Default. Asks for confirmation on most tools. Use when you want the user to review actions.\n" +
		"- plan: Read-only. Only allows read/search/inspect tools. Use for exploration and code review without modifications.\n" +
		"- auto: Allows safe operations, denies dangerous ones automatically. Good for routine coding tasks.\n" +
		"- bypass: Allows almost everything without asking. Use when the user has explicitly trusted the task.\n" +
		"- autopilot: Bypass permissions + continues autonomously when you would normally stop for input. Use for long-running autonomous tasks.\n\n" +
		"Guidelines:\n" +
		"- Default to supervised or auto unless the user requests otherwise.\n" +
		"- Switch to plan when exploring unfamiliar code to avoid accidental modifications.\n" +
		"- Only switch to bypass or autopilot when the user explicitly asks for it or the task clearly requires autonomous execution.\n" +
		"- This tool is always allowed in every mode (including plan mode)."
}

func (t SwitchModeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"mode": {
				"type": "string",
				"enum": ["supervised", "plan", "auto", "bypass", "autopilot"],
				"description": "The permission mode to switch to."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language."
			}
		},
		"required": ["mode", "description"]
	}`)
}

func (t SwitchModeTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	mode := strings.ToLower(strings.TrimSpace(args.Mode))
	if !permission.IsValidPermissionMode(mode) {
		return Result{IsError: true, Content: fmt.Sprintf("invalid mode %q. Valid modes: supervised, plan, auto, bypass, autopilot", args.Mode)}, nil
	}

	if t.policy == nil {
		return Result{IsError: true, Content: "no permission policy configured"}, nil
	}

	ms, ok := t.policy.(modeSwitcher)
	if !ok {
		return Result{IsError: true, Content: "the current permission policy does not support runtime mode switching"}, nil
	}

	oldMode := ms.Mode()
	newMode := permission.ParsePermissionMode(mode)

	if oldMode == newMode {
		return Result{Content: fmt.Sprintf("Already in %s mode.", newMode.String())}, nil
	}

	ms.SetMode(newMode)
	return Result{Content: fmt.Sprintf("Switched permission mode: %s → %s", oldMode.String(), newMode.String())}, nil
}
