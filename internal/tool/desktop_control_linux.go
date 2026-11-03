//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	linuxBackendOnce     sync.Once
	linuxBackendWayland  bool
	linuxBackendResolved bool
)

func isWaylandSession() bool {
	linuxBackendOnce.Do(func() {

		linuxBackendWayland = os.Getenv("WAYLAND_DISPLAY") != ""
		linuxBackendResolved = true
	})
	return linuxBackendWayland
}

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

	case "click":

		code, err := x11ButtonCode(p.Button)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), "click", code)
	case "double_click":
		code, err := x11ButtonCode(p.Button)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), "click", "--repeat", "2", code)
	case "triple_click":
		code, err := x11ButtonCode(p.Button)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), "click", "--repeat", "3", code)
	case "right_click":
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), "click", "3")
	case "middle_click":
		return xdotoolResult(ctx, "mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y), "click", "2")
	case "mouse_down", "mouse_up":

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

		return xdotoolResult(ctx, x11DragArgs(p.X, p.Y, p.ToX, p.ToY)...)
	case "scroll":
		btn := "4"
		if p.Direction == "down" {
			btn = "5"
		}
		args := []string{"click", "--repeat", fmt.Sprintf("%d", p.Amount), btn}
		return xdotoolResult(ctx, args...)
	case "modifier_click":

		mods, err := normalizeModifiers(p.Text)
		if err != nil {
			return Result{}, err
		}

		args := []string{"mousemove", "--sync", fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y)}
		for _, m := range mods {
			xd, ok := xdModifierName(m)
			if !ok {
				return Result{}, fmt.Errorf("modifier %q has no xdotool mapping (X11); supported: cmd, ctrl, alt, shift", m)
			}
			args = append(args, "keydown", xd)
		}

		clickBtn := "1"
		switch p.Button {
		case "right":
			clickBtn = "3"
		case "middle":
			clickBtn = "2"
		case "", "left":
		default:
			return Result{}, fmt.Errorf("unsupported button %q (supported: left, right, middle)", p.Button)
		}
		args = append(args, "click", clickBtn)
		for i := len(mods) - 1; i >= 0; i-- {
			xd, _ := xdModifierName(mods[i])
			args = append(args, "keyup", xd)
		}
		return xdotoolResult(ctx, args...)
	case "mouse_position":
		return xdotoolResult(ctx, "getmouselocation")

	case "type":
		return xdotoolResult(ctx, "type", p.Text)
	case "key_press", "key_combo":

		return xdotoolResult(ctx, "key", p.Text)
	case "hold_key":
		return holdKeyX11(ctx, p)

	case "set_window_bounds":
		if p.ToX <= 0 || p.ToY <= 0 {
			return Result{}, fmt.Errorf("set_window_bounds requires positive to_x (width) and to_y (height)")
		}

		base := []string{"getactivewindow", "windowmove", "%1"}
		if strings.TrimSpace(p.Text) != "" {

			base = []string{"search", "--name", xdLiteralPattern(p.Text), "windowmove", "%1"}
		}
		args := append(base, fmt.Sprintf("%d", p.X), fmt.Sprintf("%d", p.Y),
			"windowsize", "%1", fmt.Sprintf("%d", p.ToX), fmt.Sprintf("%d", p.ToY))
		return xdotoolResult(ctx, args...)
	case "list_windows":

		return xdotoolResult(ctx, "search", "--onlyvisible", "--name", ".")
	case "focus_window", "close_window", "minimize_window":

		if strings.TrimSpace(p.Text) == "" {
			return Result{}, fmt.Errorf("%s requires 'text' (window title); refusing to run: empty pattern matches ALL windows", p.Action)
		}
		verb := "windowactivate"
		if p.Action == "close_window" {
			verb = "windowclose"
		} else if p.Action == "minimize_window" {
			verb = "windowminimize"
		}

		return xdotoolResult(ctx, "search", "--name", xdLiteralPattern(p.Text), verb, "%@")
	case "maximize_window":

		if strings.TrimSpace(p.Text) != "" {
			return xdotoolResult(ctx, "search", "--name", xdLiteralPattern(p.Text), "windowstate", "--add", "--maximized", "%@")
		}
		return xdotoolResult(ctx, "getactivewindow", "windowstate", "--add", "--maximized")

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

		if strings.TrimSpace(p.Text) == "" {
			return Result{}, fmt.Errorf("quit_app requires 'text' (app name); refusing to run: empty pattern matches ALL windows")
		}
		return xdotoolResult(ctx, "search", "--class", xdLiteralPattern(p.Text), "windowclose", "%@")
	case "list_apps":
		return wmctrlResult(ctx, "-l")
	case "active_app":
		return xdotoolResult(ctx, "getactivewindow", "getwindowname")

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

const atspiScript = `#!/usr/bin/env python3
import json, sys
import gi
gi.require_version('Atspi', '2.0')
from gi.repository import Atspi

max_depth = int(sys.argv[1]) if len(sys.argv) > 1 else 8

def safe_child_count(node):
    # #3130: a dying app's DBus objects vanish under us - every accessor
    # on a defunct node raises GLib.Error. Count failures mean "no
    # children to walk", not "abort the whole probe".
    try:
        return node.get_child_count()
    except Exception:
        return 0

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
    for i in range(safe_child_count(node)):
        try:
            child = node.get_child_at_index(i)
        except Exception:
            continue
        walk(child, depth + 1)

desk = Atspi.get_desktop(0)
active = None
for a in range(safe_child_count(desk)):
    try:
        app = desk.get_child_at_index(a)
    except Exception:
        continue
    for w in range(safe_child_count(app)):
        try:
            win = app.get_child_at_index(w)
            st = win.get_state_set()
            if st.contains(Atspi.StateType.ACTIVE):
                active = win
                break
        except Exception:
            continue
    if active:
        break
if active is None:
    sys.stderr.write("no active window found via AT-SPI")
    sys.exit(1)
walk(active, 0)
`

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

			if p.Action != "wait_and_click" || ctx.Err() != nil {
				return Result{}, err
			}
			if time.Now().After(deadline) {
				return Result{}, fmt.Errorf("no element matching %q reachable before timeout (last probe error: %v)", p.Text, err)
			}
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
			continue
		}
		matches := atspiFind(els, p.Text)
		if len(matches) > 0 {
			first := matches[0]
			cx, cy := atspiCenter(first)
			if p.Action == "find_element" {
				return Result{Content: fmt.Sprintf("found %d matches:\n%s", len(matches), atspiFormat(matches))}, nil
			}

			var clickErr error
			if isWaylandSession() {

				clickCmds, cerr := ydoClickArgs(p.Button, 1)
				if cerr != nil {
					clickErr = cerr
				} else {
					cmds := append([][]string{ydoMoveArgs(cx, cy)}, clickCmds...)
					clickErr = runArgvSeq(ctx, cmds)
				}
			} else {
				code, cerr := x11ButtonCode(p.Button)
				if cerr != nil {
					return Result{IsError: true, Content: cerr.Error()}, nil
				}
				_, clickErr = exec.CommandContext(ctx, "xdotool", "mousemove", "--sync",
					fmt.Sprintf("%d", cx), fmt.Sprintf("%d", cy), "click", code).Output()
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

func ydoModifierClick(ctx context.Context, x, y int, modifierSpec, button string) (Result, error) {
	mods, err := normalizeModifiers(modifierSpec)
	if err != nil {
		return Result{}, err
	}

	clickCode, err := ydoClickCode(button)
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
	cmds = append(cmds, []string{"ydotool", "click", clickCode})
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
		clickCmds, cerr := ydoClickArgs(button, clicks)
		if cerr != nil {
			return Result{IsError: true, Content: cerr.Error()}, nil
		}
		cmds = append(cmds, clickCmds...)
		if err := runArgvSeq(ctx, cmds); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil
	case "modifier_click":
		return ydoModifierClick(ctx, p.X, p.Y, p.Text, p.Button)
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

		button, err := ydoButtonMask(p.Button)
		if err != nil {
			return Result{}, err
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

		all = append(all, []string{"sleep", fmt.Sprintf("%.3f", float64(duration)/1000)})
		all = append(all, releaseCmds...)
		if err := runArgvSeq(ctx, all); err != nil {

			rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer rcancel()
			_ = runArgvSeq(rctx, releaseCmds)
			return Result{}, err
		}
		return Result{Content: "OK"}, nil

	case "snapshot_ui":
		return atspiSnapshot(ctx, p.MaxDepth)
	case "find_element", "find_and_click", "wait_and_click":
		return atspiLocate(ctx, p)

	case "mouse_position", "list_windows", "focus_window", "close_window", "minimize_window",
		"maximize_window", "set_window_bounds", "quit_app", "list_apps", "active_app",
		"display_info", "menu_select":
		return Result{}, fmt.Errorf("desktop_control: action %q is not supported on Wayland (no protocol for external clients; run the target under XWayland for X11 tooling)", p.Action)

	case "key_press", "key_combo":
		return Result{}, fmt.Errorf("desktop_control: action %q is not yet supported on Wayland (only modifier-based actions are wired to ydotool); run the target under XWayland for X11 key injection", p.Action)

	default:

		break
	}

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

func x11ButtonCode(button string) (string, error) {
	button = strings.ToLower(strings.TrimSpace(button))
	if button == "" {
		button = "left"
	}
	switch button {
	case "left":
		return "1", nil
	case "middle":
		return "2", nil
	case "right":
		return "3", nil
	default:
		return "", fmt.Errorf("unknown button %q (left, right, middle)", button)
	}
}

func x11DragArgs(x, y, toX, toY int) []string {
	return []string{
		"mousemove", "--sync", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y),
		"mousedown", "1",
		"mousemove", "--sync", fmt.Sprintf("%d", toX), fmt.Sprintf("%d", toY),
		"mouseup", "1",
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

	_ = ctx
	cmd := exec.Command(name, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("failed to launch %s: %w", name, err)
	}

	go func() { _ = cmd.Wait() }()
	return Result{Content: fmt.Sprintf("OK: launched %s %s (PID %d)", name, strings.Join(args, " "), cmd.Process.Pid)}, nil
}

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

		rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer rcancel()
		_, _ = exec.CommandContext(rctx, "xdotool", "keyup", p.Text).Output()
		return Result{}, ctx.Err()
	case <-time.After(time.Duration(duration) * time.Millisecond):
	}
	return xdotoolResult(ctx, "keyup", p.Text)
}
