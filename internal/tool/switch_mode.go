package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
		"Modes: supervised (default, asks confirmation), plan (read-only), auto (safe ops auto-allowed), bypass (almost everything), autopilot (bypass + autonomous). " +
		"Default to supervised or auto; only use bypass/autopilot when the user explicitly requests it. Always allowed in every mode."
}

func (t *SwitchModeTool) Parameters() json.RawMessage {
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

func (t *SwitchModeTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
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

	newMode := permission.ParsePermissionMode(mode)

	// If a ModeSwitcher is injected (TUI/Desktop), use it — it handles both
	// policy update AND UI notification (modeChangeMsg / SetPermissionPolicy).
	if t.Switcher != nil {
		oldMode := t.Switcher.Mode()
		if oldMode == newMode {
			return Result{Content: fmt.Sprintf("Already in %s mode.", newMode.String())}, nil
		}
		t.Switcher.SetMode(newMode)
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
	return Result{Content: fmt.Sprintf("Switched permission mode: %s → %s", oldMode.String(), newMode.String())}, nil
}
