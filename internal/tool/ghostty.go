package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// GhosttyTool lets the agent manage Ghostty terminal panes, tabs, and windows
// when ggcode is running inside Ghostty. It uses the Ghostty AppleScript API
// (via osascript) for all operations.
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
				"description": "Target terminal ID (UUID) for focus, close, input, send_key, action, zoom. Use 'list' to find IDs. Omit to target the current focused terminal."
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
		return g.executeSplit(args.TerminalID, args.Direction, args.Command, args.WorkingDir), nil
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

// ── Detection ──────────────────────────────────────────────────────────────

func ghosttyAvailable() bool {
	return os.Getenv("TERM_PROGRAM") == "ghostty"
}

// ghosttyBinaryPath returns the path to the ghostty CLI binary, or empty if not found.
func ghosttyBinaryPath() string {
	if binDir := os.Getenv("GHOSTTY_BIN_DIR"); binDir != "" {
		p := binDir + string(os.PathSeparator) + "ghostty"
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("ghostty"); err == nil {
		return p
	}
	return ""
}

// ── AppleScript helpers ──

// runAppleScript executes an AppleScript string and returns stdout output.
func runAppleScript(script string) (string, error) {
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		if stderr != "" {
			return "", fmt.Errorf("%s", stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// escapeAS escapes a string for safe embedding in AppleScript double-quoted strings.
func escapeAS(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// terminalSpecifier builds an AppleScript specifier for a terminal.
// If terminalID is empty, it returns a specifier for the current focused terminal.
// Otherwise it returns a specifier that searches all windows for the terminal by ID.
func terminalSpecifier(terminalID string) string {
	if terminalID == "" {
		return "focused terminal of selected tab of window 1"
	}
	return fmt.Sprintf(`first terminal whose id is "%s"`, escapeAS(terminalID))
}

// ── Action implementations ──

func (g *GhosttyTool) executeStatus() Result {
	if !ghosttyAvailable() {
		return Result{Content: "ghostty: not detected (TERM_PROGRAM != ghostty)"}
	}

	version := ""
	if v, err := runAppleScript(`tell application "Ghostty" to get version`); err == nil {
		version = v
	}

	binPath := ghosttyBinaryPath()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("ghostty: detected\n"))
	b.WriteString(fmt.Sprintf("version: %s\n", version))
	if binPath != "" {
		b.WriteString(fmt.Sprintf("binary: %s\n", binPath))
	}
	b.WriteString(fmt.Sprintf("platform: %s/%s", runtime.GOOS, runtime.GOARCH))

	// Show current focused terminal info.
	if cwd, err := runAppleScript(`tell application "Ghostty" to get working directory of focused terminal of selected tab of window 1`); err == nil {
		b.WriteString(fmt.Sprintf("\ncurrent terminal CWD: %s", cwd))
	}
	if tid, err := runAppleScript(`tell application "Ghostty" to get id of focused terminal of selected tab of window 1`); err == nil {
		b.WriteString(fmt.Sprintf("\ncurrent terminal ID: %s", tid))
	}

	return Result{Content: b.String()}
}

func (g *GhosttyTool) executeList() Result {
	script := `
tell application "Ghostty"
	set output to ""
	set winIndex to 0
	repeat with w in windows
		set winIndex to winIndex + 1
		set output to output & "Window " & winIndex & ": " & (name of w) & linefeed
		set tabIndex to 0
		repeat with t in tabs of w
			set tabIndex to tabIndex + 1
			set output to output & "  Tab " & tabIndex & ": " & (name of t)
			if selected of t then
				set output to output & " [active]"
			end if
			set output to output & linefeed
			set term to focused terminal of t
			set output to output & "    Terminal: " & (id of term) & linefeed
			set output to output & "    Title: " & (name of term) & linefeed
			set output to output & "    CWD: " & (working directory of term) & linefeed
		end repeat
	end repeat
	if winIndex is 0 then
		set output to "No windows found"
	end if
	return output
end tell`

	out, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty list failed: %v", err)}
	}
	return Result{Content: out}
}

func (g *GhosttyTool) executeSplit(terminalID, direction, command, workingDir string) Result {
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir == "" {
		dir = "right"
	}
	switch dir {
	case "right", "left", "down", "up":
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid direction %q (use right/left/down/up)", direction)}
	}

	wd := strings.TrimSpace(workingDir)
	if wd == "" {
		wd = g.workingDir()
	}

	spec := terminalSpecifier(terminalID)
	var script string
	if strings.TrimSpace(command) != "" {
		// Create split with command by sending it as initial input after creation.
		// Ghostty's AppleScript split returns the new terminal, then we send the command.
		script = fmt.Sprintf(`
tell application "Ghostty"
	set term to %s
	set newTerm to split term direction %s
	input text "cd %s && %s" to newTerm
	return id of newTerm
end tell`, spec, dir, escapeAS(wd), escapeAS(command))
	} else {
		script = fmt.Sprintf(`
tell application "Ghostty"
	set term to %s
	set newTerm to split term direction %s
	return id of newTerm
end tell`, spec, dir)
	}

	out, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty split failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("ghostty split created: direction=%s, terminal_id=%s", dir, out)}
}

func (g *GhosttyTool) executeNewTab(command, workingDir string) Result {
	wd := strings.TrimSpace(workingDir)
	if wd == "" {
		wd = g.workingDir()
	}

	var script string
	if strings.TrimSpace(command) != "" {
		script = fmt.Sprintf(`
tell application "Ghostty"
	set newTab to make new tab
	set term to focused terminal of newTab
	input text "cd %s && %s" to term
	return id of term
end tell`, escapeAS(wd), escapeAS(command))
	} else {
		script = `
tell application "Ghostty"
	set newTab to make new tab
	set term to focused terminal of newTab
	return id of term
end tell`
	}

	out, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty new_tab failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("ghostty tab created: terminal_id=%s", out)}
}

func (g *GhosttyTool) executeNewWindow(command, workingDir string) Result {
	wd := strings.TrimSpace(workingDir)
	if wd == "" {
		wd = g.workingDir()
	}

	var script string
	if strings.TrimSpace(command) != "" {
		script = fmt.Sprintf(`
tell application "Ghostty"
	set w to make new window
	set term to focused terminal of selected tab of w
	input text "cd %s && %s" to term
	return id of term
end tell`, escapeAS(wd), escapeAS(command))
	} else {
		script = `
tell application "Ghostty"
	set w to make new window
	set term to focused terminal of selected tab of w
	return id of term
end tell`
	}

	out, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty new_window failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("ghostty window created: terminal_id=%s", out)}
}

func (g *GhosttyTool) executeFocus(terminalID string) Result {
	spec := terminalSpecifier(terminalID)
	script := fmt.Sprintf(`
tell application "Ghostty"
	focus %s
	return "focused"
end tell`, spec)

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty focus failed: %v", err)}
	}

	label := terminalID
	if label == "" {
		label = "current terminal"
	}
	return Result{Content: fmt.Sprintf("ghostty focused: %s", label)}
}

func (g *GhosttyTool) executeClose(terminalID string) Result {
	spec := terminalSpecifier(terminalID)
	script := fmt.Sprintf(`
tell application "Ghostty"
	close %s
	return "closed"
end tell`, spec)

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty close failed: %v", err)}
	}

	label := terminalID
	if label == "" {
		label = "current terminal"
	}
	return Result{Content: fmt.Sprintf("ghostty closed: %s", label)}
}

func (g *GhosttyTool) executeInput(terminalID, text string) Result {
	if strings.TrimSpace(text) == "" {
		return Result{IsError: true, Content: "text is required for input action"}
	}

	spec := terminalSpecifier(terminalID)
	script := fmt.Sprintf(`
tell application "Ghostty"
	input text "%s" to %s
	return "sent"
end tell`, escapeAS(text), spec)

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty input failed: %v", err)}
	}

	label := terminalID
	if label == "" {
		label = "current terminal"
	}
	// Show a preview of sent text (truncated for display).
	preview := text
	if len(preview) > 100 {
		preview = preview[:100] + "..."
	}
	return Result{Content: fmt.Sprintf("ghostty input sent to %s: %s", label, preview)}
}

func (g *GhosttyTool) executeSendKey(terminalID, key, modifiers string) Result {
	if strings.TrimSpace(key) == "" {
		return Result{IsError: true, Content: "key is required for send_key action"}
	}

	spec := terminalSpecifier(terminalID)
	var script string
	if strings.TrimSpace(modifiers) != "" {
		script = fmt.Sprintf(`
tell application "Ghostty"
	send key "%s" with modifiers "%s" to %s
	return "sent"
end tell`, escapeAS(key), escapeAS(modifiers), spec)
	} else {
		script = fmt.Sprintf(`
tell application "Ghostty"
	send key "%s" to %s
	return "sent"
end tell`, escapeAS(key), spec)
	}

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty send_key failed: %v", err)}
	}

	label := terminalID
	if label == "" {
		label = "current terminal"
	}
	keyDesc := key
	if modifiers != "" {
		keyDesc = modifiers + "+" + key
	}
	return Result{Content: fmt.Sprintf("ghostty key sent to %s: %s", label, keyDesc)}
}

func (g *GhosttyTool) executeAction(terminalID, actionStr string) Result {
	if strings.TrimSpace(actionStr) == "" {
		return Result{IsError: true, Content: "text (action string) is required for action command"}
	}

	// For global actions (equalize_splits, reload_config), we don't need a terminal target.
	// But perform action requires a terminal parameter per the sdef.
	// We use the current focused terminal as the target for global actions.
	spec := terminalSpecifier(terminalID)
	script := fmt.Sprintf(`
tell application "Ghostty"
	perform action "%s" on %s
	return "done"
end tell`, escapeAS(actionStr), spec)

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty action failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("ghostty action performed: %s", actionStr)}
}

func (g *GhosttyTool) executeSelectTab(tabIndex int) Result {
	if tabIndex < 1 {
		return Result{IsError: true, Content: "tab_index must be >= 1 (1-based)"}
	}

	script := fmt.Sprintf(`
tell application "Ghostty"
	set t to tab %d of window 1
	select t
	return "selected"
end tell`, tabIndex)

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty select_tab failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("ghostty selected tab: %d", tabIndex)}
}

// ── Utility ──

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
