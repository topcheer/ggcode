package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// DesktopControlTool provides OS-level mouse, keyboard, window, and
// application control. On macOS it uses Swift CGEvent (mouse), AppleScript
// (keyboard/windows/apps) and the Accessibility API (UI tree). On Linux it
// uses xdotool/wmctrl (X11 only; UI-tree actions unsupported). On Windows
// it is not yet implemented.
type DesktopControlTool struct {
	WorkingDir string
}

func (DesktopControlTool) Name() string { return "desktop_control" }

func (DesktopControlTool) Description() string {
	return `Control the desktop OS: click, type, scroll, manage windows, and launch applications.

Use when you need to interact with native desktop applications outside the browser (e.g. Finder, System Settings, Xcode, terminal windows).

Actions:
  Mouse:   click, double_click, triple_click, right_click, move, drag, scroll, modifier_click, mouse_position
  Keyboard: type, key_press, key_combo
  Window:  list_windows, focus_window, close_window, minimize_window, maximize_window, set_window_bounds
  App:     launch_app, quit_app, list_apps, active_app, open (open URL/file with default or specified app)
  UI Tree: snapshot_ui (get accessibility tree of frontmost app), find_element (locate UI element by text)
  Combo:   find_and_click (find element by text and click it), wait_and_click (wait for element then click)
  Menus:   menu_select (click a menu bar item by path, e.g. "File > Export…")
  Display: display_info (get logical/physical resolution and scale factor for HiDPI/Retina)

For mouse actions, coordinates are in logical pixels (points) from top-left of the primary display.
For key_combo, use + to chain modifiers: "cmd+c", "ctrl+shift+tab", "cmd+shift+4".
Note: On Retina/HiDPI displays, screenshots are in physical pixels (2x). Use display_info to
get the scale factor and convert: logical = physical / scale.
snapshot_ui returns a JSON tree of UI elements (role, label, frame, enabled).
find_element searches the accessibility tree for elements matching the given text, returns coordinates.
find_and_click combines find_element + click in one step (reduces LLM coordinate reasoning).
menu_select clicks a menu bar item path like "File > Save" or "View > Sort By > Name" (macOS only).
modifier_click holds modifiers (e.g. "cmd", "shift", "cmd+shift") while clicking — for multi-select.
triple_click triple-clicks to select a paragraph/line.
mouse_position returns the current cursor location in logical pixels.
set_window_bounds positions and sizes a window: x,y = top-left; to_x,to_y = width,height.
open launches a URL or file path with the default handler app (use "app" to force a specific app).

Platform support: all actions on macOS; on Linux both X11 (xdotool/wmctrl required; UI-tree/display_info actions unsupported) and Wayland (ydotool + ydotoold required; move/click/drag/modifier_click/type/open/launch_app supported — scroll has no ydotool equivalent and window management has no Wayland protocol for external clients); core mouse/keyboard/app actions on Windows via SendInput.`
}

func (DesktopControlTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["click", "double_click", "triple_click", "right_click", "move", "drag", "scroll", "modifier_click", "mouse_position",
               "type", "key_press", "key_combo",
               "list_windows", "focus_window", "close_window", "minimize_window", "maximize_window", "set_window_bounds",
               "launch_app", "quit_app", "list_apps", "active_app", "open",
               "snapshot_ui", "find_element", "find_and_click", "wait_and_click",
               "menu_select", "display_info"],
      "description": "The desktop action to perform."
    },
    "x": {"type": "integer", "description": "X coordinate (logical pixels from left). Required for mouse position actions."},
    "y": {"type": "integer", "description": "Y coordinate (logical pixels from top). Required for mouse position actions."},
    "text": {"type": "string", "description": "Text to type (for 'type') or key combo string (for 'key_combo'/'key_press') or app name (for 'launch_app'/'quit_app'/'focus_window') or menu path like 'File > Save' (for 'menu_select') or modifiers like 'cmd+shift' (for 'modifier_click') or URL/file path (for 'open')."},
    "app": {"type": "string", "description": "Application name: to open the target with (for 'open'), or the app whose menu bar to use (for 'menu_select', default frontmost), or whose window to resize (for 'set_window_bounds', default frontmost)."},
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
	App       string `json:"app"`
	Button    string `json:"button"`
	Direction string `json:"direction"`
	Amount    int    `json:"amount"`
	ToX       int    `json:"to_x"`
	ToY       int    `json:"to_y"`
	Duration  int    `json:"duration"`
	MaxDepth  int    `json:"max_depth"`
	TimeoutMs int    `json:"timeout_ms"`
}

// normalizeModifiers splits a modifier spec like "cmd+shift" or "Ctrl + Option"
// into normalized canonical tokens (cmd, ctrl, alt, shift, fn). It rejects
// non-modifier tokens so typos surface as errors instead of silently
// clicking without the intended modifier.
func normalizeModifiers(spec string) ([]string, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, fmt.Errorf("empty modifier spec")
	}
	parts := strings.Split(spec, "+")
	mods := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "cmd", "command", "meta", "super", "win":
			if !seen["cmd"] {
				mods = append(mods, "cmd")
				seen["cmd"] = true
			}
		case "ctrl", "control":
			if !seen["ctrl"] {
				mods = append(mods, "ctrl")
				seen["ctrl"] = true
			}
		case "alt", "option", "opt":
			if !seen["alt"] {
				mods = append(mods, "alt")
				seen["alt"] = true
			}
		case "shift":
			if !seen["shift"] {
				mods = append(mods, "shift")
				seen["shift"] = true
			}
		case "fn":
			if !seen["fn"] {
				mods = append(mods, "fn")
				seen["fn"] = true
			}
		case "":
			return nil, fmt.Errorf("empty modifier component in %q", spec)
		default:
			return nil, fmt.Errorf("unknown modifier %q (supported: cmd, ctrl, alt, shift, fn)", p)
		}
	}
	return mods, nil
}

// parseMenuPath splits a menu path like "View > Sort By > Name" into its
// trimmed parts, dropping empty segments. Requires at least two parts
// (menu > item); deeper paths address nested submenus.
func parseMenuPath(path string) ([]string, error) {
	parts := strings.Split(path, ">")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) < 2 {
		return nil, fmt.Errorf("menu path %q must have at least 'Menu > Item'", path)
	}
	return out, nil
}

// ─── Linux Wayland backend (ydotool) argv builders ───
// These are pure functions with no build tag so they can be unit-tested on
// any platform. The linux-only executor (desktop_control_linux.go) consumes
// them. ydotool event codes follow linux/input-event-codes.h as used by the
// ydotool README: BTN_LEFT=0xC0, BTN_RIGHT=0xC1.

// ydoMoveArgs returns the argv to move the cursor absolutely.
func ydoMoveArgs(x, y int) []string {
	return []string{"ydotool", "move", "-a", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)}
}

// ydoClickArgs returns the argv to click `clicks` times. ydotool click takes
// a single event code, so multiple clicks need repeated invocations.
func ydoClickArgs(button string, clicks int) [][]string {
	code := "0xC0" // BTN_LEFT
	if button == "right" {
		code = "0xC1" // BTN_RIGHT
	}
	if clicks < 1 {
		clicks = 1
	}
	cmds := make([][]string, clicks)
	for i := range cmds {
		cmds[i] = []string{"ydotool", "click", code}
	}
	return cmds
}

// ydoKeyArgs converts a modifier token to ydotool press/release argv pairs
// using evdev key codes: KEY_LEFTCTRL=29, KEY_LEFTALT=56, KEY_LEFTSHIFT=42,
// KEY_LEFTMETA=125.
func ydoModifierKeyArgs(mod string) (press []string, release []string, ok bool) {
	var code int
	switch mod {
	case "ctrl":
		code = 29
	case "alt":
		code = 56
	case "shift":
		code = 42
	case "cmd":
		code = 125
	default:
		return nil, nil, false
	}
	p := fmt.Sprintf("%d:1", code)
	r := fmt.Sprintf("%d:0", code)
	return []string{"ydotool", "key", p}, []string{"ydotool", "key", r}, true
}

// ydoDragArgs returns the argv sequence for a drag: move to start, button
// down, move to end, button up.
func ydoDragArgs(x, y, toX, toY int) [][]string {
	return [][]string{
		ydoMoveArgs(x, y),
		{"ydotool", "click", "-d", "100", "0xC0"}, // down, 100ms hold
		ydoMoveArgs(toX, toY),
		{"ydotool", "click", "-u", "0xC0"}, // up
	}
}

// ydoTypeArgs returns the argv to type text (ydotool type).
func ydoTypeArgs(text string) []string {
	return []string{"ydotool", "type", text}
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
