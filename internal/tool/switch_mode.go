package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
)

// policyModeSwitcher is implemented by permission policies that support
// runtime mode changes (e.g., *permission.ConfigPolicy).
type policyModeSwitcher interface {
	Mode() permission.PermissionMode
	SetMode(permission.PermissionMode)
}

// SwitchModeTool lets the LLM switch between permission modes at runtime.
// It is registered in RegisterBuiltinTools with just the policy (works for
// headless mode). TUI and Desktop inject a ModeSwitcher via SetSwitcher()
// after registration so that the UI is notified of mode changes.
type SwitchModeTool struct {
	policy   permission.PermissionPolicy
	Switcher ModeSwitcher // optional; injected by TUI/Desktop after registration
}

// NewSwitchModeTool creates a switch_mode tool bound to the given policy.
func NewSwitchModeTool(policy permission.PermissionPolicy) *SwitchModeTool {
	return &SwitchModeTool{policy: policy}
}

// SetSwitcher injects a ModeSwitcher for UI notification. Called by
// TUI (replModeSwitcher) and Desktop after tool registration.
func (t *SwitchModeTool) SetSwitcher(s ModeSwitcher) {
	t.Switcher = s
}

func (t *SwitchModeTool) Name() string { return "switch_mode" }

func (t *SwitchModeTool) Description() string {
	return "Switch the permission mode to control how tool calls are authorized. " +
		"Modes: supervised (default — most calls flow, only extremely dangerous calls gated), bypass (nothing gated except system-level destructive commands; cruise control active). " +
		"Only use bypass when the user explicitly requests it. Always allowed in every mode. " +
		"Note: plan mode is tool-internal (enter_plan_mode/exit_plan_mode); 'auto' and 'autopilot' are legacy aliases that normalize to supervised/bypass respectively."
}

func (t *SwitchModeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"mode": {
				"type": "string",
				"enum": ["supervised", "bypass"],
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

func (t *SwitchModeTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	mode := strings.ToLower(strings.TrimSpace(args.Mode))
	// Two-mode refactor: users (and the LLM on their behalf) may only
	// switch between supervised and bypass. Legacy names auto/autopilot
	// normalize silently (config compat); plan is tool-internal only
	// (enter_plan_mode / exit_plan_mode).
	switch mode {
	case "auto":
		mode = "supervised"
	case "autopilot":
		mode = "bypass"
	case "plan":
		return Result{IsError: true, Content: "plan mode cannot be switched into directly; it is entered via the enter_plan_mode tool and exited via exit_plan_mode"}, nil
	case "supervised", "bypass":
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid mode %q. Valid modes: supervised, bypass", args.Mode)}, nil
	}

	newMode := permission.ParsePermissionMode(mode)

	// If a ModeSwitcher is injected (TUI/Desktop), use it — it handles both
	// policy update AND UI notification (modeChangeMsg / SetPermissionPolicy).
	if t.Switcher != nil {
		oldMode := t.Switcher.Mode()
		if oldMode == newMode {
			return Result{Content: fmt.Sprintf("Already in %s mode.", newMode.String())}, nil
		}
		t.Switcher.SetMode(newMode)
		debug.Log("permission", "switch_mode: %s → %s (via Switcher)", oldMode.String(), newMode.String())
		return Result{Content: fmt.Sprintf("Switched permission mode: %s → %s", oldMode.String(), newMode.String())}, nil
	}

	// Fallback: direct policy manipulation (daemon/pipe mode, no UI to notify).
	if t.policy == nil {
		return Result{IsError: true, Content: "no permission policy configured"}, nil
	}
	ms, ok := t.policy.(policyModeSwitcher)
	if !ok {
		return Result{IsError: true, Content: "the current permission policy does not support runtime mode switching"}, nil
	}
	oldMode := ms.Mode()
	if oldMode == newMode {
		return Result{Content: fmt.Sprintf("Already in %s mode.", newMode.String())}, nil
	}
	ms.SetMode(newMode)
	debug.Log("permission", "switch_mode: %s → %s (direct policy)", oldMode.String(), newMode.String())
	return Result{Content: fmt.Sprintf("Switched permission mode: %s → %s", oldMode.String(), newMode.String())}, nil
}
