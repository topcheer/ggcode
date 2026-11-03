package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func kittyBinaryPath() string {

	if p, err := exec.LookPath("kitten"); err == nil {
		return p
	}

	if p, err := exec.LookPath("kitty"); err == nil {
		return p
	}

	if runtime.GOOS == "darwin" {
		for _, name := range []string{"kitten", "kitty"} {
			p := fmt.Sprintf("/Applications/kitty.app/Contents/MacOS/%s", name)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}

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

func kittyAtCtx(ctx context.Context, args ...string) (string, error) {
	bin := kittyBinaryPath()
	if bin == "" {
		return "", fmt.Errorf("kitty/kitten binary not found")
	}

	cmdArgs := []string{"@"}
	if sock := os.Getenv("KITTY_LISTEN_ON"); sock != "" {
		cmdArgs = append(cmdArgs, "--to", sock)
	}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.CommandContext(ctx, bin, cmdArgs...)
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

func matchID(windowID int) string {
	if windowID > 0 {
		return fmt.Sprintf("id:%d", windowID)
	}
	return ""
}

func errInvalidWindowID(windowID int) Result {
	return Result{IsError: true, Content: fmt.Sprintf("invalid window_id %d - kitty window ids are positive; 0 targets the focused window", windowID)}
}

func (k *KittyTool) executeStatus(ctx context.Context) Result {
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

	if binPath != "" {
		if v, err := exec.CommandContext(ctx, binPath, "--version").Output(); err == nil {
			b.WriteString(fmt.Sprintf("version: %s", strings.TrimSpace(string(v))))
		}
	}

	return Result{Content: b.String()}
}

func (k *KittyTool) executeList(ctx context.Context) Result {
	out, err := kittyAtCtx(ctx, "ls")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty list failed: %v", err)}
	}

	return Result{Content: out}
}

func (k *KittyTool) executeSplit(ctx context.Context, windowID int, direction string, size int, command, workingDir string) Result {
	if windowID < 0 {
		return errInvalidWindowID(windowID)
	}
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir == "" {
		dir = "right"
	}

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

	if windowID > 0 {
		args = append(args, "--match="+matchID(windowID))
	}

	if size != 0 {
		if size < 1 || size > 99 {
			return Result{IsError: true, Content: fmt.Sprintf("invalid size %d: kitty bias maps the new window's share and must be 1-99 (0 = unspecified)", size)}
		}
		args = append(args, fmt.Sprintf("--bias=%d", size))
	}

	if dir == "left" || dir == "up" {

	}

	if strings.TrimSpace(command) != "" {
		args = append(args, "--", "/bin/sh", "-c", command)
	}

	out, err := kittyAtCtx(ctx, args...)
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

func (k *KittyTool) executeNewTab(ctx context.Context, command, workingDir string) Result {
	wd := strings.TrimSpace(workingDir)
	if wd == "" {
		wd = k.workingDir()
	}

	args := []string{"launch", "--type=tab", "--cwd=" + wd}
	if strings.TrimSpace(command) != "" {
		args = append(args, "--", "/bin/sh", "-c", command)
	}

	out, err := kittyAtCtx(ctx, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty new_tab failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("kitty tab created\n%s", out)}
}

func (k *KittyTool) executeNewWindow(ctx context.Context, command, workingDir string) Result {
	wd := strings.TrimSpace(workingDir)
	if wd == "" {
		wd = k.workingDir()
	}

	args := []string{"launch", "--type=os-window", "--cwd=" + wd}
	if strings.TrimSpace(command) != "" {
		args = append(args, "--", "/bin/sh", "-c", command)
	}

	out, err := kittyAtCtx(ctx, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty new_window failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("kitty OS window created\n%s", out)}
}

func (k *KittyTool) executeFocus(ctx context.Context, windowID int) Result {
	if windowID < 0 {
		return errInvalidWindowID(windowID)
	}
	args := []string{"focus-window"}
	m := matchID(windowID)
	if m != "" {
		args = append(args, "--match="+m)
	}

	_, err := kittyAtCtx(ctx, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty focus failed: %v", err)}
	}

	label := fmt.Sprintf("window %d", windowID)
	if windowID == 0 {
		label = "current window"
	}
	return Result{Content: fmt.Sprintf("kitty focused: %s", label)}
}

func (k *KittyTool) executeClose(ctx context.Context, windowID int) Result {
	if windowID < 0 {
		return errInvalidWindowID(windowID)
	}
	args := []string{"close-window"}
	m := matchID(windowID)
	if m != "" {
		args = append(args, "--match="+m)
	}

	_, err := kittyAtCtx(ctx, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty close failed: %v", err)}
	}

	label := fmt.Sprintf("window %d", windowID)
	if windowID == 0 {
		label = "current window"
	}
	return Result{Content: fmt.Sprintf("kitty closed: %s", label)}
}

func (k *KittyTool) executeCloseTab(ctx context.Context) Result {
	_, err := kittyAtCtx(ctx, "close-tab")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty close_tab failed: %v", err)}
	}
	return Result{Content: "kitty tab closed"}
}

func (k *KittyTool) executeSelectTab(ctx context.Context, tabIndex int) Result {
	if tabIndex < 1 {
		return Result{IsError: true, Content: "tab_index must be >= 1 (1-based)"}
	}

	_, err := kittyAtCtx(ctx, "focus-tab", fmt.Sprintf("--match=index:%d", tabIndex-1))
	if err != nil {

		_, err2 := kittyAtCtx(ctx, "focus-tab", fmt.Sprintf("--match=order:%d", tabIndex-1))
		if err2 != nil {

			return Result{IsError: true, Content: fmt.Sprintf("kitty select_tab failed: index matcher: %v; order matcher: %v", err, err2)}
		}
	}

	return Result{Content: fmt.Sprintf("kitty selected tab: %d", tabIndex)}
}

func (k *KittyTool) executeInput(ctx context.Context, windowID int, text string) Result {
	if windowID < 0 {
		return errInvalidWindowID(windowID)
	}
	if strings.TrimSpace(text) == "" {
		return Result{IsError: true, Content: "text is required for input action"}
	}

	args := kittySendTextArgs(windowID, text)

	_, err := kittyAtCtx(ctx, args...)
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

func (k *KittyTool) executeSendKey(ctx context.Context, windowID int, key, modifiers string) Result {
	if windowID < 0 {
		return errInvalidWindowID(windowID)
	}
	if strings.TrimSpace(key) == "" {
		return Result{IsError: true, Content: "key is required for send_key action"}
	}

	keyLower := strings.ToLower(strings.TrimSpace(key))

	if strings.TrimSpace(modifiers) != "" {

		return k.sendKeyViaAction(ctx, windowID, keyLower, modifiers)
	}

	escSeq, isSpecial := kittyKeyEscapeSeq(keyLower)
	if isSpecial {

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

		cmd := exec.CommandContext(ctx, bin, cmdArgs...)
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

		args := kittySendTextArgs(windowID, key)

		_, err := kittyAtCtx(ctx, args...)
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

func (k *KittyTool) sendKeyViaAction(ctx context.Context, windowID int, key, modifiers string) Result {
	if windowID < 0 {
		return errInvalidWindowID(windowID)
	}

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

		escSeq := string(rune(key[0] - 'a' + 1))

		if hasAlt {
			escSeq = "\x1b" + escSeq
		}

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

		cmd := exec.CommandContext(ctx, bin, cmdArgs...)
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

	if hasAlt && len(key) == 1 {

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

		cmd := exec.CommandContext(ctx, bin, cmdArgs...)
		cmd.Stdin = strings.NewReader(escSeq)
		_, err := cmd.Output()
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("kitty send_key failed: %v", err)}
		}

		return Result{Content: fmt.Sprintf("kitty key sent: %s+%s", modifiers, key)}
	}

	return Result{IsError: true, Content: fmt.Sprintf("unsupported key combo: %s+%s (use simple keys with control/alt modifiers)", modifiers, key)}
}

func kittySendTextArgs(windowID int, text string) []string {
	args := []string{"send-text"}
	if m := matchID(windowID); m != "" {
		args = append(args, "--match="+m)
	}
	return append(args, "--", text)
}

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

	case "f1":
		return "\x1bOP", true
	case "f2":
		return "\x1bOQ", true
	case "f3":
		return "\x1bOR", true
	case "f4":
		return "\x1bOS", true
	case "f5":
		return "\x1b[15~", true
	case "f6":
		return "\x1b[17~", true
	case "f7":
		return "\x1b[18~", true
	case "f8":
		return "\x1b[19~", true
	case "f9":
		return "\x1b[20~", true
	case "f10":
		return "\x1b[21~", true
	case "f11":
		return "\x1b[23~", true
	case "f12":
		return "\x1b[24~", true
	default:
		return "", false
	}
}

func (k *KittyTool) executeResize(ctx context.Context, windowID int, axis string, increment int) Result {
	if windowID < 0 {
		return errInvalidWindowID(windowID)
	}
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

	_, err := kittyAtCtx(ctx, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty resize failed: %v", err)}
	}

	label := fmt.Sprintf("window %d", windowID)
	if windowID == 0 {
		label = "current window"
	}
	return Result{Content: fmt.Sprintf("kitty resized %s: %s %s %+d", label, ax, "by", increment)}
}

func (k *KittyTool) executeGetText(ctx context.Context, windowID int) Result {
	if windowID < 0 {
		return errInvalidWindowID(windowID)
	}
	args := []string{"get-text"}
	m := matchID(windowID)
	if m != "" {
		args = append(args, "--match="+m)
	}

	out, err := kittyAtCtx(ctx, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty get_text failed: %v", err)}
	}

	label := fmt.Sprintf("window %d", windowID)
	if windowID == 0 {
		label = "current window"
	}

	if len([]rune(out)) > 5000 {
		out = string([]rune(out)[:5000]) + "\n... (truncated, use list to get window IDs)"
	}

	return Result{Content: fmt.Sprintf("kitty screen text from %s:\n%s", label, out)}
}

func (k *KittyTool) executeZoom(ctx context.Context, windowID int) Result {
	if windowID < 0 {
		return errInvalidWindowID(windowID)
	}

	args := []string{"action", "toggle_layout", "stack"}
	m := matchID(windowID)
	if m != "" {

		if _, ferr := kittyAtCtx(ctx, "focus-window", "--match="+m); ferr != nil {
			return Result{IsError: true, Content: fmt.Sprintf("kitty zoom: focusing window %d failed: %v", windowID, ferr)}
		}
	}

	_, err := kittyAtCtx(ctx, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty zoom failed: %v", err)}
	}

	return Result{Content: "kitty zoom toggled"}
}

func (k *KittyTool) executeSetTabTitle(ctx context.Context, text string) Result {
	if strings.TrimSpace(text) == "" {
		return Result{IsError: true, Content: "text (tab title) is required for set_tab_title action"}
	}

	_, err := kittyAtCtx(ctx, "set-tab-title", "--", text)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty set_tab_title failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("kitty tab title set: %s", text)}
}

func (k *KittyTool) executeAction(ctx context.Context, windowID int, actionStr string) Result {
	if windowID < 0 {
		return errInvalidWindowID(windowID)
	}
	if strings.TrimSpace(actionStr) == "" {
		return Result{IsError: true, Content: "text (action name) is required for action command"}
	}

	m := matchID(windowID)
	if m != "" {

		if _, ferr := kittyAtCtx(ctx, "focus-window", "--match="+m); ferr != nil {
			return Result{IsError: true, Content: fmt.Sprintf("kitty action: focusing window %d failed: %v", windowID, ferr)}
		}
	}

	args := []string{"action"}

	parts := strings.Fields(actionStr)
	args = append(args, parts...)

	_, err := kittyAtCtx(ctx, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty action failed: %v", err)}
	}

	return Result{Content: fmt.Sprintf("kitty action performed: %s", actionStr)}
}

func (k *KittyTool) executeReloadConfig(ctx context.Context) Result {
	_, err := kittyAtCtx(ctx, "load-config")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("kitty reload_config failed: %v", err)}
	}
	return Result{Content: "kitty config reloaded"}
}
