//go:build darwin

package tool

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ── AppleScript helpers ──────────────────────────────────────────────────────
//
// iTerm2 has one of the richest AppleScript dictionaries among macOS terminals.
// Key objects:
//   - application → windows, create window/tab
//   - window → tabs, current tab
//   - tab → sessions, select
//   - session → write text, execute command, split vertically/horizontally,
//               select, close, name, tty, columns, rows, is at shell prompt
//
// Detection: TERM_PROGRAM=iTerm.app, LC_TERMINAL=iTerm2

// iterm2SessionSpecifier builds an AppleScript reference to a session.
// If sessionID is empty, it targets the current session.
func iterm2SessionSpecifier(sessionID string) string {
	if sessionID == "" {
		return "current session of current tab of front window"
	}
	return "theSession"
}

// iterm2SessionLookup returns AppleScript traversal code that finds a session by ID.
// Returns empty string for current session (no lookup needed).
// The found session is stored in variable "theSession".
func iterm2SessionLookup(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	return fmt.Sprintf(`
	set theSession to missing value
	repeat with w in windows
		repeat with t in tabs of w
			repeat with s in sessions of t
				if (id of s) is "%s" then
					set theSession to s
					exit repeat
				end if
			end repeat
			if theSession is not missing value then exit repeat
		end repeat
		if theSession is not missing value then exit repeat
	end repeat
	if theSession is missing value then error "Session not found: %s"`, sessionID, sessionID)
}

// iterm2WriteText writes text to a session using iTerm2's native AppleScript.
// Unlike Warp/Ghostty, iTerm2 has a native "write text" command that types
// directly into the target session without needing System Events.
func iterm2WriteText(sessionID, text string) error {
	spec := iterm2SessionSpecifier(sessionID)
	lookup := iterm2SessionLookup(sessionID)
	script := fmt.Sprintf(`
tell application "iTerm"
	activate%s
	tell %s
		write text "%s"
	end tell
end tell`, lookup, spec, escapeAS(text))
	_, err := runAppleScript(script)
	return err
}

// ── Action implementations (macOS) ───────────────────────────────────────────

func (t *Iterm2Tool) executeStatus() Result {
	if !iterm2Available() {
		return Result{Content: "iterm2: not detected (TERM_PROGRAM != iTerm.app)"}
	}

	var b strings.Builder
	b.WriteString("iterm2: detected\n")
	b.WriteString(fmt.Sprintf("platform: %s/%s\n", runtime.GOOS, runtime.GOARCH))

	// iTerm2 version
	if v, err := runAppleScript(`tell application "iTerm" to get version`); err == nil {
		b.WriteString(fmt.Sprintf("version: %s\n", v))
	}

	// App path
	if p, err := runAppleScript(`tell application "iTerm" to get path to`); err == nil && p != "" {
		b.WriteString(fmt.Sprintf("app path: %s\n", p))
	}

	// Current session info
	if name, err := runAppleScript(`tell application "iTerm" to get name of current session of current tab of front window`); err == nil {
		b.WriteString(fmt.Sprintf("current session name: %s\n", name))
	}
	if tty, err := runAppleScript(`tell application "iTerm" to get tty of current session of current tab of front window`); err == nil {
		b.WriteString(fmt.Sprintf("current session tty: %s\n", tty))
	}
	if cols, err := runAppleScript(`tell application "iTerm" to get columns of current session of current tab of front window`); err == nil {
		b.WriteString(fmt.Sprintf("columns: %s\n", cols))
	}
	if rows, err := runAppleScript(`tell application "iTerm" to get rows of current session of current tab of front window`); err == nil {
		b.WriteString(fmt.Sprintf("rows: %s\n", rows))
	}

	// Session ID (iTerm2 assigns a numeric ID to each session)
	if sid, err := runAppleScript(`tell application "iTerm" to get id of current session of current tab of front window`); err == nil {
		b.WriteString(fmt.Sprintf("current session ID: %s", sid))
	}

	return Result{Content: b.String()}
}

func (t *Iterm2Tool) executeList() Result {
	script := `
tell application "iTerm"
	set output to ""
	set winIndex to 0
	repeat with w in windows
		set winIndex to winIndex + 1
		set output to output & "Window " & winIndex & ": " & (name of w) & linefeed
		set tabIndex to 0
		repeat with tb in tabs of w
			set tabIndex to tabIndex + 1
			set output to output & "  Tab " & tabIndex & linefeed
			set sessIndex to 0
			repeat with s in sessions of tb
				set sessIndex to sessIndex + 1
				try
					set sName to name of s
				on error
					set sName to "(unknown)"
				end try
				try
					set sID to id of s
				on error
					set sID to "?"
				end try
				try
					set sTTY to tty of s
				on error
					set sTTY to "?"
				end try
				set output to output & "    Session " & sessIndex & ": " & sName & linefeed
				set output to output & "      ID: " & sID & linefeed
				set output to output & "      TTY: " & sTTY & linefeed
				try
					set sCols to columns of s
					set sRows to rows of s
					set output to output & "      Size: " & sCols & "x" & sRows & linefeed
				end try
			end repeat
		end repeat
	end repeat
	if winIndex is 0 then
		set output to "No windows found"
	end if
	return output
end tell`

	out, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 list failed: %v", err)}
	}
	return Result{Content: out}
}

func (t *Iterm2Tool) executeSplit(sessionID, direction string, size int, command, workingDir string) Result {
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
		wd = t.workingDir()
	}

	// Map direction to iTerm2 split command
	var splitCmd string
	switch dir {
	case "right", "down":
		// iTerm2 default split is vertically (creates a right pane) for
		// "split vertically" and horizontally (creates a bottom pane) for
		// "split horizontally".
		if dir == "right" {
			splitCmd = "vertically"
		} else {
			splitCmd = "horizontally"
		}
	case "left", "up":
		// iTerm2 always creates new panes to the right/bottom.
		if dir == "left" {
			splitCmd = "vertically"
		} else {
			splitCmd = "horizontally"
		}
	}

	// Build the script
	var script string
	targetSpec := iterm2SessionSpecifier(sessionID)
	lookup := iterm2SessionLookup(sessionID)

	if strings.TrimSpace(command) != "" {
		script = fmt.Sprintf(`
tell application "iTerm"
	activate%s
	tell %s
		set newSession to (split %s with default profile)
		tell newSession
			write text "cd %s && %s"
		end tell
		return id of newSession
	end tell
end tell`, lookup, targetSpec, splitCmd, escapeAS(wd), escapeAS(command))
	} else {
		script = fmt.Sprintf(`
tell application "iTerm"
	activate%s
	tell %s
		set newSession to (split %s with default profile)
		tell newSession
			write text "cd %s"
		end tell
		return id of newSession
	end tell
end tell`, lookup, targetSpec, splitCmd, escapeAS(wd))
	}

	out, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 split failed: %v", err)}
	}

	// Size adjustment: iTerm2 doesn't have a native AppleScript split size
	// ratio. We can use the 'split pane' approach or resize after split.
	// For now, note the limitation if size was requested.
	if size > 0 && size < 100 {
		// We could use System Events to adjust split, but iTerm2's split
		// resize isn't directly scriptable via AppleScript. The split is
		// created at 50/50 by default.
		return Result{Content: fmt.Sprintf("iterm2 split created: direction=%s (size=%d%% not yet applied, iTerm2 defaults to 50/50), session_id=%s", dir, size, out)}
	}
	return Result{Content: fmt.Sprintf("iterm2 split created: direction=%s, session_id=%s", dir, out)}
}

func (t *Iterm2Tool) executeNewTab(command, workingDir string) Result {
	wd := strings.TrimSpace(workingDir)
	if wd == "" {
		wd = t.workingDir()
	}

	var script string
	if strings.TrimSpace(command) != "" {
		script = fmt.Sprintf(`
tell application "iTerm"
	activate
	tell current window
		set newTab to create tab with default profile
		tell current session of newTab
			write text "cd %s && %s"
		end tell
		return id of current session of newTab
	end tell
end tell`, escapeAS(wd), escapeAS(command))
	} else {
		script = `
tell application "iTerm"
	activate
	tell current window
		set newTab to create tab with default profile
		return id of current session of newTab
	end tell
end tell`
	}

	out, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 new_tab failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("iterm2 tab created: session_id=%s", out)}
}

func (t *Iterm2Tool) executeNewWindow(command, workingDir string) Result {
	wd := strings.TrimSpace(workingDir)
	if wd == "" {
		wd = t.workingDir()
	}

	var script string
	if strings.TrimSpace(command) != "" {
		script = fmt.Sprintf(`
tell application "iTerm"
	activate
	set newWindow to create window with default profile
	tell current session of newWindow
		write text "cd %s && %s"
	end tell
	return id of current session of newWindow
end tell`, escapeAS(wd), escapeAS(command))
	} else {
		script = `
tell application "iTerm"
	activate
	set newWindow to create window with default profile
	return id of current session of newWindow
end tell`
	}

	out, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 new_window failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("iterm2 window created: session_id=%s", out)}
}

func (t *Iterm2Tool) executeFocus(sessionID string) Result {
	spec := iterm2SessionSpecifier(sessionID)
	lookup := iterm2SessionLookup(sessionID)
	script := fmt.Sprintf(`
tell application "iTerm"
	activate%s
	select %s
	return "focused"
end tell`, lookup, spec)

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 focus failed: %v", err)}
	}

	label := sessionID
	if label == "" {
		label = "current session"
	}
	return Result{Content: fmt.Sprintf("iterm2 focused: %s", label)}
}

func (t *Iterm2Tool) executeClose(sessionID string) Result {
	spec := iterm2SessionSpecifier(sessionID)
	lookup := iterm2SessionLookup(sessionID)
	script := fmt.Sprintf(`
tell application "iTerm"
%s
	close %s
	return "closed"
end tell`, lookup, spec)

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 close failed: %v", err)}
	}

	label := sessionID
	if label == "" {
		label = "current session"
	}
	return Result{Content: fmt.Sprintf("iterm2 closed: %s", label)}
}

func (t *Iterm2Tool) executeSelectTab(tabIndex int) Result {
	if tabIndex < 1 {
		return Result{IsError: true, Content: "tab_index must be >= 1 (1-based)"}
	}

	script := fmt.Sprintf(`
tell application "iTerm"
	activate
	tell front window
		set tb to item %d of tabs
		select tb
		return "selected"
	end tell
end tell`, tabIndex)

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 select_tab failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("iterm2 selected tab: %d", tabIndex)}
}

func (t *Iterm2Tool) executeInput(sessionID, text string) Result {
	if strings.TrimSpace(text) == "" {
		return Result{IsError: true, Content: "text is required for input action"}
	}

	err := iterm2WriteText(sessionID, text)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 input failed: %v", err)}
	}

	label := sessionID
	if label == "" {
		label = "current session"
	}
	preview := text
	if len(preview) > 100 {
		preview = preview[:100] + "..."
	}
	return Result{Content: fmt.Sprintf("iterm2 input sent to %s: %s", label, preview)}
}

func (t *Iterm2Tool) executeSendKey(sessionID, key, modifiers string) Result {
	if strings.TrimSpace(key) == "" {
		return Result{IsError: true, Content: "key is required for send_key action"}
	}

	// Map key names to terminal escape sequences.
	// This avoids System Events / Accessibility permission requirements.
	keyLower := strings.ToLower(key)
	escMap := map[string]string{
		"enter": "\r", "return": "\r",
		"escape": "\x1b", "esc": "\x1b",
		"tab":       "\t",
		"space":     " ",
		"backspace": "\x7f", "delete": "\x7f",
		"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
		"home": "\x1b[H", "end": "\x1b[F",
		"pageup": "\x1b[5~", "pagedown": "\x1b[6~",
		"f1": "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS",
		"f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~",
		"f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
	}

	var data string
	if seq, ok := escMap[keyLower]; ok {
		data = seq
	} else if len(key) == 1 {
		// Single character — apply modifier transformations
		data = key
		mods := strings.ToLower(modifiers)
		if strings.Contains(mods, "control") || strings.Contains(mods, "ctrl") {
			// Ctrl+letter → control code
			ch := strings.ToUpper(key)
			if ch[0] >= 'A' && ch[0] <= 'Z' {
				data = string(rune(ch[0] - 'A' + 1))
			}
		}
		if strings.Contains(mods, "option") || strings.Contains(mods, "alt") {
			data = "\x1b" + data
		}
	} else {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 send_key: unknown key %q (supported: enter, escape, tab, space, backspace, arrow keys, home, end, pageup, pagedown, f1-f12, single characters)", key)}
	}

	if err := t.iterm2WriteToTTY(sessionID, data); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 send_key failed: %v", err)}
	}

	keyDesc := key
	if modifiers != "" {
		keyDesc = modifiers + "+" + key
	}
	label := sessionID
	if label == "" {
		label = "current session"
	}
	return Result{Content: fmt.Sprintf("iterm2 key sent to %s: %s", label, keyDesc)}
}

func (t *Iterm2Tool) executeResize(sessionID, axis string, increment int) Result {
	if increment == 0 {
		increment = 20
	}

	// iTerm2 doesn't have a direct AppleScript resize for individual panes,
	// but we can use the "adjust split" via System Events menu.
	// The menu items are: "Edit" > "Split Panes" submenu.
	// Alternatively, we can use keyboard shortcuts: Cmd+[ / Cmd+] for resize.
	//
	// For now, use keyboard shortcuts which are the most reliable approach:
	// Cmd+Alt+[ = shrink vertically, Cmd+Alt+] = grow vertically
	// Cmd+Shift+Alt+[ = shrink horizontally, Cmd+Shift+Alt+] = grow horizontally
	//
	// However, these are user-configurable. A more reliable approach is to
	// resize the window itself via AppleScript.

	// Focus target session
	if sessionID != "" {
		t.executeFocus(sessionID)
	} else {
		exec.Command("osascript", "-e", `tell application "iTerm" to activate`).Run()
	}
	time.Sleep(30 * time.Millisecond)

	ax := strings.ToLower(strings.TrimSpace(axis))
	if ax == "" {
		ax = "horizontal"
	}

	// Use iTerm2's native AppleScript to adjust columns/rows directly.
	// This avoids the need for Accessibility/System Events permissions.
	spec := iterm2SessionSpecifier(sessionID)
	lookup := iterm2SessionLookup(sessionID)
	var script string
	if ax == "horizontal" {
		script = fmt.Sprintf(`
tell application "iTerm"
%s
	tell %s
		set currentCols to columns
		set columns to (currentCols + %d)
	end tell
end tell`, lookup, spec, increment)
	} else {
		script = fmt.Sprintf(`
tell application "iTerm"
%s
	tell %s
		set currentRows to rows
		set rows to (currentRows + %d)
	end tell
end tell`, lookup, spec, increment)
	}

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 resize failed: %v", err)}
	}

	dir := "grew"
	if increment < 0 {
		dir = "shrank"
	}
	return Result{Content: fmt.Sprintf("iterm2 %s %s by %d", dir, ax, absInt(increment))}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (t *Iterm2Tool) executeGetText(sessionID string) Result {
	spec := iterm2SessionSpecifier(sessionID)
	lookup := iterm2SessionLookup(sessionID)
	script := fmt.Sprintf(`
tell application "iTerm"
%s
	tell %s
		-- Get the terminal contents
		set output to ""
		repeat with i from 1 to rows
			try
				set lineContent to (contents of line i)
				set output to output & lineContent & linefeed
			end try
		end repeat
		return output
	end tell
end tell`, lookup, spec)

	out, err := runAppleScript(script)
	if err != nil {
		// Fallback: try using capture() which may not be available in all versions
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 get_text failed: %v", err)}
	}

	return Result{Content: out}
}

func (t *Iterm2Tool) executeSetTitle(sessionID, title string) Result {
	if strings.TrimSpace(title) == "" {
		return Result{IsError: true, Content: "text (title) is required for set_title action"}
	}

	spec := iterm2SessionSpecifier(sessionID)
	lookup := iterm2SessionLookup(sessionID)
	script := fmt.Sprintf(`
tell application "iTerm"
%s
	tell %s
		set name to "%s"
	end tell
end tell`, lookup, spec, escapeAS(title))

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 set_title failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("iterm2 session title set: %s", title)}
}

func (t *Iterm2Tool) executeProfile(sessionID, profileName string) Result {
	if strings.TrimSpace(profileName) == "" {
		return Result{IsError: true, Content: "text (profile name) is required for profile action"}
	}

	// iTerm2's "profile name" AppleScript property is read-only.
	// Use the proprietary escape sequence ESC]1337;SetProfile=Name BEL
	// to switch the profile for the target session.
	esc := fmt.Sprintf("\x1b]1337;SetProfile=%s\x07", profileName)
	if err := t.iterm2WriteToTTY(sessionID, esc); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 profile switch failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("iterm2 profile switched: %s", profileName)}
}

// iterm2WriteToTTY writes a raw string directly to the session's TTY device,
// bypassing both System Events and the shell. This is used for escape
// sequences (badge, mark, clear) that must not be interpreted by the shell.
func (t *Iterm2Tool) iterm2WriteToTTY(sessionID, data string) error {
	spec := iterm2SessionSpecifier(sessionID)
	lookup := iterm2SessionLookup(sessionID)
	ttyScript := fmt.Sprintf(`
tell application "iTerm"
%s
	tell %s
		return tty
	end tell
end tell`, lookup, spec)

	tty, err := runAppleScript(ttyScript)
	if err != nil {
		return fmt.Errorf("failed to get session tty: %w", err)
	}
	tty = strings.TrimSpace(tty)
	if tty == "" {
		return fmt.Errorf("could not determine session TTY")
	}

	cmd := exec.Command("sh", "-c", fmt.Sprintf("printf '%s' > %s", data, tty))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to write to tty: %w", err)
	}
	return nil
}

func (t *Iterm2Tool) executeBadge(sessionID, badgeText string) Result {
	// iTerm2 supports per-session badges via the OSC 1337 escape sequence:
	// ESC ] 1337 ; SetBadgeFormat = BASE64(text) BEL
	// We write the escape sequence directly to the session's TTY device
	// to avoid interference from the shell or TUI raw mode.

	if badgeText == "" {
		err := t.iterm2WriteToTTY(sessionID, "\033]1337;SetBadgeFormat=\007")
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("iterm2 badge clear failed: %v", err)}
		}
		return Result{Content: "iterm2 badge cleared"}
	}

	err := t.iterm2WriteToTTY(sessionID, fmt.Sprintf("\033]1337;SetBadgeFormat=%s\007", badgeText))
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 badge failed: %v", err)}
	}

	preview := badgeText
	if len(preview) > 60 {
		preview = preview[:57] + "..."
	}
	return Result{Content: fmt.Sprintf("iterm2 badge set: %s", preview)}
}

func (t *Iterm2Tool) executeBroadcast(subAction string) Result {
	sub := strings.ToLower(strings.TrimSpace(subAction))
	if sub == "" {
		sub = "toggle"
	}

	var script string
	switch sub {
	case "toggle":
		// Toggle "Toggle Input Broadcasting" menu item
		script = `
tell application "iTerm" to activate
tell application "System Events"
	click menu item "Toggle Broadcasting to Current Split Pane" of menu "Shell" of menu bar item "Shell" of menu bar 1 of process "iTerm2"
end tell`
	case "on":
		script = `
tell application "iTerm" to activate
tell application "System Events"
	click menu item "Broadcast Input" of menu "Shell" of menu bar item "Shell" of menu bar 1 of process "iTerm2"
end tell`
	case "off":
		script = `
tell application "iTerm" to activate
tell application "System Events"
	click menu item "Toggle Broadcasting to Current Split Pane" of menu "Shell" of menu bar item "Shell" of menu bar 1 of process "iTerm2"
end tell`
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid broadcast sub-action %q (use toggle/on/off)", subAction)}
	}

	_, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 broadcast failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("iterm2 broadcast: %s", sub)}
}

func (t *Iterm2Tool) executeMark(subAction string) Result {
	sub := strings.ToLower(strings.TrimSpace(subAction))
	if sub == "" {
		sub = "set"
	}

	switch sub {
	case "set":
		// Use OSC 1337 escape sequence written directly to TTY — no
		// Accessibility permission needed.
		err := t.iterm2WriteToTTY("", "\033]1337;SetMark\007")
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("iterm2 mark set failed: %v", err)}
		}
		return Result{Content: "iterm2 mark: set"}

	case "next", "prev", "clear":
		// No known escape sequence for jump/clear marks — needs keyboard
		// shortcuts via System Events, which requires Accessibility.
		script := fmt.Sprintf(`
tell application "iTerm" to activate
tell application "System Events"
	tell process "iTerm2"
		if %q is "next" then
			keystroke "j" using {command down, shift down}
		else if %q is "prev" then
			keystroke "j" using {command down, shift down, option down}
		else if %q is "clear" then
			keystroke "m" using {command down, shift down, option down}
		end if
	end tell
end tell`, sub, sub, sub)

		_, err := runAppleScript(script)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("iterm2 mark %s failed (needs Accessibility permission for keyboard shortcuts): %v", sub, err)}
		}
		return Result{Content: fmt.Sprintf("iterm2 mark: %s", sub)}

	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid mark sub-action %q (use set/next/prev/clear)", subAction)}
	}
}

func (t *Iterm2Tool) executeClear(sessionID string) Result {
	// Clear screen + scrollback via escape sequence written to TTY.
	// ESC[2J = clear screen, ESC[3J = clear scrollback, ESC[H = cursor home.
	// No Accessibility permission needed.
	err := t.iterm2WriteToTTY(sessionID, "\033[2J\033[3J\033[H")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 clear failed: %v", err)}
	}
	return Result{Content: "iterm2 buffer cleared"}
}

func (t *Iterm2Tool) executeMenuAction(menuItem string) Result {
	if strings.TrimSpace(menuItem) == "" {
		return Result{IsError: true, Content: "text (menu item name) is required for action"}
	}

	// Search all menus for the item (same pattern as Warp)
	script := fmt.Sprintf(`
tell application "iTerm" to activate
tell application "System Events"
	tell process "iTerm2"
		set targetItem to %q
		set menuNames to {"File", "Edit", "View", "Sessions", "Tab", "Split Panes", "Window", "Help", "Shell"}
		repeat with mb in menuNames
			try
				tell menu bar 1
					tell menu 1 of menu bar item mb
						set itemCount to count of menu items
						repeat with i from 1 to itemCount
							if name of menu item i is targetItem then
								click menu item i
								return "ok"
							end if
						end repeat
					end tell
				end tell
			end try
		end repeat
	end tell
end tell
return "not found"`, menuItem)

	out, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 action %q failed: %v", menuItem, err)}
	}

	result := strings.TrimSpace(out)
	if result == "not found" {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2: menu item %q not found in any menu", menuItem)}
	}
	return Result{Content: fmt.Sprintf("iterm2: %s", menuItem)}
}

func (t *Iterm2Tool) executeReloadConfig() Result {
	// iTerm2 does not have a native AppleScript command to reload preferences.
	// System Events UI scripting requires Accessibility permissions.
	// Best effort: try the menu item; if it fails, inform the user.
	script := `
tell application "iTerm" to activate
tell application "System Events"
	tell process "iTerm2"
		try
			click menu item "Reload Preferences" of menu "iTerm2" of menu bar item "iTerm2" of menu bar 1
			return "reloaded"
		on error
			try
				click menu item "Preferences…" of menu "iTerm2" of menu bar item "iTerm2" of menu bar 1
				return "opened preferences"
			on error
				return "no reload menu item found"
			end try
		end try
	end tell
end tell`

	out, err := runAppleScript(script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("iterm2 reload_config failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("iterm2 config: %s", strings.TrimSpace(out))}
}
