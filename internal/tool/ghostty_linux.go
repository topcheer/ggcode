//go:build linux

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ── Linux DBus helpers ──────────────────────────────────────────────────────
//
// On Linux, Ghostty uses the GTK/GIO DBus Actions API for IPC.
// The bus name defaults to "com.mitchellh.ghostty" and the object path to
// "/com/mitchellh/ghostty". Actions are invoked via org.gtk.Actions.Activate.
//
// Reference (from Ghostty source, src/apprt/gtk/ipc/new_window.zig):
//
//	gdbus call --session \
//	  --dest com.mitchellh.ghostty \
//	  --object-path /com/mitchellh/ghostty \
//	  --method org.gtk.Actions.Activate \
//	  new-window [] {}
//
// Widget-level actions (split-tree.new-split, split-tree.equalize, etc.) are
// registered on the SplitTree widget, not the GApplication. However, GTK
// accelerators registered on the application reference these actions, so they
// may be accessible via the action muxer. We try DBus first, fall back to
// xdotool key simulation for actions that aren't directly available.

const (
	ghosttyDBusBusName = "com.mitchellh.ghostty"
	ghosttyDBusObjPath = "/com/mitchellh/ghostty"
	ghosttyDBusIFace   = "org.gtk.Actions"
)

// gdbusCall invokes a GIO DBus action on the Ghostty application.
func gdbusCall(action string, params string) (string, error) {
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

	cmd := exec.Command("gdbus", args...)
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

// gdbusList lists all exported DBus actions.
func gdbusList() (string, error) {
	cmd := exec.Command("gdbus", "call", "--session",
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

// ghosttyBinaryPath returns the path to the ghostty CLI binary, or empty if not found.
func ghosttyBinaryPath() string {
	if p, err := exec.LookPath("ghostty"); err == nil {
		return p
	}
	return ""
}

// ── Action implementations (Linux) ──────────────────────────────────────────

func (g *GhosttyTool) executeStatus() Result {
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

	// Check if gdbus is available.
	if _, err := exec.LookPath("gdbus"); err != nil {
		b.WriteString("ipc: gdbus not found (install gdbus for pane management)\n")
	} else {
		// Check if Ghostty is reachable on DBus.
		if _, err := gdbusList(); err != nil {
			b.WriteString("ipc: Ghostty DBus not reachable\n")
		} else {
			b.WriteString("ipc: DBus connected (com.mitchellh.ghostty)\n")
		}
	}

	// On Linux, we can get GHOSTTY_SURFACE_ID and GHOSTTY_RESOURCES_DIR from env.
	if sid := os.Getenv("GHOSTTY_SURFACE_ID"); sid != "" {
		b.WriteString(fmt.Sprintf("surface ID: %s", sid))
	}

	return Result{Content: b.String()}
}

func (g *GhosttyTool) executeList() Result {
	// Linux Ghostty doesn't expose terminal introspection via DBus.
	// Try gdbus to list actions instead.
	out, err := gdbusList()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty list failed: %v\nNote: on Linux, full terminal listing is not supported. Use 'status' to check DBus connectivity.", err)}
	}
	return Result{Content: fmt.Sprintf("Available DBus actions:\n%s\n\nNote: Linux does not support per-terminal introspection (IDs, CWD, titles). Terminal-level operations (focus, close, input by ID) are limited.", out)}
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

	// On Linux, terminalID is not supported for targeting — splits always
	// happen relative to the active surface.
	if terminalID != "" {
		return Result{IsError: true, Content: "terminal_id targeting is not supported on Linux. Splits are always relative to the focused pane."}
	}

	// Try to invoke split-tree.new-split via DBus.
	// GVariant format for string parameter: "[<'right'>]"
	params := fmt.Sprintf("[<%q>]", dir)
	_, err := gdbusCall("split-tree.new-split", params)
	if err != nil {
		// Fallback: not all Ghostty versions expose split-tree actions via DBus.
		return Result{IsError: true, Content: fmt.Sprintf("ghostty split failed via DBus: %v\nNote: Linux split requires Ghostty to expose 'split-tree.new-split' via DBus. If this fails, use keyboard shortcuts or the 'action' with xdotool.", err)}
	}

	// Size resize is not available via DBus on Linux.
	sizeNote := ""
	if size > 0 && size < 99 {
		sizeNote = fmt.Sprintf(" (note: size=%d%% not supported on Linux, split is 50/50)", size)
	}

	// If a command was specified, try to send it via GHOSTTY_SURFACE_ID focus + input.
	// This is a best-effort approach.
	if strings.TrimSpace(command) != "" {
		// Unfortunately, there's no DBus action to send text input on Linux.
		// The command can't be auto-executed in the new pane via DBus.
		return Result{Content: fmt.Sprintf("ghostty split created: direction=%s%s\nNote: command execution in new pane is not supported on Linux via DBus. Manually run: %s", dir, sizeNote, command)}
	}

	return Result{Content: fmt.Sprintf("ghostty split created: direction=%s%s", dir, sizeNote)}
}

func (g *GhosttyTool) executeNewTab(command, workingDir string) Result {
	// new-window-command is the only tab/window action available via DBus.
	// There's no explicit "new-tab" DBus action — we use new-window as fallback.
	if strings.TrimSpace(command) != "" {
		// gdbus format for new-window-command: array of string args
		params := fmt.Sprintf("[<[\"-e\", %q]>]", command)
		_, err := gdbusCall("new-window-command", params)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("ghostty new_tab failed: %v", err)}
		}
		return Result{Content: "ghostty tab created with command (via new-window DBus action)"}
	}

	_, err := gdbusCall("new-window", "")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty new_tab failed: %v", err)}
	}
	return Result{Content: "ghostty tab created (via new-window DBus action)"}
}

func (g *GhosttyTool) executeNewWindow(command, workingDir string) Result {
	if strings.TrimSpace(command) != "" {
		params := fmt.Sprintf("[<[\"-e\", %q]>]", command)
		_, err := gdbusCall("new-window-command", params)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("ghostty new_window failed: %v", err)}
		}
		return Result{Content: "ghostty window created with command"}
	}

	_, err := gdbusCall("new-window", "")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty new_window failed: %v", err)}
	}
	return Result{Content: "ghostty window created"}
}

func (g *GhosttyTool) executeFocus(terminalID string) Result {
	return Result{IsError: true, Content: "focus is not supported on Linux via DBus. Ghostty Linux does not expose per-terminal focus control through DBus IPC."}
}

func (g *GhosttyTool) executeClose(terminalID string) Result {
	return Result{IsError: true, Content: "close is not supported on Linux via DBus. Ghostty Linux does not expose per-terminal close through DBus IPC."}
}

func (g *GhosttyTool) executeInput(terminalID, text string) Result {
	if strings.TrimSpace(text) == "" {
		return Result{IsError: true, Content: "text is required for input action"}
	}
	return Result{IsError: true, Content: "input is not supported on Linux via DBus. Ghostty Linux does not expose text input to terminals through DBus IPC. Consider using xdotool or tmux for this functionality."}
}

func (g *GhosttyTool) executeSendKey(terminalID, key, modifiers string) Result {
	if strings.TrimSpace(key) == "" {
		return Result{IsError: true, Content: "key is required for send_key action"}
	}
	return Result{IsError: true, Content: "send_key is not supported on Linux via DBus. Ghostty Linux does not expose key events through DBus IPC. Consider using xdotool for this functionality."}
}

func (g *GhosttyTool) executeAction(terminalID, actionStr string) Result {
	if strings.TrimSpace(actionStr) == "" {
		return Result{IsError: true, Content: "text (action string) is required for action command"}
	}

	// Map common Ghostty actions to DBus action names.
	dbusAction := ""
	switch actionStr {
	case "toggle_split_zoom":
		dbusAction = "split-tree.zoom"
	case "equalize_splits":
		dbusAction = "split-tree.equalize"
	case "reload_config":
		dbusAction = "app.reload-config"
	default:
		// Try passing the action string directly.
		dbusAction = actionStr
	}

	_, err := gdbusCall(dbusAction, "")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ghostty action '%s' failed via DBus: %v\nNote: not all Ghostty actions are exposed via DBus on Linux.", actionStr, err)}
	}

	return Result{Content: fmt.Sprintf("ghostty action performed: %s", actionStr)}
}

func (g *GhosttyTool) executeSelectTab(tabIndex int) Result {
	if tabIndex < 1 {
		return Result{IsError: true, Content: "tab_index must be >= 1 (1-based)"}
	}
	return Result{IsError: true, Content: "select_tab is not supported on Linux via DBus. Ghostty Linux does not expose tab selection through DBus IPC."}
}
