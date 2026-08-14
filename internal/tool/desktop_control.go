package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// DesktopControlTool provides OS-level mouse, keyboard, window, and
// application control. On macOS it uses AppleScript + Quartz events
// via cliclick (auto-installed if missing). On Linux it uses xdotool.
// On Windows it uses PowerShell + SendInput.
type DesktopControlTool struct {
	WorkingDir string
}

func (DesktopControlTool) Name() string { return "desktop_control" }

func (DesktopControlTool) Description() string {
	return `Control the desktop OS: click, type, scroll, manage windows, and launch applications.

Use when you need to interact with native desktop applications outside the browser (e.g. Finder, System Settings, Xcode, terminal windows).

Actions:
  Mouse:   click, double_click, right_click, move, drag, scroll
  Keyboard: type, key_press, key_combo
  Window:  list_windows, focus_window, close_window, minimize_window, maximize_window
  App:     launch_app, quit_app, list_apps, active_app
  UI Tree: snapshot_ui (get accessibility tree of frontmost app), find_element (locate UI element by text)
  Combo:   find_and_click (find element by text and click it), wait_and_click (wait for element then click)
  Display: display_info (get logical/physical resolution and scale factor for HiDPI/Retina)

For mouse actions, coordinates are in logical pixels (points) from top-left of the primary display.
For key_combo, use + to chain modifiers: "cmd+c", "ctrl+shift+tab", "cmd+shift+4".
Note: On Retina/HiDPI displays, screenshots are in physical pixels (2x). Use display_info to
get the scale factor and convert: logical = physical / scale.
snapshot_ui returns a JSON tree of UI elements (role, label, frame, enabled).
find_element searches the accessibility tree for elements matching the given text, returns coordinates.
find_and_click combines find_element + click in one step (reduces LLM coordinate reasoning).`
}

func (DesktopControlTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["click", "double_click", "right_click", "move", "drag", "scroll",
               "type", "key_press", "key_combo",
               "list_windows", "focus_window", "close_window", "minimize_window", "maximize_window",
               "launch_app", "quit_app", "list_apps", "active_app",
               "snapshot_ui", "find_element", "find_and_click", "wait_and_click",
               "display_info"],
      "description": "The desktop action to perform."
    },
    "x": {"type": "integer", "description": "X coordinate (logical pixels from left). Required for mouse position actions."},
    "y": {"type": "integer", "description": "Y coordinate (logical pixels from top). Required for mouse position actions."},
    "text": {"type": "string", "description": "Text to type (for 'type') or key combo string (for 'key_combo'/'key_press') or app name (for 'launch_app'/'quit_app'/'focus_window')."},
    "button": {"type": "string", "enum": ["left", "right"], "default": "left", "description": "Mouse button for click actions."},
    "direction": {"type": "string", "enum": ["up", "down"], "default": "down", "description": "Scroll direction."},
    "amount": {"type": "integer", "default": 1, "description": "Scroll amount (number of steps)."},
    "to_x": {"type": "integer", "description": "Destination X for drag."},
    "to_y": {"type": "integer", "description": "Destination Y for drag."},
    "duration": {"type": "integer", "default": 0, "description": "Duration in milliseconds for drag animation."},
    "max_depth": {"type": "integer", "default": 8, "description": "Max depth for snapshot_ui accessibility tree traversal."},
    "timeout_ms": {"type": "integer", "default": 5000, "description": "Timeout for wait_and_click (polls for element)."}
  },
  "required": ["action"]
}`)
}

// desktopParams holds parsed parameters for desktop_control.
type desktopParams struct {
	Action    string `json:"action"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Text      string `json:"text"`
	Button    string `json:"button"`
	Direction string `json:"direction"`
	Amount    int    `json:"amount"`
	ToX       int    `json:"to_x"`
	ToY       int    `json:"to_y"`
	Duration  int    `json:"duration"`
	MaxDepth  int    `json:"max_depth"`
	TimeoutMs int    `json:"timeout_ms"`
}

func (t DesktopControlTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params desktopParams
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{}, fmt.Errorf("parse params: %w", err)
	}
	if params.Button == "" {
		params.Button = "left"
	}
	if params.Direction == "" {
		params.Direction = "down"
	}
	if params.Amount == 0 {
		params.Amount = 1
	}
	if params.MaxDepth == 0 {
		params.MaxDepth = 8
	}
	if params.TimeoutMs == 0 {
		params.TimeoutMs = 5000
	}
	return executeDesktopControl(ctx, params)
}
