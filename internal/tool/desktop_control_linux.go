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
		// Move to coordinates first, then click — matching macOS behavior.
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), "click", "1")
	case "double_click":
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), "click", "--repeat", "2", "1")
	case "triple_click":
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), "click", "--repeat", "3", "1")
	case "right_click":
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), "click", "3")
	case "move":
		return xdotoolResult(ctx, "mousemove", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y))
	case "drag":
		// Move to start position, mouse down, move to target, mouse up
		_, _ = exec.CommandContext(ctx, "xdotool", "mousemove", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y)).Output()
		_, _ = exec.CommandContext(ctx, "xdotool", "mousedown", "1").Output()
		_, _ = exec.CommandContext(ctx, "xdotool", "mousemove", fmt.Sprintf("%d", p.ToX), fmt.Sprintf("%d", p.ToY)).Output()
		return xdotoolResult(ctx, "mouseup", "1")
	case "scroll":
		btn := "4" // up
		if p.Direction == "down" {
			btn = "5"
		}
		args := []string{"click", "--repeat", fmt.Sprintf("%d", p.Amount), btn}
		return xdotoolResult(ctx, args...)
	case "modifier_click":
		// Validate and normalize the modifier spec first so typos fail
		// before anything is clicked.
		mods, err := normalizeModifiers(p.Text)
		if err != nil {
			return Result{}, err
		}
		// xdotool supports "ctrl+shift+1" style click args.
		clickSpec := strings.Join(mods, "+") + "+1"
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), "click", clickSpec)
	case "mouse_position":
		return xdotoolResult(ctx, "getmouselocation")

	// ── Keyboard ──
	case "type":
		return xdotoolResult(ctx, "type", p.Text)
	case "key_press", "key_combo":
		// xdotool uses + for combos: ctrl+c, alt+Tab
		return xdotoolResult(ctx, "key", p.Text)

	// ── Window management ──
	case "set_window_bounds":
		if p.ToX <= 0 || p.ToY <= 0 {
			return Result{}, fmt.Errorf("set_window_bounds requires positive to_x (width) and to_y (height)")
		}
		// Target the named window when given; otherwise the active window.
		base := []string{"getactivewindow"}
		if strings.TrimSpace(p.Text) != "" {
			base = []string{"search", "--name", p.Text}
		}
		args := append(base, "windowmove", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y),
			"windowsize", fmt.Sprintf("%d", p.ToX), fmt.Sprintf("%d", p.ToY))
		return xdotoolResult(ctx, args...)
	case "list_windows":
		// "." matches any non-empty window name; an empty pattern is an error
		// in some xdotool versions.
		return xdotoolResult(ctx, "search", "--onlyvisible", "--name", ".")
	case "focus_window":
		return xdotoolResult(ctx, "search", "--name", p.Text, "windowactivate", "%@")
	case "close_window":
		return xdotoolResult(ctx, "search", "--name", p.Text, "windowclose", "%@")
	case "minimize_window":
		return xdotoolResult(ctx, "search", "--name", p.Text, "windowminimize", "%@")
	case "maximize_window":
		// Use the target window name when provided; fall back to the active
		// window so we never maximize every visible window (empty pattern
		// matches all).
		if strings.TrimSpace(p.Text) != "" {
			return xdotoolResult(ctx, "search", "--name", p.Text, "windowstate", "--toggle", "--maximized", "%@")
		}
		return xdotoolResult(ctx, "getactivewindow", "windowstate", "--toggle", "--maximized")

	// ── Application ──
	case "open":
		target := strings.TrimSpace(p.Text)
		if target == "" {
			return Result{}, fmt.Errorf("open requires 'text' (URL or file path)")
		}
		if app := strings.TrimSpace(p.App); app != "" {
			return runAppResult(ctx, app+" "+target)
		}
		return runAppResult(ctx, "xdg-open "+target)
	case "launch_app":
		return runAppResult(ctx, p.Text)
	case "quit_app":
		return xdotoolResult(ctx, "search", "--class", p.Text, "windowclose", "%@")
	case "list_apps":
		return wmctrlResult(ctx, "-l")
	case "active_app":
		return xdotoolResult(ctx, "getactivewindow", "getwindowname")

	// UI-tree and menu actions require a platform accessibility API;
	// xdotool has none. Report a clear platform message instead of
	// "unknown action" so the agent does not retry with different
	// parameters.
	case "snapshot_ui", "find_element", "find_and_click", "wait_and_click", "display_info", "menu_select":
		return Result{}, fmt.Errorf("desktop_control: action %q is not supported on Linux (requires a platform accessibility API; xdotool has none)", p.Action)

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
