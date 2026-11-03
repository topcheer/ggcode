//go:build linux

package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	ghosttyDBusBusName = "com.mitchellh.ghostty"
	ghosttyDBusObjPath = "/com/mitchellh/ghostty"
	ghosttyDBusIFace   = "org.gtk.Actions"
)

func gdbusCall(ctx context.Context, action string, params string) (string, error) {
	args := []string{
		"call", "--session",
		"--dest", ghosttyDBusBusName,
		"--object-path", ghosttyDBusObjPath,
		"--method", ghosttyDBusIFace + ".Activate",
		action,
	}
	if params == "" {
		args = append(args, "[]", "{}")
	} else {
		args = append(args, params, "{}")
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "gdbus", args...)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		if stderr != "" {
			return "", fmt.Errorf("gdbus: %s", stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gdbusList(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "gdbus", "call", "--session",
		"--dest", ghosttyDBusBusName,
		"--object-path", ghosttyDBusObjPath,
		"--method", ghosttyDBusIFace+".List")
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		if stderr != "" {
			return "", fmt.Errorf("gdbus: %s", stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func ghosttyBinaryPath() string {
	if p, err := exec.LookPath("ghostty"); err == nil {
		return p
	}
	return ""
}

func (g *GhosttyTool) executeStatus(ctx context.Context) Result {
	if !ghosttyAvailable() {
		return Result{Content: "ghostty: not detected (TERM_PROGRAM != ghostty)"}
	}

	var b strings.Builder
	b.WriteString("ghostty: detected\n")
	b.WriteString(fmt.Sprintf("platform: %s/%s\n", runtime.GOOS, runtime.GOARCH))

	binPath := ghosttyBinaryPath()
	if binPath != "" {
		b.WriteString(fmt.Sprintf("binary: %s\n", binPath))
	}

	if _, err := exec.LookPath("gdbus"); err != nil {
		b.WriteString("ipc: gdbus not found (install gdbus for pane management)\n")
	} else {

		if _, err := gdbusList(ctx); err != nil {
			b.WriteString("ipc: Ghostty DBus not reachable\n")
		} else {
			b.WriteString("ipc: DBus connected (com.mitchellh.ghostty)\n")
		}
	}

	if sid := os.Getenv("GHOSTTY_SURFACE_ID"); sid != "" {
		b.WriteString(fmt.Sprintf("surface ID: %s", sid))
	}

	return Result{Content: b.String()}
}

func (g *GhosttyTool) executeList(ctx context.Context) Result {

	out, err := gdbusList(ctx)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty list failed: %v\nNote: on Linux, full terminal listing is not supported. Use 'status' to check DBus connectivity.", err)}
	}
	return Result{Content: fmt.Sprintf("Available DBus actions:\n%s\n\nNote: Linux does not support per-terminal introspection (IDs, CWD, titles). Terminal-level operations (focus, close, input by ID) are limited.", out)}
}

func (g *GhosttyTool) executeSplit(ctx context.Context, terminalID, direction string, size int, command, workingDir string) Result {
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir == "" {
		dir = "right"
	}
	switch dir {
	case "right", "left", "down", "up":
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid direction %q (use right/left/down/up)", direction)}
	}

	if terminalID != "" {
		return Result{IsError: true, Content: "terminal_id targeting is not supported on Linux. Splits are always relative to the focused pane."}
	}

	params := fmt.Sprintf("[<%q>]", dir)
	_, err := gdbusCall(ctx, "split-tree.new-split", params)
	if err != nil {

		return Result{IsError: true, Content: fmt.Sprintf("ghostty split failed via DBus: %v\nNote: Linux split requires Ghostty to expose 'split-tree.new-split' via DBus. If this fails, use keyboard shortcuts or the 'action' with xdotool.", err)}
	}

	sizeNote := ""
	if size > 0 && size <= 99 {
		sizeNote = fmt.Sprintf(" (note: size=%d%% not supported on Linux, split is 50/50)", size)
	}

	if strings.TrimSpace(command) != "" {

		return Result{Content: fmt.Sprintf("ghostty split created: direction=%s%s\nNote: command execution in new pane is not supported on Linux via DBus. Manually run: %s", dir, sizeNote, command)}
	}

	return Result{Content: fmt.Sprintf("ghostty split created: direction=%s%s", dir, sizeNote)}
}

func escapeShellSingleQuoteLinux(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func ghosttyLinuxCommandArgv(command, wd string) []string {
	shellCmd := command
	if wd = strings.TrimSpace(wd); wd != "" {
		shellCmd = fmt.Sprintf("cd %s && %s", escapeShellSingleQuoteLinux(wd), command)
	}
	return []string{"sh", "-c", shellCmd}
}

func ghosttyLinuxNewWindowCommandParams(argv []string) string {
	q := make([]string, 0, len(argv))
	q = append(q, "\"-e\"")
	for _, a := range argv {
		q = append(q, fmt.Sprintf("%q", a))
	}
	return "[<[" + strings.Join(q, ", ") + "]>]"
}

func ghosttyLinuxCdOnlyArgv(wd string) []string {
	return []string{"sh", "-c", fmt.Sprintf("cd %s", escapeShellSingleQuoteLinux(wd))}
}

func (g *GhosttyTool) resolveWorkingDir(workingDir string) string {
	if wd := strings.TrimSpace(workingDir); wd != "" {
		return wd
	}
	return g.workingDir()
}

func (g *GhosttyTool) executeNewTab(ctx context.Context, command, workingDir string) Result {

	wd := g.resolveWorkingDir(workingDir)

	if strings.TrimSpace(command) != "" {

		argv := ghosttyLinuxCommandArgv(command, wd)
		params := ghosttyLinuxNewWindowCommandParams(argv)
		_, err := gdbusCall(ctx, "new-window-command", params)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("ghostty new_tab failed: %v", err)}
		}
		return Result{Content: "ghostty tab created with command (via new-window DBus action)"}
	}

	_, err := gdbusCall(ctx, "new-window-command", ghosttyLinuxNewWindowCommandParams(ghosttyLinuxCdOnlyArgv(wd)))
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty new_tab failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("ghostty tab created (via new-window DBus action, cwd %s)", wd)}
}

func (g *GhosttyTool) executeNewWindow(ctx context.Context, command, workingDir string) Result {

	wd := g.resolveWorkingDir(workingDir)
	if strings.TrimSpace(command) != "" {

		argv := ghosttyLinuxCommandArgv(command, wd)
		params := ghosttyLinuxNewWindowCommandParams(argv)
		_, err := gdbusCall(ctx, "new-window-command", params)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("ghostty new_window failed: %v", err)}
		}
		return Result{Content: "ghostty window created with command"}
	}

	_, err := gdbusCall(ctx, "new-window-command", ghosttyLinuxNewWindowCommandParams(ghosttyLinuxCdOnlyArgv(wd)))
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty new_window failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("ghostty window created (cwd %s)", wd)}
}

func (g *GhosttyTool) executeFocus(ctx context.Context, terminalID string) Result {
	return Result{IsError: true, Content: "focus is not supported on Linux via DBus. Ghostty Linux does not expose per-terminal focus control through DBus IPC."}
}

func (g *GhosttyTool) executeClose(ctx context.Context, terminalID string) Result {
	return Result{IsError: true, Content: "close is not supported on Linux via DBus. Ghostty Linux does not expose per-terminal close through DBus IPC."}
}

func (g *GhosttyTool) executeInput(ctx context.Context, terminalID, text string) Result {
	if strings.TrimSpace(text) == "" {
		return Result{IsError: true, Content: "text is required for input action"}
	}
	return Result{IsError: true, Content: "input is not supported on Linux via DBus. Ghostty Linux does not expose text input to terminals through DBus IPC. Consider using xdotool or tmux for this functionality."}
}

func (g *GhosttyTool) executeSendKey(ctx context.Context, terminalID, key, modifiers string) Result {
	if strings.TrimSpace(key) == "" {
		return Result{IsError: true, Content: "key is required for send_key action"}
	}
	return Result{IsError: true, Content: "send_key is not supported on Linux via DBus. Ghostty Linux does not expose key events through DBus IPC. Consider using xdotool for this functionality."}
}

func (g *GhosttyTool) executeAction(ctx context.Context, terminalID, actionStr string) Result {
	if strings.TrimSpace(actionStr) == "" {
		return Result{IsError: true, Content: "text (action string) is required for action command"}
	}

	dbusAction := ""
	switch actionStr {
	case "toggle_split_zoom":
		dbusAction = "split-tree.zoom"
	case "equalize_splits":
		dbusAction = "split-tree.equalize"
	case "reload_config":
		dbusAction = "app.reload-config"
	default:

		dbusAction = actionStr
	}

	_, err := gdbusCall(ctx, dbusAction, "")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty action '%s' failed via DBus: %v\nNote: not all Ghostty actions are exposed via DBus on Linux.", actionStr, err)}
	}

	return Result{Content: fmt.Sprintf("ghostty action performed: %s", actionStr)}
}

func (g *GhosttyTool) executeSelectTab(ctx context.Context, tabIndex int) Result {
	if tabIndex < 1 {
		return Result{IsError: true, Content: "tab_index must be >= 1 (1-based)"}
	}
	return Result{IsError: true, Content: "select_tab is not supported on Linux via DBus. Ghostty Linux does not expose tab selection through DBus IPC."}
}
