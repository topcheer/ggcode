//go:build darwin

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

func executeDesktopControl(ctx context.Context, p desktopParams) (Result, error) {
	switch p.Action {
	// ── Mouse ──
	case "click":
		return cliclickResult(ctx, fmt.Sprintf("c:%d,%d", p.X, p.Y))
	case "double_click":
		return cliclickResult(ctx, fmt.Sprintf("dc:%d,%d", p.X, p.Y))
	case "right_click":
		return cliclickResult(ctx, fmt.Sprintf("rc:%d,%d", p.X, p.Y))
	case "move":
		return cliclickResult(ctx, fmt.Sprintf("m:%d,%d", p.X, p.Y))
	case "drag":
		return cliclickResult(ctx, fmt.Sprintf("dd:%d,%d", p.ToX, p.ToY))
	case "scroll":
		// cliclick uses negative for down, positive for up
		amt := p.Amount
		if p.Direction == "down" {
			amt = -amt
		}
		return cliclickResult(ctx, fmt.Sprintf("scroll:%d,%d,%d", p.X, p.Y, amt))

	// ── Keyboard ──
	case "type":
		// Escape double quotes in the text for cliclick
		escaped := strings.ReplaceAll(p.Text, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		return cliclickResult(ctx, fmt.Sprintf("t:\"%s\"", escaped))
	case "key_press":
		return cliclickResult(ctx, fmt.Sprintf("kp:%s", p.Text))
	case "key_combo":
		return cliclickResult(ctx, fmt.Sprintf("kp:%s", p.Text))

	// ── Window management ──
	case "list_windows":
		return appleScriptResult(ctx, `
tell application "System Events"
  set output to ""
  repeat with proc in (every process whose background only is false)
    set output to output & (name of proc) & "\n"
    repeat with w in windows of proc
      try
        set output to output & "  " & (name of w) & "\n"
      end try
    end repeat
  end repeat
  return output
end tell`)
	case "focus_window":
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application "%s"
  activate
end tell`, p.Text))
	case "close_window":
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application "%s"
  close front window
end tell`, p.Text))
	case "minimize_window":
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application "%s"
  set miniaturized of front window to true
end tell`, p.Text))
	case "maximize_window":
		// macOS doesn't have true maximize; use fullscreen toggle
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application "%s"
  set fullscreen of front window to not fullscreen of front window
end tell`, p.Text))

	// ── Application ──
	case "launch_app":
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application "%s"
  activate
end tell`, p.Text))
	case "quit_app":
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application "%s"
  quit
end tell`, p.Text))
	case "list_apps":
		return appleScriptResult(ctx, `
tell application "System Events"
  set output to ""
  repeat with proc in (every process whose background only is false)
    set output to output & (name of proc) & "\n"
  end repeat
  return output
end tell`)
	case "active_app":
		return appleScriptResult(ctx, `
tell application "System Events"
  return name of first process whose frontmost is true
end tell`)

	default:
		return Result{}, fmt.Errorf("unknown action: %s", p.Action)
	}
}

// cliclickResult runs a cliclick command. cliclick is a macOS utility
// for sending mouse/keyboard events. If not installed, it tries to
// install via Homebrew.
func cliclickResult(ctx context.Context, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, "cliclick", args...)
	out, err := cmd.Output()
	if err != nil {
		// Check if cliclick is missing
		if isCommandNotFound(err) {
			// Try to install via brew
			installOut, installErr := exec.CommandContext(ctx, "brew", "install", "cliclick").CombinedOutput()
			if installErr != nil {
				return Result{}, fmt.Errorf("cliclick is not installed and brew install failed: %w\n%s", installErr, string(installOut))
			}
			// Retry
			out, err = exec.CommandContext(ctx, "cliclick", args...).Output()
			if err != nil {
				return Result{}, fmt.Errorf("cliclick failed after install: %w", err)
			}
		} else {
			return Result{}, fmt.Errorf("cliclick failed: %w", err)
		}
	}
	result := fmt.Sprintf("OK: cliclick %s", strings.Join(args, " "))
	if len(out) > 0 {
		result += "\n" + strings.TrimSpace(string(out))
	}
	return Result{Content: result}, nil
}

func appleScriptResult(ctx context.Context, script string) (Result, error) {
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("osascript failed: %w", err)
	}
	result := strings.TrimSpace(string(out))
	if result == "" {
		result = "OK"
	}
	return Result{Content: result}, nil
}

func isCommandNotFound(err error) bool {
	if exitErr, ok := err.(*exec.ExitError); ok {
		// On macOS, command not found gives exit status 127
		return exitErr.ExitCode() == 127
	}
	return strings.Contains(err.Error(), "executable file not found") ||
		strings.Contains(err.Error(), "no such file or directory")
}

// Ensure desktopControlResult is used (placeholder for JSON marshaling consistency)
var _ = json.Marshal
