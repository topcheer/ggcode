package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// GhosttyTool lets the agent manage Ghostty terminal panes, tabs, and windows
// when ggcode is running inside Ghostty.
//
// Platform support:
//   - macOS: Uses the Ghostty AppleScript API (via osascript)
//   - Linux: Uses GIO DBus Actions (via gdbus) for IPC with the Ghostty GTK app
//
// See ghostty_actions_darwin.go and ghostty_actions_linux.go for implementations.
type GhosttyTool struct {
	WorkingDir string
}

func NewGhosttyTool(workingDir string) *GhosttyTool {
	return &GhosttyTool{WorkingDir: workingDir}
}

func (g *GhosttyTool) Clone() Tool {
	if g == nil {
		return NewGhosttyTool("")
	}
	return &GhosttyTool{WorkingDir: g.WorkingDir}
}

func (g *GhosttyTool) Name() string { return "ghostty" }

func (g *GhosttyTool) Description() string {
	return "Manage Ghostty terminal panes, tabs, and windows when running inside Ghostty: inspect status, list surfaces, create splits with optional commands, run commands in panes, focus/navigate between splits, send text input, send key events, and trigger Ghostty actions (zoom, resize, equalize, etc). Use this when the user asks to manage terminal panes in Ghostty."
}

func (g *GhosttyTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["status", "list", "split", "new_tab", "new_window", "focus", "close", "input", "send_key", "action", "zoom", "equalize", "select_tab", "reload_config"],
				"description": "Ghostty action to perform. status shows detection info; list shows all windows/tabs/terminals; split creates a new split pane; new_tab/new_window create new surfaces; focus navigates to a terminal; close removes a terminal; input sends text to a terminal; send_key sends a keyboard event; action performs any Ghostty action string; zoom toggles split zoom; equalize balances split sizes; select_tab switches tab by index; reload_config reloads Ghostty configuration."
			},
			"direction": {
				"type": "string",
				"enum": ["right", "left", "down", "up"],
				"description": "Split direction for 'split' action. Default: right."
			},
			"size": {
				"type": "integer",
				"description": "Size percentage (1-99) for the new pane after 'split'. E.g. size=30 means the new pane occupies 30%% of the space. 0 or omitted means 50/50 split (no resize). Only applies to 'split' action. Note: size resize is only supported on macOS.",
				"minimum": 0,
				"maximum": 99
			},
			"command": {
				"type": "string",
				"description": "Command to run in the new split/tab/window. When set, the surface launches with this command instead of a shell."
			},
			"working_dir": {
				"type": "string",
				"description": "Working directory for the new split/tab/window surface. Defaults to the current working directory."
			},
			"terminal_id": {
				"type": "string",
				"description": "Target terminal ID (UUID) for focus, close, input, send_key, action, zoom. Use 'list' to find IDs. Omit to target the current focused terminal. Note: terminal_id targeting is only fully supported on macOS."
			},
			"text": {
				"type": "string",
				"description": "Text to input (for 'input' action) or Ghostty action string (for 'action' action, e.g. 'resize_split:right,20' or 'scroll_page_down')."
			},
			"key": {
				"type": "string",
				"description": "Key name for send_key (e.g. 'enter', 'c', 'space', 'escape', 'tab', 'backspace', 'up', 'down', 'left', 'right')."
			},
			"modifiers": {
				"type": "string",
				"description": "Comma-separated modifier keys for send_key: shift, control, option, command. Example: 'control' for Ctrl+C."
			},
			"tab_index": {
				"type": "integer",
				"description": "1-based tab index for select_tab action."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Splitting Ghostty pane', '列出 Ghostty 面板'). You MUST always provide this field."
			}
		},
		"required": ["action", "description"]
	}`)
}

func (g *GhosttyTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action     string `json:"action"`
		Direction  string `json:"direction"`
		Size       int    `json:"size"`
		Command    string `json:"command"`
		WorkingDir string `json:"working_dir"`
		TerminalID string `json:"terminal_id"`
		Text       string `json:"text"`
		Key        string `json:"key"`
		Modifiers  string `json:"modifiers"`
		TabIndex   int    `json:"tab_index"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		return Result{IsError: true, Content: "action is required"}, nil
	}

	// Status doesn't require Ghostty to be running — it reports detection.
	if action == "status" {
		return g.executeStatus(), nil
	}

	if !ghosttyAvailable() {
		return Result{IsError: true, Content: "Ghostty is not detected (TERM_PROGRAM != ghostty). This tool only works when ggcode runs inside the Ghostty terminal emulator."}, nil
	}

	switch action {
	case "list":
		return g.executeList(), nil
	case "split":
		return g.executeSplit(args.TerminalID, args.Direction, args.Size, args.Command, args.WorkingDir), nil
	case "new_tab":
		return g.executeNewTab(args.Command, args.WorkingDir), nil
	case "new_window":
		return g.executeNewWindow(args.Command, args.WorkingDir), nil
	case "focus":
		return g.executeFocus(args.TerminalID), nil
	case "close":
		return g.executeClose(args.TerminalID), nil
	case "input":
		return g.executeInput(args.TerminalID, args.Text), nil
	case "send_key":
		return g.executeSendKey(args.TerminalID, args.Key, args.Modifiers), nil
	case "action":
		return g.executeAction(args.TerminalID, args.Text), nil
	case "zoom":
		return g.executeAction(args.TerminalID, "toggle_split_zoom"), nil
	case "equalize":
		return g.executeAction("", "equalize_splits"), nil
	case "select_tab":
		return g.executeSelectTab(args.TabIndex), nil
	case "reload_config":
		return g.executeAction("", "reload_config"), nil
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported ghostty action %q", args.Action)}, nil
	}
}

// ── Detection (shared) ─────────────────────────────────────────────────────

func ghosttyAvailable() bool {
	return os.Getenv("TERM_PROGRAM") == "ghostty"
}

// ── Utility (shared) ────────────────────────────────────────────────────────

func (g *GhosttyTool) workingDir() string {
	if g != nil && strings.TrimSpace(g.WorkingDir) != "" {
		return g.WorkingDir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
