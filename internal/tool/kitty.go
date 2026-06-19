package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// KittyTool lets the agent manage Kitty terminal windows, tabs, and panes
// when ggcode is running inside Kitty.
//
// Kitty has a first-class remote control protocol accessible via the
// `kitten @` (or `kitty @`) CLI. This provides:
//   - Structured JSON output (kitten @ ls)
//   - Per-window targeting by numeric ID (--match=id:N)
//   - Screen content retrieval (kitten @ get-text)
//   - Native command execution (kitten @ launch -- CMD)
//   - Full tab/window/layout management
//
// Unlike Ghostty and Warp which require platform-specific implementations
// (AppleScript on macOS, DBus on Linux, menu clicking on macOS), Kitty's
// remote control CLI works identically on all platforms.
//
// See kitty_impl.go for the implementation.
type KittyTool struct {
	WorkingDir string
}

func NewKittyTool(workingDir string) *KittyTool {
	return &KittyTool{WorkingDir: workingDir}
}

func (k *KittyTool) Clone() Tool {
	if k == nil {
		return NewKittyTool("")
	}
	return &KittyTool{WorkingDir: k.WorkingDir}
}

func (k *KittyTool) Name() string { return "kitty" }

func (k *KittyTool) Description() string {
	return "Manage Kitty terminal windows, tabs, and panes when running inside Kitty: inspect status, list windows/tabs as JSON, create splits/tabs/windows with optional commands, focus/close/resize windows by ID, send text input, send key events, get screen text contents, manage tab titles, perform arbitrary Kitty actions (zoom, scroll, layout switching), and reload config. Kitty's remote control protocol provides structured JSON output and per-window targeting on ALL platforms. Use this when the user asks to manage terminal panes in Kitty."
}

func (k *KittyTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["status", "list", "split", "new_tab", "new_window", "focus", "close", "close_tab", "select_tab", "input", "send_key", "resize", "get_text", "zoom", "set_tab_title", "action", "reload_config"],
				"description": "Kitty action to perform. status shows detection info; list shows all windows/tabs as JSON; split creates a new split pane; new_tab/new_window create new surfaces; focus navigates to a window by ID; close removes a window by ID; close_tab closes the current tab; select_tab switches tab by 1-based index; input sends text to a window; send_key sends a keyboard event; resize changes window dimensions; get_text retrieves screen contents (unique to kitty); zoom toggles stack layout; set_tab_title renames the current tab; action performs any Kitty action string; reload_config reloads kitty.conf."
			},
			"direction": {
				"type": "string",
				"enum": ["right", "left", "down", "up"],
				"description": "Split direction for 'split' action. right/left create a vertical split; down/up create a horizontal split. Default: right."
			},
			"size": {
				"type": "integer",
				"description": "Size percentage (1-99) for the new pane after 'split'. E.g. size=30 means the new pane occupies 30%% of the space. 0 or omitted means 50/50 split. Uses kitty's --bias parameter.",
				"minimum": 0,
				"maximum": 99
			},
			"command": {
				"type": "string",
				"description": "Command to run in the new split/tab/window. When set, the surface launches with this command via 'kitten @ launch -- CMD'."
			},
			"working_dir": {
				"type": "string",
				"description": "Working directory for the new split/tab/window. Defaults to the current working directory."
			},
			"window_id": {
				"type": "integer",
				"description": "Target window ID for focus, close, input, send_key, resize, get_text. Use 'list' to find IDs. Omit to target the current focused window."
			},
			"text": {
				"type": "string",
				"description": "Text to send (for 'input' action) or action name (for 'action' action, e.g. 'scroll_page_down', 'next_tab') or tab title (for 'set_tab_title' action)."
			},
			"key": {
				"type": "string",
				"description": "Key name for send_key (e.g. 'enter', 'c', 'space', 'escape', 'tab', 'backspace', 'up', 'down', 'left', 'right', 'home', 'end', 'pageup', 'pagedown')."
			},
			"modifiers": {
				"type": "string",
				"description": "Comma-separated modifier keys for send_key: shift, control, alt, super (cmd on macOS). Example: 'control' for Ctrl+C."
			},
			"axis": {
				"type": "string",
				"enum": ["horizontal", "vertical"],
				"description": "Resize axis for 'resize' action. Default: horizontal."
			},
			"increment": {
				"type": "integer",
				"description": "Pixel increment for 'resize' action. Positive to grow, negative to shrink. Default: 20.",
				"minimum": -500,
				"maximum": 500
			},
			"tab_index": {
				"type": "integer",
				"description": "1-based tab index for select_tab action."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Splitting Kitty pane', '列出 Kitty 面板'). You MUST always provide this field."
			}
		},
		"required": ["action", "description"]
	}`)
}

func (k *KittyTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action     string `json:"action"`
		Direction  string `json:"direction"`
		Size       int    `json:"size"`
		Command    string `json:"command"`
		WorkingDir string `json:"working_dir"`
		WindowID   int    `json:"window_id"`
		Text       string `json:"text"`
		Key        string `json:"key"`
		Modifiers  string `json:"modifiers"`
		Axis       string `json:"axis"`
		Increment  int    `json:"increment"`
		TabIndex   int    `json:"tab_index"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		return Result{IsError: true, Content: "action is required"}, nil
	}

	// Status doesn't require Kitty to be running — it reports detection.
	if action == "status" {
		return k.executeStatus(), nil
	}

	if !kittyAvailable() {
		return Result{IsError: true, Content: "Kitty is not detected (TERM_PROGRAM != kitty). This tool only works when ggcode runs inside the Kitty terminal emulator."}, nil
	}

	switch action {
	case "list":
		return k.executeList(), nil
	case "split":
		return k.executeSplit(args.WindowID, args.Direction, args.Size, args.Command, args.WorkingDir), nil
	case "new_tab":
		return k.executeNewTab(args.Command, args.WorkingDir), nil
	case "new_window":
		return k.executeNewWindow(args.Command, args.WorkingDir), nil
	case "focus":
		return k.executeFocus(args.WindowID), nil
	case "close":
		return k.executeClose(args.WindowID), nil
	case "close_tab":
		return k.executeCloseTab(), nil
	case "select_tab":
		return k.executeSelectTab(args.TabIndex), nil
	case "input":
		return k.executeInput(args.WindowID, args.Text), nil
	case "send_key":
		return k.executeSendKey(args.WindowID, args.Key, args.Modifiers), nil
	case "resize":
		return k.executeResize(args.WindowID, args.Axis, args.Increment), nil
	case "get_text":
		return k.executeGetText(args.WindowID), nil
	case "zoom":
		return k.executeZoom(args.WindowID), nil
	case "set_tab_title":
		return k.executeSetTabTitle(args.Text), nil
	case "action":
		return k.executeAction(args.WindowID, args.Text), nil
	case "reload_config":
		return k.executeReloadConfig(), nil
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported kitty action %q", args.Action)}, nil
	}
}

// ── Detection (shared) ─────────────────────────────────────────────────────

func kittyAvailable() bool {
	// Kitty sets TERM_PROGRAM=kitty, but this can be lost when running
	// through tmux, screen, or other wrappers. KITTY_WINDOW_ID is always
	// set by kitty and is a reliable fallback.
	return os.Getenv("TERM_PROGRAM") == "kitty" || os.Getenv("KITTY_WINDOW_ID") != ""
}

// ── Utility (shared) ────────────────────────────────────────────────────────

func (k *KittyTool) workingDir() string {
	if k != nil && strings.TrimSpace(k.WorkingDir) != "" {
		return k.WorkingDir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
