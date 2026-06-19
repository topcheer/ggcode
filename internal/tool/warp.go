package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// WarpTool lets the agent manage Warp terminal panes, tabs, and windows
// when ggcode is running inside Warp.
//
// Platform support:
//   - macOS: Uses System Events menu item clicks (via osascript)
//   - Linux: Not yet supported (Warp on Linux has limited automation surface)
//
// See warp_darwin.go for the implementation.
type WarpTool struct {
	WorkingDir string
}

func NewWarpTool(workingDir string) *WarpTool {
	return &WarpTool{WorkingDir: workingDir}
}

func (w *WarpTool) Clone() Tool {
	if w == nil {
		return NewWarpTool("")
	}
	return &WarpTool{WorkingDir: w.WorkingDir}
}

func (w *WarpTool) Name() string { return "warp" }

func (w *WarpTool) Description() string {
	return "Manage Warp terminal panes, tabs, and windows when running inside Warp: inspect status, create splits, new tabs, new agent tabs, navigate panes/tabs, send text input, send key events, toggle pane zoom, and interact with Warp blocks (select, copy, share). Use this when the user asks to manage terminal panes in Warp."
}

func (w *WarpTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["status", "new_tab", "new_agent_tab", "new_window", "split_right", "split_left", "split_down", "split_up", "close_tab", "close_pane", "next_tab", "prev_tab", "next_pane", "prev_pane", "toggle_zoom", "focus_input", "input", "send_key", "select_prev_block", "select_next_block", "copy_block", "copy_block_output", "share_block", "clear_blocks", "command_palette", "close_other_tabs", "move_tab_up", "move_tab_down"],
				"description": "Warp action to perform. status shows detection info; new_tab/new_agent_tab/new_window create new surfaces; split_right/left/down/up create split panes; close_tab/close_pane close surfaces; next_tab/prev_tab switch tabs; next_pane/prev_pane switch panes; toggle_zoom maximizes/restores pane; focus_input refocuses the command input; input sends text to the terminal; send_key sends a keyboard event; select_prev/next_block navigate Warp blocks; copy_block copies the selected block's command+output; copy_block_output copies only the output; share_block shares the selected block; clear_blocks clears all blocks; command_palette opens the Warp command palette; close_other_tabs closes all tabs except the active one; move_tab_up/down reorders tabs."
			},
			"text": {
				"type": "string",
				"description": "Text to send for 'input' action."
			},
			"key": {
				"type": "string",
				"description": "Key name for send_key (e.g. 'enter', 'c', 'space', 'escape', 'tab', 'backspace', 'up', 'down', 'left', 'right')."
			},
			"modifiers": {
				"type": "string",
				"description": "Comma-separated modifier keys for send_key: shift, control, option, command. Example: 'control' for Ctrl+C."
			},
			"command": {
				"type": "string",
				"description": "Command to execute in the new tab/window/split. When set, the text is typed into the new surface after creation."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Splitting Warp pane', '列出 Warp 面板'). You MUST always provide this field."
			}
		},
		"required": ["action", "description"]
	}`)
}

func (w *WarpTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action    string `json:"action"`
		Text      string `json:"text"`
		Key       string `json:"key"`
		Modifiers string `json:"modifiers"`
		Command   string `json:"command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		return Result{IsError: true, Content: "action is required"}, nil
	}

	// Status doesn't require Warp to be running — it reports detection.
	if action == "status" {
		return w.executeStatus(), nil
	}

	if !warpAvailable() {
		return Result{IsError: true, Content: "Warp is not detected (TERM_PROGRAM != WarpTerminal). This tool only works when ggcode runs inside the Warp terminal emulator."}, nil
	}

	// Menu-based actions
	menuActions := map[string]string{
		"new_tab":           "New Terminal Tab",
		"new_agent_tab":     "New Agent Tab",
		"new_window":        "New Window",
		"split_right":       "Split Pane Right",
		"split_left":        "Split Pane Left",
		"split_down":        "Split Pane Down",
		"split_up":          "Split Pane Up",
		"close_tab":         "Close the Current Tab",
		"close_pane":        "Close Current Session",
		"next_tab":          "Switch to Next Tab",
		"prev_tab":          "Switch to Previous Tab",
		"next_pane":         "Activate Next Pane",
		"prev_pane":         "Activate Previous Pane",
		"toggle_zoom":       "Toggle Maximize Active Pane",
		"focus_input":       "Focus Terminal Input",
		"select_prev_block": "Select Previous Block",
		"select_next_block": "Select Next Block",
		"copy_block":        "Copy Command and Output",
		"copy_block_output": "Copy Command Output",
		"share_block":       "Share Selected Block",
		"clear_blocks":      "Clear Blocks",
		"command_palette":   "Command Palette",
		"close_other_tabs":  "Close Other Tabs",
		"move_tab_up":       "Move Tab Up",
		"move_tab_down":     "Move Tab Down",
	}

	menuItem, isMenu := menuActions[action]
	if isMenu {
		result := w.clickMenu(menuItem)
		// If a command was provided and the action creates a new surface, type the command
		if result.IsError == false && args.Command != "" {
			surfaceActions := map[string]bool{
				"new_tab":       true,
				"new_agent_tab": true,
				"new_window":    true,
				"split_right":   true,
				"split_left":    true,
				"split_down":    true,
				"split_up":      true,
			}
			if surfaceActions[action] {
				// Brief delay to let the new surface get focus
				w.executeInput(args.Command)
				w.executeSendKey("enter", "")
			}
		}
		return result, nil
	}

	switch action {
	case "input":
		return w.executeInput(args.Text), nil
	case "send_key":
		return w.executeSendKey(args.Key, args.Modifiers), nil
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported warp action %q", args.Action)}, nil
	}
}

// ── Detection (shared) ─────────────────────────────────────────────────────

func warpAvailable() bool {
	return os.Getenv("TERM_PROGRAM") == "WarpTerminal"
}

// ── Utility (shared) ────────────────────────────────────────────────────────

func (w *WarpTool) workingDir() string {
	if w != nil && strings.TrimSpace(w.WorkingDir) != "" {
		return w.WorkingDir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
