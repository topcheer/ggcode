//go:build linux

package tool

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func executeDesktopControl(ctx context.Context, p desktopParams) (Result, error) {
	switch p.Action {
	// ── Mouse ──
	case "click":
		return xdotoolResult(ctx, "click", "1") // left=1, right=3
	case "double_click":
		return xdotoolResult(ctx, "click", "--repeat", "2", "1")
	case "right_click":
		return xdotoolResult(ctx, "click", "3")
	case "move":
		return xdotoolResult(ctx, "mousemove", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y))
	case "drag":
		// Move then click-drag
		_, _ = exec.CommandContext(ctx, "xdotool", "mousemove", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y)).Output()
		return xdotoolResult(ctx, "mousemove", "--window", "%1", fmt.Sprintf("%d", p.ToX), fmt.Sprintf("%d", p.ToY))
	case "scroll":
		btn := "4" // up
		if p.Direction == "down" {
			btn = "5"
		}
		args := []string{"click", "--repeat", fmt.Sprintf("%d", p.Amount), btn}
		return xdotoolResult(ctx, args...)

	// ── Keyboard ──
	case "type":
		return xdotoolResult(ctx, "type", p.Text)
	case "key_press", "key_combo":
		// xdotool uses + for combos: ctrl+c, alt+Tab
		return xdotoolResult(ctx, "key", p.Text)

	// ── Window management ──
	case "list_windows":
		return xdotoolResult(ctx, "search", "", "--name")
	case "focus_window":
		return xdotoolResult(ctx, "search", "--name", p.Text, "windowactivate", "%@")
	case "close_window":
		return xdotoolResult(ctx, "search", "--name", p.Text, "windowclose", "%@")
	case "minimize_window":
		return xdotoolResult(ctx, "search", "--name", p.Text, "windowminimize", "%@")
	case "maximize_window":
		return xdotoolResult(ctx, "search", "--onlyvisible", "--class", "", "windowstate", "--toggle", "--maximized")

	// ── Application ──
	case "launch_app":
		return runAppResult(ctx, p.Text)
	case "quit_app":
		return xdotoolResult(ctx, "search", "--class", p.Text, "windowclose", "%@")
	case "list_apps":
		return wmctrlResult(ctx, "-l")
	case "active_app":
		return xdotoolResult(ctx, "getactivewindow", "getwindowname")

	default:
		return Result{}, fmt.Errorf("unknown action: %s", p.Action)
	}
}

func xdotoolResult(ctx context.Context, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, "xdotool", args...)
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("xdotool failed: %w (is xdotool installed?)", err)
	}
	result := fmt.Sprintf("OK: xdotool %s", strings.Join(args, " "))
	if len(out) > 0 {
		result += "\n" + strings.TrimSpace(string(out))
	}
	return Result{Content: result}, nil
}

func wmctrlResult(ctx context.Context, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, "wmctrl", args...)
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("wmctrl failed: %w (is wmctrl installed?)", err)
	}
	return Result{Content: strings.TrimSpace(string(out))}, nil
}

func runAppResult(ctx context.Context, app string) (Result, error) {
	cmd := exec.CommandContext(ctx, app)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("failed to launch %s: %w", app, err)
	}
	return Result{Content: fmt.Sprintf("OK: launched %s (PID %d)", app, cmd.Process.Pid)}, nil
}
