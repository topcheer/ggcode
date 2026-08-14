//go:build linux

package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// linuxDisplayBackend selects the automation backend at runtime.
// Wayland sessions (WAYLAND_DISPLAY set) need ydotool (uinput); X11 keeps
// xdotool. Cached after first resolution since session type cannot change
// within a process lifetime.
var (
	linuxBackendWayland  bool
	linuxBackendResolved bool
)

func isWaylandSession() bool {
	if !linuxBackendResolved {
		// XDG_SESSION_TYPE=wayland without WAYLAND_DISPLAY still has no
		// wayland socket to talk over; require the socket variable.
		linuxBackendWayland = os.Getenv("WAYLAND_DISPLAY") != ""
		linuxBackendResolved = true
	}
	return linuxBackendWayland
}

// runArgvSeq executes a sequence of full argv commands, stopping at the
// first failure.
func runArgvSeq(ctx context.Context, cmds [][]string) error {
	for _, argv := range cmds {
		if len(argv) == 0 {
			continue
		}
		if out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s failed: %w\n%s (is %s installed and the ydotoold daemon running?)",
				argv[0], err, string(out), argv[0])
		}
	}
	return nil
}

func executeDesktopControl(ctx context.Context, p desktopParams) (Result, error) {
	if isWaylandSession() {
		return executeDesktopControlWayland(ctx, p)
	}
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
	case "middle_click":
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), "click", "2")
	case "mouse_down", "mouse_up":
		// xdotool button codes: 1=left, 2=middle, 3=right.
		btn := "1"
		if p.Button == "right" {
			btn = "3"
		} else if p.Button == "middle" {
			btn = "2"
		}
		sub := "mousedown"
		if p.Action == "mouse_up" {
			sub = "mouseup"
		}
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), sub, btn)
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
	case "hold_key":
		return holdKeyX11(ctx, p)

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

// executeDesktopControlWayland implements the ydotool-backed subset for
// Wayland sessions. Mouse move/click/drag, typing, and key combos work;
// scroll has no ydotool equivalent (REL_WHEEL is not a BTN event, so the
// click subcommand cannot send it) and window management has no protocol
// for external clients — both report clear messages instead of failing
// cryptically.
func executeDesktopControlWayland(ctx context.Context, p desktopParams) (Result, error) {
	switch p.Action {
	case "move":
		if err := runArgvSeq(ctx, [][]string{ydoMoveArgs(p.X, p.Y)}); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil
	case "click", "double_click", "triple_click", "right_click", "middle_click":
		clicks := 1
		switch p.Action {
		case "double_click":
			clicks = 2
		case "triple_click":
			clicks = 3
		}
		button := p.Button
		if p.Action == "right_click" {
			button = "right"
		} else if p.Action == "middle_click" {
			button = "middle"
		}
		cmds := [][]string{ydoMoveArgs(p.X, p.Y)}
		cmds = append(cmds, ydoClickArgs(button, clicks)...)
		if err := runArgvSeq(ctx, cmds); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil
	case "modifier_click":
		mods, err := normalizeModifiers(p.Text)
		if err != nil {
			return Result{}, err
		}
		cmds := [][]string{ydoMoveArgs(p.X, p.Y)}
		for _, m := range mods {
			press, _, ok := ydoModifierKeyArgs(m)
			if !ok {
				return Result{}, fmt.Errorf("modifier %q has no ydotool mapping", m)
			}
			cmds = append(cmds, press)
		}
		cmds = append(cmds, []string{"ydotool", "click", "0xC0"})
		for i := len(mods) - 1; i >= 0; i-- {
			_, release, _ := ydoModifierKeyArgs(mods[i])
			cmds = append(cmds, release)
		}
		if err := runArgvSeq(ctx, cmds); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil
	case "drag":
		if err := runArgvSeq(ctx, ydoDragArgs(p.X, p.Y, p.ToX, p.ToY)); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil
	case "type":
		if err := runArgvSeq(ctx, [][]string{ydoTypeArgs(p.Text)}); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil

	case "scroll":
		return Result{}, fmt.Errorf("desktop_control: scroll is not supported on Wayland via ydotool (no REL_WHEEL event in the click API)")

	case "mouse_down", "mouse_up":
		code := "0xC0" // BTN_LEFT
		if p.Button == "right" {
			code = "0xC1"
		} else if p.Button == "middle" {
			code = "0xC2"
		}
		flag := "-d"
		if p.Action == "mouse_up" {
			flag = "-u"
		}
		cmds := [][]string{ydoMoveArgs(p.X, p.Y), {"ydotool", "click", flag, code}}
		if err := runArgvSeq(ctx, cmds); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil

	case "hold_key":
		duration, err := holdKeyDurationClamp(p.Duration)
		if err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(p.Text) == "" {
			return Result{}, fmt.Errorf("hold_key requires 'text' (key or combo)")
		}
		// Build press list from modifier tokens; sleeps go between press and
		// release. Non-modifier keys have no evdev mapping here — rejected
		// explicitly (see ydoModifierKeyArgs).
		var pressCmds [][]string
		var releaseCmds [][]string
		for _, part := range strings.Split(p.Text, "+") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "" {
				continue
			}
			press, release, ok := ydoModifierKeyArgs(part)
			if !ok {
				return Result{}, fmt.Errorf("hold_key on Wayland supports modifier combos only (no evdev mapping for %q)", part)
			}
			pressCmds = append(pressCmds, press)
			releaseCmds = append(releaseCmds, release)
		}
		if len(pressCmds) == 0 {
			return Result{}, fmt.Errorf("hold_key requires at least one key")
		}
		all := append([][]string{}, pressCmds...)
		all = append(all, []string{"sleep", fmt.Sprintf("%d", duration/1000)}) // coarse seconds; ydotool has no sub-second sleep
		all = append(all, releaseCmds...)
		if err := runArgvSeq(ctx, all); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil

	// Window management has no Wayland protocol for external clients, and
	// ydotool exposes no position read or accessibility tree.
	case "mouse_position", "list_windows", "focus_window", "close_window", "minimize_window",
		"maximize_window", "set_window_bounds", "quit_app", "list_apps", "active_app",
		"snapshot_ui", "find_element", "find_and_click", "wait_and_click", "display_info", "menu_select":
		return Result{}, fmt.Errorf("desktop_control: action %q is not supported on Wayland (no protocol for external clients; run the target under XWayland for X11 tooling)", p.Action)

	default:
		// App launching is display-server independent — delegate to the
		// shared path below.
		break
	}
	// Shared, display-server-independent actions.
	switch p.Action {
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

// holdKeyX11 implements hold_key via xdotool keydown/sleep/keyup with
// context-cancellation-safe release.
func holdKeyX11(ctx context.Context, p desktopParams) (Result, error) {
	duration, err := holdKeyDurationClamp(p.Duration)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(p.Text) == "" {
		return Result{}, fmt.Errorf("hold_key requires 'text' (key or combo)")
	}
	if _, err := exec.CommandContext(ctx, "xdotool", "keydown", p.Text).Output(); err != nil {
		return Result{}, fmt.Errorf("xdotool keydown failed: %w", err)
	}
	select {
	case <-ctx.Done():
		// Always attempt release so a cancelled context doesn't leave the
		// key stuck down.
		_, _ = exec.CommandContext(context.Background(), "xdotool", "keyup", p.Text).Output()
		return Result{}, ctx.Err()
	case <-time.After(time.Duration(duration) * time.Millisecond):
	}
	return xdotoolResult(ctx, "keyup", p.Text)
}
