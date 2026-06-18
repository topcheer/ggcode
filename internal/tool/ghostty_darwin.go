//go:build darwin

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

// ── AppleScript helpers (macOS only) ────────────────────────────────────────

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

func oppositeDir(dir string) string {
	switch dir {
	case "right":
		return "left"
	case "left":
		return "right"
	case "down":
		return "up"
	case "up":
		return "down"
	}
	return dir
}

// termCellDims returns the terminal's (cols, rows) via TIOCGWINSZ on /dev/tty.
// Falls back to (80, 24) if unavailable (e.g. piped stdin/stdout).
func termCellDims() (cols, rows int) {
	cols, rows = 80, 24
	f, err := os.Open("/dev/tty")
	if err != nil {
		return
	}
	defer f.Close()

	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return
	}
	cols = int(ws.Col)
	rows = int(ws.Row)
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	return
}

// termWidthPx estimates the terminal window pixel width from grid dimensions.
// Ghostty default font (~13pt) has cell width ~8px.
func termWidthPx() int {
	cols, _ := termCellDims()
	return cols * 8
}

// termHeightPx estimates the terminal window pixel height from grid dimensions.
// Ghostty default font (~13pt) has line height ~17px.
func termHeightPx() int {
	_, rows := termCellDims()
	return rows * 17
}

// terminalSpecifier builds an AppleScript specifier for a terminal.
func terminalSpecifier(terminalID string) string {
	if terminalID == "" {
		return "focused terminal of selected tab of window 1"
	}
	return fmt.Sprintf(`first terminal whose id is "%s"`, escapeAS(terminalID))
}

// ghosttyBinaryPath returns the path to the ghostty CLI binary, or empty if not found.
func ghosttyBinaryPath() string {
	if p, err := exec.LookPath("ghostty"); err == nil {
		return p
	}
	// Check common macOS install location
	p := "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	if _, err := exec.LookPath(p); err == nil {
		return p
	}
	return ""
}

// ── Action implementations (macOS) ──────────────────────────────────────────

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
	b.WriteString("ghostty: detected\n")
	b.WriteString(fmt.Sprintf("version: %s\n", version))
	if binPath != "" {
		b.WriteString(fmt.Sprintf("binary: %s\n", binPath))
	}
	b.WriteString(fmt.Sprintf("platform: %s/%s", runtime.GOOS, runtime.GOARCH))

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

func (g *GhosttyTool) executeSplit(terminalID, direction string, size int, command, workingDir string) Result {
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

	var resizePart string
	if size > 0 && size < 99 {
		// Ghostty resize_split amount is in PIXELS, not percentage.
		// After a 50/50 split, move the divider so the new pane is size%.
		delta := 50 - size
		var resizeDir string
		if delta >= 0 {
			resizeDir = dir // shrink new pane
		} else {
			resizeDir = oppositeDir(dir) // grow new pane
			delta = -delta
		}

		// Ghostty sdef has no window bounds/size property. We estimate
		// pixel dimensions from the terminal grid size (cols/rows) obtained
		// via TIOCGWINSZ on /dev/tty. Ghostty default font ~13pt gives
		// cell width ~8px and line height ~17px, which is accurate enough
		// for proportional split resizing.
		var pixAmt int
		if dir == "right" || dir == "left" {
			pixAmt = delta * termWidthPx() / 100
		} else {
			pixAmt = delta * termHeightPx() / 100
		}
		if pixAmt < 1 {
			pixAmt = 1
		}

		resizePart = fmt.Sprintf(`
	perform action "resize_split:%s,%d" on term`, resizeDir, pixAmt)
	}

	var cmdPart string
	if strings.TrimSpace(command) != "" {
		cmdPart = fmt.Sprintf(`
	input text "cd %s && %s" to newTerm`, escapeAS(wd), escapeAS(command))
	}

	script := fmt.Sprintf(`
tell application "Ghostty"
	set term to %s
	set newTerm to split term direction %s%s%s
	return id of newTerm
end tell`, spec, dir, resizePart, cmdPart)

	out, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty split failed: %v", err)}
	}

	if size > 0 && size < 99 {
		return Result{Content: fmt.Sprintf("ghostty split created: direction=%s, size=%d%%, terminal_id=%s", dir, size, out)}
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
	set newTab to new tab in window 1
	set term to focused terminal of newTab
	input text "cd %s && %s" to term
	return id of term
end tell`, escapeAS(wd), escapeAS(command))
	} else {
		script = `
tell application "Ghostty"
	set newTab to new tab in window 1
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
	set newWindow to new window
	set term to focused terminal of selected tab of newWindow
	input text "cd %s && %s" to term
	return id of term
end tell`, escapeAS(wd), escapeAS(command))
	} else {
		script = `
tell application "Ghostty"
	set newWindow to new window
	set term to focused terminal of selected tab of newWindow
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

	// Ghostty's sdef defines "select tab" as a command whose direct parameter
	// is a tab specifier. The correct AppleScript syntax is:
	//   tell window 1 to select tab (first tab) / (second tab) / etc.
	// We convert the 1-based index to an ordinal.
	ordinals := []string{"", "first", "second", "third", "fourth", "fifth",
		"sixth", "seventh", "eighth", "ninth", "tenth"}
	var tabRef string
	if tabIndex < len(ordinals) {
		tabRef = ordinals[tabIndex] + " tab"
	} else {
		// For indices beyond our ordinal list, use "item N of tabs"
		tabRef = fmt.Sprintf("item %d of tabs", tabIndex)
	}

	script := fmt.Sprintf(`
tell application "Ghostty"
	tell window 1 to select tab (%s)
	return "selected"
end tell`, tabRef)

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty select_tab failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("ghostty selected tab: %d", tabIndex)}
}
