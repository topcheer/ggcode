package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Iterm2Tool lets the agent manage iTerm2 terminal sessions, tabs, panes, and
// windows when ggcode is running inside iTerm2.
//
// Platform support:
//   - macOS: Uses iTerm2's rich AppleScript dictionary (via osascript) and
//     System Events for keyboard events.
//
// iTerm2 is arguably the most automation-friendly terminal on macOS, with a
// mature AppleScript scripting surface that supports:
//   - Structured window/tab/session enumeration
//   - Native `write text` to sessions (no System Events needed)
//   - Split pane creation with `split vertically/horizontally`
//   - Per-session targeting by session ID
//   - Screen content capture
//   - Profile switching, badges, marks, input broadcasting (unique features)
//
// See iterm2_darwin.go for the implementation.
type Iterm2Tool struct {
	WorkingDir string
}

func NewIterm2Tool(workingDir string) *Iterm2Tool {
	return &Iterm2Tool{WorkingDir: workingDir}
}

func (t *Iterm2Tool) Clone() Tool {
	if t == nil {
		return NewIterm2Tool("")
	}
	return &Iterm2Tool{WorkingDir: t.WorkingDir}
}

func (t *Iterm2Tool) Name() string { return "iterm2" }

func (t *Iterm2Tool) Description() string {
	return "Manage iTerm2 terminal sessions, tabs, panes, and windows when running inside iTerm2. Supports splits, text input, key events, screen capture, profiles, and more via AppleScript. Use when the user asks to manage terminal panes in iTerm2."
}

func (t *Iterm2Tool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["status", "list", "split", "new_tab", "new_window", "focus", "close", "select_tab", "input", "send_key", "resize", "get_text", "set_title", "profile", "badge", "broadcast", "mark", "clear", "action", "reload_config"],
				"description": "iTerm2 action to perform. status shows detection info; list shows all windows/tabs/sessions; split creates a new split pane; new_tab/new_window create new surfaces; focus navigates to a session by ID; close removes a session by ID; select_tab switches tab by index; input sends text to a session; send_key sends a keyboard event; resize changes split dimensions; get_text retrieves screen contents; set_title renames the current session; profile switches the session profile; badge sets a session badge (unique to iTerm2); broadcast toggles input broadcasting to all panes (unique to iTerm2); mark sets or jumps to marks (unique to iTerm2); clear clears the session buffer; action clicks a menu item; reload_config reloads preferences."
			},
			"direction": {
				"type": "string",
				"enum": ["right", "left", "down", "up"],
				"description": "Split direction for 'split' action. right/left create a vertical split; down/up create a horizontal split. Default: right."
			},
			"size": {
				"type": "integer",
				"description": "Size percentage (1-99) for the new pane after 'split'. E.g. size=30 means the new pane occupies 30%% of the space. 0 or omitted means 50/50 split.",
				"minimum": 0,
				"maximum": 99
			},
			"command": {
				"type": "string",
				"description": "Command to run in the new split/tab/window. When set, the text is typed into the new surface after creation."
			},
			"working_dir": {
				"type": "string",
				"description": "Working directory for the new split/tab/window. Defaults to the current working directory."
			},
			"session_id": {
				"type": "string",
				"description": "Target session ID for focus, close, input, send_key, get_text, set_title, profile. Use 'list' to find IDs. Omit to target the current session."
			},
			"text": {
				"type": "string",
				"description": "Text to send (for 'input' action), menu item name (for 'action' action), session title (for 'set_title' action), profile name (for 'profile' action), badge text (for 'badge' action), or mark sub-action (for 'mark' action: 'set', 'next', 'prev', 'clear')."
			},
			"key": {
				"type": "string",
				"description": "Key name for send_key (e.g. 'enter', 'c', 'space', 'escape', 'tab', 'backspace', 'up', 'down', 'left', 'right', 'home', 'end', 'pageup', 'pagedown')."
			},
			"modifiers": {
				"type": "string",
				"description": "Comma-separated modifier keys for send_key: shift, control, option, command. Example: 'control' for Ctrl+C."
			},
			"axis": {
				"type": "string",
				"enum": ["horizontal", "vertical"],
				"description": "Resize axis for 'resize' action. Default: horizontal."
			},
			"increment": {
				"type": "integer",
				"description": "Pixel/column increment for 'resize' action. Positive to grow, negative to shrink. Default: 20.",
				"minimum": -500,
				"maximum": 500
			},
			"tab_index": {
				"type": "integer",
				"description": "1-based tab index for select_tab action."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Splitting iTerm2 pane', '列出 iTerm2 面板'). You MUST always provide this field."
			}
		},
		"required": ["action", "description"]
	}`)
}

func (t *Iterm2Tool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action     string `json:"action"`
		Direction  string `json:"direction"`
		Size       int    `json:"size"`
		Command    string `json:"command"`
		WorkingDir string `json:"working_dir"`
		SessionID  string `json:"session_id"`
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

	// Status doesn't require iTerm2 to be running — it reports detection.
	if action == "status" {
		return t.executeStatus(), nil
	}

	if !iterm2Available() {
		return Result{IsError: true, Content: "iTerm2 is not detected (TERM_PROGRAM != iTerm.app). This tool only works when ggcode runs inside the iTerm2 terminal emulator."}, nil
	}

	switch action {
	case "list":
		return t.executeList(), nil
	case "split":
		return t.executeSplit(args.SessionID, args.Direction, args.Size, args.Command, args.WorkingDir), nil
	case "new_tab":
		return t.executeNewTab(args.Command, args.WorkingDir), nil
	case "new_window":
		return t.executeNewWindow(args.Command, args.WorkingDir), nil
	case "focus":
		return t.executeFocus(args.SessionID), nil
	case "close":
		return t.executeClose(args.SessionID), nil
	case "select_tab":
		return t.executeSelectTab(args.TabIndex), nil
	case "input":
		return t.executeInput(args.SessionID, args.Text), nil
	case "send_key":
		return t.executeSendKey(args.SessionID, args.Key, args.Modifiers), nil
	case "resize":
		return t.executeResize(args.SessionID, args.Axis, args.Increment), nil
	case "get_text":
		return t.executeGetText(args.SessionID), nil
	case "set_title":
		return t.executeSetTitle(args.SessionID, args.Text), nil
	case "profile":
		return t.executeProfile(args.SessionID, args.Text), nil
	case "badge":
		return t.executeBadge(args.SessionID, args.Text), nil
	case "broadcast":
		return t.executeBroadcast(args.Text), nil
	case "mark":
		return t.executeMark(args.Text), nil
	case "clear":
		return t.executeClear(args.SessionID), nil
	case "action":
		return t.executeMenuAction(args.Text), nil
	case "reload_config":
		return t.executeReloadConfig(), nil
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported iterm2 action %q", args.Action)}, nil
	}
}

// -- Detection (shared) --------------------------------------------------------

func iterm2Available() bool {
	return os.Getenv("TERM_PROGRAM") == "iTerm.app"
}

// -- Utility (shared) ----------------------------------------------------------

func (t *Iterm2Tool) workingDir() string {
	if t != nil && strings.TrimSpace(t.WorkingDir) != "" {
		return t.WorkingDir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
