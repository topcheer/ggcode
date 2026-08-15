//go:build linux

package tool

import (
	"context"
	"encoding/json"
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
		// xdotool click takes no modifier syntax (atoi(argv[0]) only —
		// "ctrl+1" parses as button 0). Chain keydown/click/keyup in a
		// single xdotool invocation instead (#216). fn has no X mapping;
		// error explicitly like the Wayland path does.
		args := []string{"mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y)}
		for _, m := range mods {
			xd, ok := xdModifierName(m)
			if !ok {
				return Result{}, fmt.Errorf("modifier %q has no xdotool mapping (X11); supported: cmd, ctrl, alt, shift", m)
			}
			args = append(args, "keydown", xd)
		}
		args = append(args, "click", "1")
		for i := len(mods) - 1; i >= 0; i-- {
			xd, _ := xdModifierName(mods[i])
			args = append(args, "keyup", xd)
		}
		return xdotoolResult(ctx, args...)
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
			return runAppResult(ctx, app, target)
		}
		return runAppResult(ctx, "xdg-open", target)
	case "launch_app":
		fields := strings.Fields(p.Text)
		if len(fields) == 0 {
			return Result{}, fmt.Errorf("launch_app requires 'text' (command to launch)")
		}
		return runAppResult(ctx, fields[0], fields[1:]...)
	case "quit_app":
		return xdotoolResult(ctx, "search", "--class", p.Text, "windowclose", "%@")
	case "list_apps":
		return wmctrlResult(ctx, "-l")
	case "active_app":
		return xdotoolResult(ctx, "getactivewindow", "getwindowname")

	// UI-tree actions use AT-SPI (the Linux desktop accessibility API) via
	// python3-gi — works on both X11 and Wayland. display_info has no
	// xdotool equivalent; menu_select needs a menu-bar accessibility walk
	// (deferred).
	case "snapshot_ui":
		return atspiSnapshot(ctx, p.MaxDepth)
	case "find_element", "find_and_click", "wait_and_click":
		return atspiLocate(ctx, p)
	case "display_info":
		return Result{}, fmt.Errorf("desktop_control: action %q is not supported on Linux X11 (no xdotool equivalent; try Wayland compositors' wlr-output-management or the XRandR CLI)", p.Action)
	case "menu_select":
		return Result{}, fmt.Errorf("desktop_control: action %q is not supported on Linux yet (menu-bar accessibility walk pending)", p.Action)

	default:
		return Result{}, fmt.Errorf("unknown action: %s", p.Action)
	}
}

// atspiScript is the python3-gi AT-SPI probe: dumps the flattened
// accessibility tree of the currently focused application as JSON lines.
// Requires gir1.2-atspi-2.0 (preinstalled on GNOME/KDE systems). Shared by
// the X11 and Wayland backends.
const atspiScript = `#!/usr/bin/env python3
import json, sys
import gi
gi.require_version('Atspi', '2.0')
from gi.repository import Atspi

max_depth = int(sys.argv[1]) if len(sys.argv) > 1 else 8

def walk(node, depth):
    if node is None or depth > max_depth:
        return
    try:
        ext = node.get_extents(Atspi.CoordType.SCREEN)
        if ext and (ext.width > 0 or ext.height > 0 or node.get_name()):
            print(json.dumps({
                "role": node.get_role_name(),
                "name": node.get_name() or "",
                "x": ext.x, "y": ext.y, "w": ext.width, "h": ext.height,
            }), flush=True)
    except Exception:
        pass
    for i in range(node.get_child_count()):
        walk(node.get_child_at_index(i), depth + 1)

desk = Atspi.get_desktop(0)
active = None
for a in range(desk.get_child_count()):
    app = desk.get_child_at_index(a)
    for w in range(app.get_child_count()):
        win = app.get_child_at_index(w)
        st = win.get_state_set()
        if st.contains(Atspi.StateType.ACTIVE):
            active = win
            break
    if active:
        break
if active is None:
    sys.stderr.write("no active window found via AT-SPI")
    sys.exit(1)
walk(active, 0)
`

// atspiRun executes the probe and returns the flattened elements.
func atspiRun(ctx context.Context, maxDepth int) ([]ATSPIElement, error) {
	if maxDepth <= 0 {
		maxDepth = 8
	}
	tmp, err := os.CreateTemp("", "ggcode-atspi-*.py")
	if err != nil {
		return nil, fmt.Errorf("temp script: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(atspiScript); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("write script: %w", err)
	}
	tmp.Close()

	out, err := exec.CommandContext(ctx, "python3", tmp.Name(), fmt.Sprintf("%d", maxDepth)).Output()
	if err != nil {
		return nil, fmt.Errorf("AT-SPI probe failed: %w (requires python3-gi with gir1.2-atspi-2.0; on headless Xvfb there is no a11y bridge)", err)
	}
	var els []ATSPIElement
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var e ATSPIElement
		if json.Unmarshal([]byte(line), &e) == nil && (e.Role != "" || e.Name != "") {
			els = append(els, e)
		}
	}
	return els, nil
}

func atspiSnapshot(ctx context.Context, maxDepth int) (Result, error) {
	els, err := atspiRun(ctx, maxDepth)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: atspiFormat(els)}, nil
}

// atspiLocate implements find_element / find_and_click / wait_and_click on
// top of the shared snapshot+match helpers.
func atspiLocate(ctx context.Context, p desktopParams) (Result, error) {
	deadline := time.Now().Add(500 * time.Millisecond)
	if p.Action == "wait_and_click" {
		d := time.Duration(p.TimeoutMs) * time.Millisecond
		if d <= 0 {
			d = 5 * time.Second
		}
		deadline = time.Now().Add(d)
	}
	for {
		els, err := atspiRun(ctx, p.MaxDepth)
		if err != nil {
			return Result{}, err
		}
		matches := atspiFind(els, p.Text)
		if len(matches) > 0 {
			first := matches[0]
			cx, cy := atspiCenter(first)
			if p.Action == "find_element" {
				return Result{Content: fmt.Sprintf("found %d matches:\n%s", len(matches), atspiFormat(matches))}, nil
			}
			// Click via the session's own backend: X11 xdotool, Wayland ydotool.
			var clickErr error
			if isWaylandSession() {
				cmds := [][]string{ydoMoveArgs(cx, cy), {"ydotool", "click", "0xC0"}}
				clickErr = runArgvSeq(ctx, cmds)
			} else {
				_, clickErr = exec.CommandContext(ctx, "xdotool", "mousemove", "--sync",
					fmt.Sprintf("%d", cx), fmt.Sprintf("%d", cy), "click", "1").Output()
			}
			if clickErr != nil {
				return Result{}, fmt.Errorf("found element at (%d,%d) but click failed: %w", cx, cy, clickErr)
			}
			return Result{Content: fmt.Sprintf("OK: clicked %q %s at (%d,%d)", first.Name, first.Role, cx, cy)}, nil
		}
		if p.Action != "wait_and_click" || time.Now().After(deadline) {
			return Result{}, fmt.Errorf("no element matching %q found%s", p.Text, retrySuffix(p.Action))
		}
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func retrySuffix(action string) string {
	if action == "wait_and_click" {
		return " before timeout"
	}
	return ""
}

// executeDesktopControlWayland implements the ydotool-backed subset for
// Wayland sessions. Mouse move/click/drag, typing, and key combos work;
// scroll has no ydotool equivalent (REL_WHEEL is not a BTN event, so the
// click subcommand cannot send it) and window management has no protocol
// for external clients — both report clear messages instead of failing
// cryptically.
// ydoModifierClick holds normalized modifiers while clicking via ydotool.
func ydoModifierClick(ctx context.Context, x, y int, modifierSpec string) (Result, error) {
	mods, err := normalizeModifiers(modifierSpec)
	if err != nil {
		return Result{}, err
	}
	cmds := [][]string{ydoMoveArgs(x, y)}
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
}

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
		return ydoModifierClick(ctx, p.X, p.Y, p.Text)
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
		// ydotool click bitmask: low bits = button (0 left, 1 right, 2
		// middle); 0x40 = down-only, 0x80 = up-only. The old -d/-u flags do
		// not exist upstream and 0xC0 always performs a full click (#191).
		button := 0x0
		if p.Button == "right" {
			button = 0x1
		} else if p.Button == "middle" {
			button = 0x2
		}
		mode := 0x40
		if p.Action == "mouse_up" {
			mode = 0x80
		}
		cmds := [][]string{ydoMoveArgs(p.X, p.Y), {"ydotool", "click", fmt.Sprintf("0x%X", button|mode)}}
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
		// coreutils sleep accepts fractional seconds — integer division
		// turned 500ms into an instant tap (#192).
		all = append(all, []string{"sleep", fmt.Sprintf("%.3f", float64(duration)/1000)})
		all = append(all, releaseCmds...)
		if err := runArgvSeq(ctx, all); err != nil {
			// runArgvSeq stops at the first failure (ctx cancel kills the
			// sleep); re-issue the release sequence on a background context
			// so modifiers do not stay pressed at the compositor level. Mirrors
			// holdKeyX11's ctx.Done() protection (#192).
			_ = runArgvSeq(context.Background(), releaseCmds)
			return Result{}, err
		}
		return Result{Content: "OK"}, nil

	// UI-tree actions work on Wayland too: AT-SPI is display-server
	// independent, and atspiLocate clicks via ydotool on this backend.
	case "snapshot_ui":
		return atspiSnapshot(ctx, p.MaxDepth)
	case "find_element", "find_and_click", "wait_and_click":
		return atspiLocate(ctx, p)

	// Window management has no Wayland protocol for external clients, and
	// ydotool exposes no position read.
	case "mouse_position", "list_windows", "focus_window", "close_window", "minimize_window",
		"maximize_window", "set_window_bounds", "quit_app", "list_apps", "active_app",
		"display_info", "menu_select":
		return Result{}, fmt.Errorf("desktop_control: action %q is not supported on Wayland (no protocol for external clients; run the target under XWayland for X11 tooling)", p.Action)

	// key_press/key_combo are supported on the X11 backend but not wired
	// through the ydotool key path here. Report the platform limitation
	// explicitly instead of falling through to a misleading "unknown
	// action" that makes the agent retry with different action names (#193).
	case "key_press", "key_combo":
		return Result{}, fmt.Errorf("desktop_control: action %q is not yet supported on Wayland (only modifier-based actions are wired to ydotool); run the target under XWayland for X11 key injection", p.Action)

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
			return runAppResult(ctx, app, target)
		}
		return runAppResult(ctx, "xdg-open", target)
	case "launch_app":
		fields := strings.Fields(p.Text)
		if len(fields) == 0 {
			return Result{}, fmt.Errorf("launch_app requires 'text' (command to launch)")
		}
		return runAppResult(ctx, fields[0], fields[1:]...)
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

func runAppResult(ctx context.Context, name string, args ...string) (Result, error) {
	// exec does not go through a shell: name must be the bare executable
	// and every argument a separate argv entry. Concatenating them into one
	// string makes exec look for a file literally named "xdg-open <url>".
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("failed to launch %s: %w", name, err)
	}
	// Reap the child in the background so repeated launches don't
	// accumulate zombies.
	go func() { _ = cmd.Wait() }()
	return Result{Content: fmt.Sprintf("OK: launched %s %s (PID %d)", name, strings.Join(args, " "), cmd.Process.Pid)}, nil
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
