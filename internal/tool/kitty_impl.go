package tool

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ── Kitty remote control CLI helpers ─────────────────────────────────────────
//
// Kitty's remote control is accessed via the `kitten @` or `kitty @` CLI.
// The `@` argument tells kitty/kitten to operate in remote control mode.
//
// Environment variables:
//   - TERM_PROGRAM=kitty         — identifies Kitty
//   - KITTY_WINDOW_ID=N          — numeric ID of the current window
//   - KITTY_LISTEN_ON=unix:PATH  — socket for remote control (may be empty
//                                  if allow_remote_control is not set)
//
// The `--to` flag passes the socket address when KITTY_LISTEN_ON is set.

// kittyBinaryPath returns the path to the kitten or kitty binary, or empty if not found.
// Prefers `kitten` (standalone remote control client) over `kitty`.
func kittyBinaryPath() string {
	// Try `kitten` in PATH first (standalone, smaller, preferred for RC)
	if p, err := exec.LookPath("kitten"); err == nil {
		return p
	}
	// Try `kitty` in PATH
	if p, err := exec.LookPath("kitty"); err == nil {
		return p
	}
	// macOS app bundle locations
	if runtime.GOOS == "darwin" {
		for _, name := range []string{"kitten", "kitty"} {
			p := fmt.Sprintf("/Applications/kitty.app/Contents/MacOS/%s", name)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	// Linux common locations
	for _, p := range []string{"/usr/bin/kitten", "/usr/local/bin/kitten", "/opt/homebrew/bin/kitten"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, p := range []string{"/usr/bin/kitty", "/usr/local/bin/kitty", "/opt/homebrew/bin/kitty"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// kittyAt runs a `kitten @ <cmd>` / `kitty @ <cmd>` command and returns stdout.
// It automatically adds the `--to` flag if KITTY_LISTEN_ON is set.
func kittyAt(args ...string) (string, error) {
	bin := kittyBinaryPath()
	if bin == "" {
		return "", fmt.Errorf("kitty/kitten binary not found")
	}

	// Build the full command: <binary> @ [--to SOCKET] <args...>
	cmdArgs := []string{"@"}
	if sock := os.Getenv("KITTY_LISTEN_ON"); sock != "" {
		cmdArgs = append(cmdArgs, "--to", sock)
	}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command(bin, cmdArgs...)
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

// matchID builds a --match=id:N argument for targeting a specific window.
// If windowID is 0, returns empty string (targets the current focused window).
func matchID(windowID int) string {
	if windowID > 0 {
		return fmt.Sprintf("id:%d", windowID)
	}
	return ""
}

// ── Action implementations ──────────────────────────────────────────────────

func (k *KittyTool) executeStatus() Result {
	if !kittyAvailable() {
		return Result{Content: "kitty: not detected (TERM_PROGRAM != kitty)"}
	}

	var b strings.Builder
	b.WriteString("kitty: detected\n")
	b.WriteString(fmt.Sprintf("platform: %s/%s\n", runtime.GOOS, runtime.GOARCH))

	binPath := kittyBinaryPath()
	if binPath != "" {
		b.WriteString(fmt.Sprintf("binary: %s\n", binPath))
	} else {
		b.WriteString("binary: not found (install kitty/kitten for pane management)\n")
	}

	if wid := os.Getenv("KITTY_WINDOW_ID"); wid != "" {
		b.WriteString(fmt.Sprintf("window ID: %s\n", wid))
	}

	sock := os.Getenv("KITTY_LISTEN_ON")
	if sock != "" {
		b.WriteString(fmt.Sprintf("listen socket: %s\n", sock))
	} else {
		b.WriteString("listen socket: not set (remote control may require allow_remote_control=yes in kitty.conf)\n")
	}

	// Try to get kitty version
	if binPath != "" {
		if v, err := exec.Command(binPath, "--version").Output(); err == nil {
			b.WriteString(fmt.Sprintf("version: %s", strings.TrimSpace(string(v))))
		}
	}

	return Result{Content: b.String()}
}

func (k *KittyTool) executeList() Result {
	out, err := kittyAt("ls")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty list failed: %v", err)}
	}
	// The output from `kitten @ ls` is JSON. Return it as-is — it's
	// structured and useful for programmatic consumption.
	return Result{Content: out}
}

func (k *KittyTool) executeSplit(windowID int, direction string, size int, command, workingDir string) Result {
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir == "" {
		dir = "right"
	}

	// Map direction to kitty launch --location
	var location string
	switch dir {
	case "right", "left":
		location = "vsplit"
	case "down", "up":
		location = "hsplit"
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid direction %q (use right/left/down/up)", direction)}
	}

	wd := strings.TrimSpace(workingDir)
	if wd == "" {
		wd = k.workingDir()
	}

	args := []string{"launch", "--type=window", "--location=" + location, "--cwd=" + wd}

	// Size bias: kitty uses --bias N where N is 1-100 (the new window's share).
	// We map our size percentage directly.
	if size > 0 && size < 100 {
		args = append(args, fmt.Sprintf("--bias=%d", size))
	}

	// For left/up splits, add --no-tab-mgmt is not needed, but kitty doesn't
	// directly support creating splits on the left/top. We create the split
	// normally — the direction just determines vsplit vs hsplit.
	if dir == "left" || dir == "up" {
		// Kitty always creates vsplit to the right and hsplit below.
		// For left/up, we note the limitation.
	}

	// Command to run in the new window.
	// Wrap in /bin/sh -c so the command string is handled as a shell command,
	// matching the behavior of tmux/xterm/ghostty. Without this, kitty would
	// treat the entire command string as a single program name.
	if strings.TrimSpace(command) != "" {
		args = append(args, "--", "/bin/sh", "-c", command)
	}

	out, err := kittyAt(args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty split failed: %v", err)}
	}

	sizeInfo := ""
	if size > 0 && size < 100 {
		sizeInfo = fmt.Sprintf(", size=%d%%", size)
	}
	if dir == "left" || dir == "up" {
		sizeInfo += fmt.Sprintf(" (note: kitty creates %s splits in the default direction)", location)
	}

	return Result{Content: fmt.Sprintf("kitty split created: direction=%s%s\n%s", dir, sizeInfo, out)}
}

func (k *KittyTool) executeNewTab(command, workingDir string) Result {
	wd := strings.TrimSpace(workingDir)
	if wd == "" {
		wd = k.workingDir()
	}

	args := []string{"launch", "--type=tab", "--cwd=" + wd}
	if strings.TrimSpace(command) != "" {
		args = append(args, "--", "/bin/sh", "-c", command)
	}

	out, err := kittyAt(args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty new_tab failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("kitty tab created\n%s", out)}
}

func (k *KittyTool) executeNewWindow(command, workingDir string) Result {
	wd := strings.TrimSpace(workingDir)
	if wd == "" {
		wd = k.workingDir()
	}

	args := []string{"launch", "--type=os-window", "--cwd=" + wd}
	if strings.TrimSpace(command) != "" {
		args = append(args, "--", "/bin/sh", "-c", command)
	}

	out, err := kittyAt(args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty new_window failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("kitty OS window created\n%s", out)}
}

func (k *KittyTool) executeFocus(windowID int) Result {
	args := []string{"focus-window"}
	m := matchID(windowID)
	if m != "" {
		args = append(args, "--match="+m)
	}

	_, err := kittyAt(args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty focus failed: %v", err)}
	}

	label := fmt.Sprintf("window %d", windowID)
	if windowID == 0 {
		label = "current window"
	}
	return Result{Content: fmt.Sprintf("kitty focused: %s", label)}
}

func (k *KittyTool) executeClose(windowID int) Result {
	args := []string{"close-window"}
	m := matchID(windowID)
	if m != "" {
		args = append(args, "--match="+m)
	}

	_, err := kittyAt(args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty close failed: %v", err)}
	}

	label := fmt.Sprintf("window %d", windowID)
	if windowID == 0 {
		label = "current window"
	}
	return Result{Content: fmt.Sprintf("kitty closed: %s", label)}
}

func (k *KittyTool) executeCloseTab() Result {
	_, err := kittyAt("close-tab")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty close_tab failed: %v", err)}
	}
	return Result{Content: "kitty tab closed"}
}

func (k *KittyTool) executeSelectTab(tabIndex int) Result {
	if tabIndex < 1 {
		return Result{IsError: true, Content: "tab_index must be >= 1 (1-based)"}
	}

	// kitty focus-tab uses --match=index:N (0-based internally, but
	// many kitty versions use a `recent` or `index` matcher).
	// We use index:N where N is 0-based, so we subtract 1 from our 1-based input.
	_, err := kittyAt("focus-tab", fmt.Sprintf("--match=index:%d", tabIndex-1))
	if err != nil {
		// Some kitty versions might not support index matcher; try
		// the `--match=order:N` alternative.
		_, err2 := kittyAt("focus-tab", fmt.Sprintf("--match=order:%d", tabIndex-1))
		if err2 != nil {
			return Result{IsError: true, Content: fmt.Sprintf("kitty select_tab failed: %v", err)}
		}
	}

	return Result{Content: fmt.Sprintf("kitty selected tab: %d", tabIndex)}
}

func (k *KittyTool) executeInput(windowID int, text string) Result {
	if strings.TrimSpace(text) == "" {
		return Result{IsError: true, Content: "text is required for input action"}
	}

	args := []string{"send-text"}
	m := matchID(windowID)
	if m != "" {
		args = append(args, "--match="+m)
	}
	args = append(args, text)

	_, err := kittyAt(args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty input failed: %v", err)}
	}

	label := fmt.Sprintf("window %d", windowID)
	if windowID == 0 {
		label = "current window"
	}
	preview := text
	if len([]rune(preview)) > 100 {
		preview = string([]rune(preview)[:100]) + "..."
	}
	return Result{Content: fmt.Sprintf("kitty input sent to %s: %s", label, preview)}
}

func (k *KittyTool) executeSendKey(windowID int, key, modifiers string) Result {
	if strings.TrimSpace(key) == "" {
		return Result{IsError: true, Content: "key is required for send_key action"}
	}

	// Map common key names to kitty escape sequences for send-text.
	// For special keys that don't have simple text representations,
	// we use the `action` command instead.
	keyLower := strings.ToLower(strings.TrimSpace(key))

	// Check if modifiers are specified
	if strings.TrimSpace(modifiers) != "" {
		// With modifiers, use action command for key combos.
		// Map common Ctrl+key combos to kitty action names.
		return k.sendKeyViaAction(windowID, keyLower, modifiers)
	}

	// Without modifiers, try to send via send-text with escape sequences.
	escSeq, isSpecial := kittyKeyEscapeSeq(keyLower)
	if isSpecial {
		// Use send-text with the escape sequence.
		// kitty's send-text interprets these escape sequences when sent
		// as literal bytes via stdin.
		args := []string{"send-text", "--stdin"}
		m := matchID(windowID)
		if m != "" {
			args = append(args, "--match="+m)
		}

		bin := kittyBinaryPath()
		if bin == "" {
			return Result{IsError: true, Content: "kitty/kitten binary not found"}
		}

		cmdArgs := []string{"@"}
		if sock := os.Getenv("KITTY_LISTEN_ON"); sock != "" {
			cmdArgs = append(cmdArgs, "--to", sock)
		}
		cmdArgs = append(cmdArgs, args...)

		cmd := exec.Command(bin, cmdArgs...)
		cmd.Stdin = strings.NewReader(escSeq)
		_, err := cmd.Output()
		if err != nil {
			stderr := ""
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = strings.TrimSpace(string(ee.Stderr))
			}
			if stderr != "" {
				return Result{IsError: true, Content: fmt.Sprintf("kitty send_key failed: %s", stderr)}
			}
			return Result{IsError: true, Content: fmt.Sprintf("kitty send_key failed: %v", err)}
		}
	} else {
		// Regular character — send via send-text
		args := []string{"send-text"}
		m := matchID(windowID)
		if m != "" {
			args = append(args, "--match="+m)
		}
		args = append(args, key)

		_, err := kittyAt(args...)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("kitty send_key failed: %v", err)}
		}
	}

	label := fmt.Sprintf("window %d", windowID)
	if windowID == 0 {
		label = "current window"
	}
	return Result{Content: fmt.Sprintf("kitty key sent to %s: %s", label, key)}
}

// sendKeyViaAction sends key combos (with modifiers) via the `action` command.
// Kitty maps common key combos to action names.
func (k *KittyTool) sendKeyViaAction(windowID int, key, modifiers string) Result {
	// For Ctrl+C, Ctrl+V, etc., we use send-text with the control character.
	// Ctrl+letter = byte 1-26 (a=1, b=2, ..., z=26)
	mods := strings.Split(modifiers, ",")
	hasControl := false
	hasAlt := false
	for _, m := range mods {
		switch strings.TrimSpace(strings.ToLower(m)) {
		case "control", "ctrl":
			hasControl = true
		case "alt", "option":
			hasAlt = true
		}
	}

	if hasControl && len(key) == 1 && key[0] >= 'a' && key[0] <= 'z' {
		// Ctrl+letter → control character
		char := byte(key[0] - 'a' + 1)
		escSeq := string(rune(char))

		args := []string{"send-text", "--stdin"}
		mID := matchID(windowID)
		if mID != "" {
			args = append(args, "--match="+mID)
		}

		bin := kittyBinaryPath()
		if bin == "" {
			return Result{IsError: true, Content: "kitty/kitten binary not found"}
		}

		cmdArgs := []string{"@"}
		if sock := os.Getenv("KITTY_LISTEN_ON"); sock != "" {
			cmdArgs = append(cmdArgs, "--to", sock)
		}
		cmdArgs = append(cmdArgs, args...)

		cmd := exec.Command(bin, cmdArgs...)
		cmd.Stdin = strings.NewReader(escSeq)
		_, err := cmd.Output()
		if err != nil {
			stderr := ""
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = strings.TrimSpace(string(ee.Stderr))
			}
			if stderr != "" {
				return Result{IsError: true, Content: fmt.Sprintf("kitty send_key failed: %s", stderr)}
			}
			return Result{IsError: true, Content: fmt.Sprintf("kitty send_key failed: %v", err)}
		}

		return Result{Content: fmt.Sprintf("kitty key sent: %s+%s", modifiers, key)}
	}

	// For other modifier combos, try the action command.
	// Kitty has action names like "debug_config", "previous_tab", etc.
	// For generic combos, we fall back to send-text.
	if hasAlt && len(key) == 1 {
		// Alt+letter → ESC followed by letter
		escSeq := "\x1b" + key

		args := []string{"send-text", "--stdin"}
		mID := matchID(windowID)
		if mID != "" {
			args = append(args, "--match="+mID)
		}

		bin := kittyBinaryPath()
		if bin == "" {
			return Result{IsError: true, Content: "kitty/kitten binary not found"}
		}

		cmdArgs := []string{"@"}
		if sock := os.Getenv("KITTY_LISTEN_ON"); sock != "" {
			cmdArgs = append(cmdArgs, "--to", sock)
		}
		cmdArgs = append(cmdArgs, args...)

		cmd := exec.Command(bin, cmdArgs...)
		cmd.Stdin = strings.NewReader(escSeq)
		_, err := cmd.Output()
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("kitty send_key failed: %v", err)}
		}

		return Result{Content: fmt.Sprintf("kitty key sent: %s+%s", modifiers, key)}
	}

	return Result{IsError: true, Content: fmt.Sprintf("unsupported key combo: %s+%s (use simple keys with control/alt modifiers)", modifiers, key)}
}

// kittyKeyEscapeSeq returns the escape sequence for a special key name,
// and true if the key is a special key. Returns ("", false) for regular characters.
func kittyKeyEscapeSeq(key string) (string, bool) {
	switch key {
	case "enter", "return":
		return "\r", true
	case "tab":
		return "\t", true
	case "escape", "esc":
		return "\x1b", true
	case "backspace":
		return "\x7f", true
	case "up":
		return "\x1b[A", true
	case "down":
		return "\x1b[B", true
	case "right":
		return "\x1b[C", true
	case "left":
		return "\x1b[D", true
	case "home":
		return "\x1b[H", true
	case "end":
		return "\x1b[F", true
	case "pageup":
		return "\x1b[5~", true
	case "pagedown":
		return "\x1b[6~", true
	case "delete":
		return "\x1b[3~", true
	case "space":
		return " ", true
	default:
		return "", false
	}
}

func (k *KittyTool) executeResize(windowID int, axis string, increment int) Result {
	ax := strings.ToLower(strings.TrimSpace(axis))
	if ax == "" {
		ax = "horizontal"
	}
	switch ax {
	case "horizontal", "vertical":
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid axis %q (use horizontal or vertical)", axis)}
	}

	if increment == 0 {
		increment = 20
	}

	args := []string{"resize-window", "--axis=" + ax, fmt.Sprintf("--increment=%d", increment)}
	m := matchID(windowID)
	if m != "" {
		args = append(args, "--match="+m)
	}

	_, err := kittyAt(args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty resize failed: %v", err)}
	}

	label := fmt.Sprintf("window %d", windowID)
	if windowID == 0 {
		label = "current window"
	}
	return Result{Content: fmt.Sprintf("kitty resized %s: %s %s %+d", label, ax, "by", increment)}
}

func (k *KittyTool) executeGetText(windowID int) Result {
	args := []string{"get-text"}
	m := matchID(windowID)
	if m != "" {
		args = append(args, "--match="+m)
	}

	out, err := kittyAt(args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty get_text failed: %v", err)}
	}

	label := fmt.Sprintf("window %d", windowID)
	if windowID == 0 {
		label = "current window"
	}

	// Truncate very long output for readability
	if len([]rune(out)) > 5000 {
		out = string([]rune(out)[:5000]) + "\n... (truncated, use list to get window IDs)"
	}

	return Result{Content: fmt.Sprintf("kitty screen text from %s:\n%s", label, out)}
}

func (k *KittyTool) executeZoom(windowID int) Result {
	// Toggle between the current layout and the 'stack' layout (zoom).
	args := []string{"action", "toggle_layout", "stack"}
	m := matchID(windowID)
	if m != "" {
		// action doesn't support --match, but we focus the window first
		_, _ = kittyAt("focus-window", "--match="+m)
	}

	_, err := kittyAt(args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty zoom failed: %v", err)}
	}

	return Result{Content: "kitty zoom toggled"}
}

func (k *KittyTool) executeSetTabTitle(text string) Result {
	if strings.TrimSpace(text) == "" {
		return Result{IsError: true, Content: "text (tab title) is required for set_tab_title action"}
	}

	_, err := kittyAt("set-tab-title", text)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty set_tab_title failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("kitty tab title set: %s", text)}
}

func (k *KittyTool) executeAction(windowID int, actionStr string) Result {
	if strings.TrimSpace(actionStr) == "" {
		return Result{IsError: true, Content: "text (action name) is required for action command"}
	}

	// Focus the target window first if a specific ID is given.
	m := matchID(windowID)
	if m != "" {
		_, _ = kittyAt("focus-window", "--match="+m)
	}

	args := []string{"action"}
	// Split actionStr into parts (e.g. "scroll_page_down" or "send_key string f1")
	parts := strings.Fields(actionStr)
	args = append(args, parts...)

	_, err := kittyAt(args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty action failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("kitty action performed: %s", actionStr)}
}

func (k *KittyTool) executeReloadConfig() Result {
	_, err := kittyAt("load-config")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty reload_config failed: %v", err)}
	}
	return Result{Content: "kitty config reloaded"}
}
