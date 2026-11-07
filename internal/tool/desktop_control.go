package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type DesktopControlTool struct {
	WorkingDir string
}

func (t DesktopControlTool) Clone() Tool { return &DesktopControlTool{WorkingDir: t.WorkingDir} }

func (DesktopControlTool) Name() string { return "desktop_control" }

func (DesktopControlTool) Description() string {
	return `Control the desktop OS: click, type, scroll, manage windows, and launch applications.

Use when you need to interact with native desktop applications outside the browser (e.g. Finder, System Settings, Xcode, terminal windows).

Actions:
  Mouse:   click, double_click, triple_click, right_click, middle_click, move, drag, scroll, modifier_click, mouse_position, mouse_down, mouse_up
  Keyboard: type, key_press, key_combo, hold_key
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
mouse_down/mouse_up split a click into separate press and release calls — for
  long-press interactions and cross-call timing (e.g. press, drag via move, release).
hold_key presses a key/combo, holds it for duration_ms, then releases — e.g.
  hold_key "w" 1500ms in a game, or holding arrows for continuous scroll.
set_window_bounds positions and sizes a window: x,y = top-left; to_x,to_y = width,height. Target selection: macOS always the frontmost window; Linux/Windows target by 'text' (window title substring; Linux falls back to the active window when omitted, Windows requires a title match). The 'app' param is not yet honored for this action.
open launches a URL or file path with the default handler app (use "app" to force a specific app).

Platform support: all actions on macOS; on Linux both X11 (xdotool/wmctrl; UI-tree actions via AT-SPI python3-gi) and Wayland (ydotool + ydotoold for input; UI-tree actions via AT-SPI — display-server independent; scroll has no ydotool equivalent and window management has no Wayland protocol for external clients); on Windows mouse/keyboard/app plus window management (Win32: list/focus/close/minimize/maximize/set_window_bounds, display_info) via SendInput and EnumWindows — UI-tree and menu actions pending UI Automation. maximize_window is idempotent on every platform: a no-op when the window is already maximized/fullscreen, so retries never restore the window (#4058).`
}

func (DesktopControlTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["click", "double_click", "triple_click", "right_click", "middle_click", "move", "drag", "scroll", "modifier_click", "mouse_position", "mouse_down", "mouse_up",
               "type", "key_press", "key_combo", "hold_key",
               "list_windows", "focus_window", "close_window", "minimize_window", "maximize_window", "set_window_bounds",
               "launch_app", "quit_app", "list_apps", "active_app", "open",
               "snapshot_ui", "find_element", "find_and_click", "wait_and_click",
               "menu_select", "display_info"],
      "description": "The desktop action to perform."
    },
    "x": {"type": "integer", "description": "X coordinate (logical pixels from left). Required for mouse position actions."},
    "y": {"type": "integer", "description": "Y coordinate (logical pixels from top). Required for mouse position actions."},
    "text": {"type": "string", "description": "Text to type (for 'type') or key combo string (for 'key_combo'/'key_press') or app name (for 'launch_app'/'quit_app'/'focus_window') or menu path like 'File > Save' (for 'menu_select') or modifiers like 'cmd+shift' (for 'modifier_click') or URL/file path (for 'open') or window title substring (for 'set_window_bounds' on Linux/Windows). Key names: 'delete' is backspace on macOS but forward delete on Windows/Linux; use 'backspace' or 'forward-delete' (macOS only) for unambiguous behavior."},
    "app": {"type": "string", "description": "Application name: to open the target with (for 'open'), or the app whose menu bar to use (for 'menu_select', default frontmost). Not yet honored for 'set_window_bounds': target that action's window via 'text' (title substring) on Linux/Windows, or the frontmost window on macOS."},
    "button": {"type": "string", "enum": ["left", "right", "middle"], "default": "left", "description": "Mouse button for click actions."},
    "direction": {"type": "string", "enum": ["up", "down"], "default": "down", "description": "Scroll direction."},
    "amount": {"type": "integer", "default": 1, "minimum": 1, "description": "Scroll amount (number of steps)."},
    "to_x": {"type": "integer", "description": "Destination X for drag."},
    "to_y": {"type": "integer", "description": "Destination Y for drag."},
    "duration": {"type": "integer", "default": 0, "minimum": 0, "description": "Duration in milliseconds: drag animation time, or hold_key hold time."},
    "max_depth": {"type": "integer", "default": 8, "minimum": 1, "description": "Max depth for snapshot_ui accessibility tree traversal."},
    "timeout_ms": {"type": "integer", "default": 5000, "minimum": 1, "description": "Timeout for wait_and_click (polls for element)."}
  },
  "required": ["action"]
}`)
}

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

func holdKeyDurationClamp(ms int) (int, error) {
	if ms <= 0 {
		return 0, fmt.Errorf("hold_key requires positive duration (ms)")
	}
	if ms > 30000 {
		return 0, fmt.Errorf("hold_key duration %dms exceeds the 30s cap (use separate press/sleep/release for longer holds)", ms)
	}
	return ms, nil
}

func ydoClickCode(button string) (string, error) {
	switch button {
	case "", "left":
		return "0xC0", nil
	case "right":
		return "0xC1", nil
	case "middle":
		return "0xC2", nil
	default:
		return "", fmt.Errorf("unsupported button %q (supported: left, right, middle)", button)
	}
}

func ydoButtonMask(button string) (int, error) {
	switch button {
	case "", "left":
		return 0x0, nil
	case "right":
		return 0x1, nil
	case "middle":
		return 0x2, nil
	default:
		return 0, fmt.Errorf("unsupported button %q (supported: left, right, middle)", button)
	}
}

func xdLiteralPattern(p string) string {
	return regexp.QuoteMeta(strings.TrimSpace(p))
}

func ydoMoveArgs(x, y int) []string {
	return []string{"ydotool", "move", "-a", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)}
}

func ydoClickArgs(button string, clicks int) ([][]string, error) {

	button = strings.ToLower(strings.TrimSpace(button))
	if button == "" {
		button = "left"
	}
	if button != "left" && button != "right" && button != "middle" {
		return nil, fmt.Errorf("unknown button %q (left, right, middle)", button)
	}
	code := "0xC0"
	if button == "right" {
		code = "0xC1"
	}
	if button == "middle" {
		code = "0xC2"
	}
	if clicks < 1 {
		clicks = 1
	}
	cmds := make([][]string, clicks)
	for i := range cmds {
		cmds[i] = []string{"ydotool", "click", code}
	}
	return cmds, nil
}

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

func ydoDragArgs(x, y, toX, toY int) [][]string {
	return [][]string{
		ydoMoveArgs(x, y),
		{"ydotool", "click", "0x40"},
		ydoMoveArgs(toX, toY),
		{"ydotool", "click", "0x80"},
	}
}

func ydoTypeArgs(text string) []string {
	return []string{"ydotool", "type", text}
}

func windowTitleMatches(title, needle string) bool {
	if strings.TrimSpace(needle) == "" {
		return false
	}
	return strings.Contains(strings.ToLower(title), strings.ToLower(needle))
}

type ATSPIElement struct {
	Role string `json:"role"`
	Name string `json:"name"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
}

func atspiCenter(e ATSPIElement) (int, int) {
	return e.X + e.W/2, e.Y + e.H/2
}

func atspiFind(els []ATSPIElement, needle string) []ATSPIElement {
	var out []ATSPIElement
	for _, e := range els {
		if e.Name != "" && windowTitleMatches(e.Name, needle) {
			out = append(out, e)
		}
	}
	return out
}

func atspiFormat(matches []ATSPIElement) string {
	var sb strings.Builder
	for _, e := range matches {
		cx, cy := atspiCenter(e)
		fmt.Fprintf(&sb, "%s %q at (%d,%d) size %dx%d\n", e.Role, e.Name, cx, cy, e.W, e.H)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (t DesktopControlTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params desktopParams
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{}, fmt.Errorf("parse params: %w", err)
	}
	clampDesktopDefaults(&params)
	// #5209 D4: same Execute-level errcode choke point as the browser tool
	// (narrow scope: browser + desktop_control only).
	res, err := executeDesktopControl(ctx, params)
	if err != nil {
		return res, err
	}
	return annotateErrcode(res), nil
}

func clampDesktopDefaults(params *desktopParams) {
	if params.Button == "" {
		params.Button = "left"
	}
	if params.Direction == "" {
		params.Direction = "down"
	}
	if params.Amount < 1 {
		params.Amount = 1
	}
	if params.MaxDepth < 1 {
		params.MaxDepth = 8
	}
	if params.TimeoutMs < 1 {
		params.TimeoutMs = 5000
	}
}

func unknownAlphaKeyName(key string) bool {
	if len(key) <= 1 {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

func xdModifierName(m string) (string, bool) {
	switch m {
	case "cmd":
		return "super", true
	case "ctrl", "alt", "shift", "super":
		return m, true
	default:
		return "", false
	}
}
