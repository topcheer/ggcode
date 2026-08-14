//go:build windows

package tool

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Windows implementation via the SendInput API (core subset: mouse,
// keyboard, application launch). Window-management and UI-tree actions
// require UI Automation / Win32 APIs and are reported as unsupported for
// now so the agent gets a clear message instead of "unknown action".

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	pSendInput                = user32.NewProc("SendInput")
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pEnumWindows              = user32.NewProc("EnumWindows")
	pGetWindowTextW           = user32.NewProc("GetWindowTextW")
	pGetClassNameW            = user32.NewProc("GetClassNameW")
	pIsWindowVisible          = user32.NewProc("IsWindowVisible")
	pGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pShowWindow               = user32.NewProc("ShowWindow")
	pIsIconic                 = user32.NewProc("IsIconic")
	pGetWindowRect            = user32.NewProc("GetWindowRect")
	pMoveWindow               = user32.NewProc("MoveWindow")
)

const (
	wmClose    = 0x0010
	swMinimize = 6
	swMaximize = 3
	swRestore  = 9
)

// Virtual key codes for the modifiers and special keys we map.
var winVK = map[string]uint16{
	"ctrl":      0x11, // VK_CONTROL
	"control":   0x11,
	"alt":       0x12, // VK_MENU
	"option":    0x12,
	"opt":       0x12,
	"shift":     0x10, // VK_SHIFT
	"cmd":       0x5B, // VK_LWIN (Windows key is the cmd analogue)
	"command":   0x5B,
	"meta":      0x5B,
	"super":     0x5B,
	"win":       0x5B,
	"fn":        0x5D, // VK_APPS (no true fn key; closest analogue)
	"enter":     0x0D, // VK_RETURN
	"return":    0x0D,
	"tab":       0x09, // VK_TAB
	"esc":       0x1B, // VK_ESCAPE
	"escape":    0x1B,
	"escape2":   0x1B,
	"backspace": 0x08, // VK_BACK
	"delete":    0x2E, // VK_DELETE
	"up":        0x26, // VK_UP
	"down":      0x28, // VK_DOWN
	"left":      0x25, // VK_LEFT
	"right":     0x27, // VK_RIGHT
	"space":     0x20, // VK_SPACE
	"home":      0x24, // VK_HOME
	"end":       0x23, // VK_END
	"pageup":    0x21, // VK_PRIOR
	"pagedown":  0x22, // VK_NEXT
}

// INPUT struct layout for SendInput (winuser.h). The union must be the
// size of the largest member; MOUSEINPUT is the largest at 28 bytes on
// x64 (padding included via uint32 fields).
type winINPUT struct {
	Type uint32
	// Union: KEYBDINPUT or MOUSEINPUT. We use a byte array sized to the
	// largest member and fill it per type.
	U [28]byte
}

type winMOUSEINPUT struct {
	Type      uint32
	Dx, Dy    int32
	MouseData uint32
	DwFlags   uint32
	Time      uint32
	ExtraInfo uintptr
}

type winKEYBDINPUT struct {
	Type      uint32
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove        = 0x0001 // MOUSEEVENTF_MOVE (relative)
	mouseAbsolute    = 0x8000 // MOUSEEVENTF_ABSOLUTE
	mouseVirtualDesk = 0x4000 // MOUSEEVENTF_VIRTUALDESK
	mouseLeftDown    = 0x0002
	mouseLeftUp      = 0x0004
	mouseRightDown   = 0x0008
	mouseRightUp     = 0x0010
	mouseMiddleDown  = 0x0020 // MOUSEEVENTF_MIDDLEDOWN
	mouseMiddleUp    = 0x0040 // MOUSEEVENTF_MIDDLEUP
	mouseWheel       = 0x0800

	keyUp = 0x0002 // KEYEVENTF_KEYUP
)

func sendInput(inputs []winINPUT) error {
	if len(inputs) == 0 {
		return nil
	}
	n, _, _ := pSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(winINPUT{}),
	)
	if n != uintptr(len(inputs)) {
		return fmt.Errorf("SendInput delivered %d of %d events", n, len(inputs))
	}
	return nil
}

func mouseEventInput(flags uint32, dx, dy int32) winINPUT {
	mi := winMOUSEINPUT{
		Type:    inputMouse,
		Dx:      dx,
		Dy:      dy,
		DwFlags: flags,
	}
	var in winINPUT
	in.Type = inputMouse
	copy(in.U[:], (*[28]byte)(unsafe.Pointer(&mi))[:])
	return in
}

func keyEventInput(vk uint16, flags uint32) winINPUT {
	ki := winKEYBDINPUT{
		Type:  inputKeyboard,
		Vk:    vk,
		Flags: flags,
	}
	var in winINPUT
	in.Type = inputKeyboard
	copy(in.U[:], (*[28]byte)(unsafe.Pointer(&ki))[:])
	return in
}

// absCoord converts a logical pixel coordinate to the 0..65535 normalized
// range SendInput expects with MOUSEEVENTF_ABSOLUTE|VIRTUALDESK.
func absCoord(v int32, total int32) int32 {
	if total <= 0 {
		return 0
	}
	return int32((int64(v) * 65535) / int64(total))
}

func screenSpan() (w, h int32) {
	// GetSystemMetrics(SM_CXVIRTUALSCREEN=76, SM_CYVIRTUALSCREEN=79)
	p := user32.NewProc("GetSystemMetrics")
	cx, _, _ := p.Call(76)
	cy, _, _ := p.Call(79)
	return int32(cx), int32(cy)
}

func executeDesktopControl(ctx context.Context, p desktopParams) (Result, error) {
	switch p.Action {
	// ── Mouse ──
	case "click", "double_click", "triple_click":
		count := 1
		if p.Action == "double_click" {
			count = 2
		} else if p.Action == "triple_click" {
			count = 3
		}
		return winClick(ctx, p.X, p.Y, p.Button, count)
	case "middle_click":
		return winClick(ctx, p.X, p.Y, "middle", 1)
	case "mouse_down", "mouse_up":
		return winMouseButtonEvent(ctx, p.X, p.Y, p.Button, p.Action == "mouse_down")
	case "move":
		return winMove(ctx, p.X, p.Y)
	case "drag":
		return winDrag(ctx, p.X, p.Y, p.ToX, p.ToY)
	case "scroll":
		return winScroll(ctx, p.Direction, p.Amount)
	case "modifier_click":
		return winModifierClick(ctx, p.X, p.Y, p.Text)
	case "mouse_position":
		var pt struct{ X, Y int32 }
		r, _, err := pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		if r == 0 {
			return Result{}, fmt.Errorf("GetCursorPos failed: %v", err)
		}
		return Result{Content: fmt.Sprintf("%d %d", pt.X, pt.Y)}, nil

	// ── Keyboard ──
	case "type":
		return winType(ctx, p.Text)
	case "key_press", "key_combo":
		return winKeyCombo(ctx, p.Text)
	case "hold_key":
		return winHoldKey(ctx, p)

	// ── Application ──
	case "launch_app", "open":
		return winOpenApp(ctx, p)

	case "list_windows", "list_apps":
		return winListWindows()

	case "active_app":
		h := getForegroundWindow()
		if h == 0 {
			return Result{Content: "unknown (no foreground window)"}, nil
		}
		title := windowText(h)
		return Result{Content: title}, nil

	case "focus_window":
		h, err := findWindowByTitle(p.Text)
		if err != nil {
			return Result{}, err
		}
		r, _, _ := pSetForegroundWindow.Call(uintptr(h))
		if r == 0 {
			return Result{}, fmt.Errorf("SetForegroundWindow failed (foreground lock may prevent stealing focus)")
		}
		return Result{Content: "OK: focused " + windowText(h)}, nil

	case "close_window":
		h, err := findWindowByTitle(p.Text)
		if err != nil {
			return Result{}, err
		}
		pPostMessageW.Call(uintptr(h), wmClose, 0, 0)
		return Result{Content: "OK: close requested for " + windowText(h)}, nil

	case "minimize_window":
		h, err := findWindowByTitle(p.Text)
		if err != nil {
			return Result{}, err
		}
		pShowWindow.Call(uintptr(h), swMinimize)
		return Result{Content: "OK: minimized " + windowText(h)}, nil

	case "maximize_window":
		h, err := findWindowByTitle(p.Text)
		if err != nil {
			return Result{}, err
		}
		state := swMaximize
		if r, _, _ := pIsIconic.Call(uintptr(h)); r != 0 {
			// Already maximized means iconic-restore cycle is handled by the
			// app; simplest deterministic behavior is maximize (idempotent).
			state = swMaximize
		}
		pShowWindow.Call(uintptr(h), uintptr(state))
		return Result{Content: "OK: maximized " + windowText(h)}, nil

	case "set_window_bounds":
		if p.ToX <= 0 || p.ToY <= 0 {
			return Result{}, fmt.Errorf("set_window_bounds requires positive to_x (width) and to_y (height)")
		}
		h, err := findWindowByTitle(p.Text)
		if err != nil {
			return Result{}, err
		}
		r, _, _ := pMoveWindow.Call(uintptr(h), uintptr(int32(p.X)), uintptr(int32(p.Y)),
			uintptr(int32(p.ToX)), uintptr(int32(p.ToY)), 1)
		if r == 0 {
			return Result{}, fmt.Errorf("MoveWindow failed")
		}
		return Result{Content: fmt.Sprintf("OK: window at (%d,%d) size %dx%d", p.X, p.Y, p.ToX, p.ToY)}, nil

	case "quit_app":
		// Best-effort without process enumeration: close all windows whose
		// class matches — or by title substring as with other window actions.
		h, err := findWindowByTitle(p.Text)
		if err != nil {
			return Result{}, err
		}
		pPostMessageW.Call(uintptr(h), wmClose, 0, 0)
		return Result{Content: "OK: close requested for " + windowText(h)}, nil

	case "display_info":
		// Virtual screen span via GetSystemMetrics (76/79); origin via 76/77
		// is the primary origin — report span as the multi-monitor extent.
		p := user32.NewProc("GetSystemMetrics")
		cx, _, _ := p.Call(76)
		cy, _, _ := p.Call(79)
		return Result{Content: fmt.Sprintf("virtual screen: %dx%d logical pixels (multi-monitor extent)", int32(cx), int32(cy))}, nil

	case "snapshot_ui", "find_element", "find_and_click", "wait_and_click", "menu_select":
		return Result{}, fmt.Errorf("desktop_control: action %q is not yet supported on Windows (requires UI Automation integration)", p.Action)

	default:
		return Result{}, fmt.Errorf("unknown action: %s", p.Action)
	}
}

type winWindowInfo struct {
	handle uintptr
	title  string
	class  string
	pid    int
}

// enumVisibleWindows lists top-level visible windows with non-empty titles.
func enumVisibleWindows() ([]winWindowInfo, error) {
	var out []winWindowInfo
	cb := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		vis, _, _ := pIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1 // continue
		}
		title := windowText(hwnd)
		if strings.TrimSpace(title) == "" {
			return 1
		}
		var pid uint32
		pGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		out = append(out, winWindowInfo{
			handle: hwnd,
			title:  title,
			class:  className(hwnd),
			pid:    int(pid),
		})
		return 1
	})
	r, _, err := pEnumWindows.Call(uintptr(cb), 0)
	if r == 0 {
		return nil, fmt.Errorf("EnumWindows failed: %v", err)
	}
	return out, nil
}

func windowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func className(hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := pGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

// findWindowByTitle finds the first visible top-level window whose title
// contains the given text, using the shared windowTitleMatches semantics.
func findWindowByTitle(needle string) (uintptr, error) {
	if strings.TrimSpace(needle) == "" {
		return 0, fmt.Errorf("window target text is required")
	}
	windows, err := enumVisibleWindows()
	if err != nil {
		return 0, err
	}
	for _, w := range windows {
		if windowTitleMatches(w.title, needle) {
			return w.handle, nil
		}
	}
	return 0, fmt.Errorf("no window with title containing %q", needle)
}

func getForegroundWindow() uintptr {
	p := user32.NewProc("GetForegroundWindow")
	h, _, _ := p.Call()
	return h
}

// ── Extracted action helpers (thin dispatch, fat helpers — same shape as
// the darwin backend) ──

func winClick(ctx context.Context, x, y int, button string, count int) (Result, error) {
	sw, sh := screenSpan()
	var inputs []winINPUT
	inputs = append(inputs, mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
		absCoord(int32(x), sw), absCoord(int32(y), sh)))
	btn := uint32(mouseLeftDown)
	btnUp := uint32(mouseLeftUp)
	if button == "right" {
		btn = mouseRightDown
		btnUp = mouseRightUp
	} else if button == "middle" {
		btn = mouseMiddleDown
		btnUp = mouseMiddleUp
	}
	for i := 0; i < count; i++ {
		inputs = append(inputs, mouseEventInput(btn, 0, 0))
		inputs = append(inputs, mouseEventInput(btnUp, 0, 0))
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

// winMouseButtonEvent posts a single button-down or -up event at (x,y).
func winMouseButtonEvent(ctx context.Context, x, y int, button string, down bool) (Result, error) {
	sw, sh := screenSpan()
	btn := uint32(mouseLeftDown)
	btnUp := uint32(mouseLeftUp)
	switch button {
	case "", "left":
	case "right":
		btn = mouseRightDown
		btnUp = mouseRightUp
	case "middle":
		btn = mouseMiddleDown
		btnUp = mouseMiddleUp
	default:
		return Result{}, fmt.Errorf("unknown button %q (left, right, middle)", button)
	}
	event := btn
	if !down {
		event = btnUp
	}
	inputs := []winINPUT{
		mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
			absCoord(int32(x), sw), absCoord(int32(y), sh)),
		mouseEventInput(event, 0, 0),
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

// winHoldKey presses a key/combo, sleeps durationMs, releases.
func winHoldKey(ctx context.Context, p desktopParams) (Result, error) {
	duration, err := holdKeyDurationClamp(p.Duration)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(p.Text) == "" {
		return Result{}, fmt.Errorf("hold_key requires 'text' (key or combo)")
	}
	parts := strings.Split(p.Text, "+")
	var vks []uint16
	for _, part := range parts {
		name := strings.ToLower(strings.TrimSpace(part))
		vk, ok := winVK[name]
		if !ok {
			if len(name) == 1 && name[0] >= 'a' && name[0] <= 'z' {
				vk = uint16(name[0] - 'a' + 'A')
			} else if len(name) == 1 && name[0] >= '0' && name[0] <= '9' {
				vk = uint16(name[0])
			} else {
				return Result{}, fmt.Errorf("unknown key %q on Windows", part)
			}
		}
		vks = append(vks, vk)
	}
	var inputs []winINPUT
	for _, vk := range vks {
		inputs = append(inputs, keyEventInput(vk, 0))
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	release := func() error {
		var rel []winINPUT
		for i := len(vks) - 1; i >= 0; i-- {
			rel = append(rel, keyEventInput(vks[i], keyUp))
		}
		return sendInput(rel)
	}
	select {
	case <-ctx.Done():
		// Release even on cancellation so the key doesn't stick.
		_ = release()
		return Result{}, ctx.Err()
	case <-time.After(time.Duration(duration) * time.Millisecond):
	}
	if err := release(); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

func winMove(ctx context.Context, x, y int) (Result, error) {
	sw, sh := screenSpan()
	inputs := []winINPUT{mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
		absCoord(int32(x), sw), absCoord(int32(y), sh))}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

func winDrag(ctx context.Context, x, y, toX, toY int) (Result, error) {
	sw, sh := screenSpan()
	inputs := []winINPUT{
		mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
			absCoord(int32(x), sw), absCoord(int32(y), sh)),
		mouseEventInput(mouseLeftDown, 0, 0),
		mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
			absCoord(int32(toX), sw), absCoord(int32(toY), sh)),
		mouseEventInput(mouseLeftUp, 0, 0),
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

func winScroll(ctx context.Context, direction string, amount int) (Result, error) {
	d := int32(120) // WHEEL_DELTA per step
	if direction == "up" {
		d = -120
	}
	var inputs []winINPUT
	for i := 0; i < amount; i++ {
		inputs = append(inputs, mouseEventInput(mouseWheel, 0, d))
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

func winModifierClick(ctx context.Context, x, y int, modifierSpec string) (Result, error) {
	mods, err := normalizeModifiers(modifierSpec)
	if err != nil {
		return Result{}, err
	}
	sw, sh := screenSpan()
	var inputs []winINPUT
	inputs = append(inputs, mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
		absCoord(int32(x), sw), absCoord(int32(y), sh)))
	for _, m := range mods {
		inputs = append(inputs, keyEventInput(winVK[m], 0))
	}
	inputs = append(inputs, mouseEventInput(mouseLeftDown, 0, 0))
	inputs = append(inputs, mouseEventInput(mouseLeftUp, 0, 0))
	for i := len(mods) - 1; i >= 0; i-- {
		inputs = append(inputs, keyEventInput(winVK[mods[i]], keyUp))
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

func winType(ctx context.Context, text string) (Result, error) {
	// Unicode via KEYEVENTF_UNICODE (0x0004) per code unit — no keyboard
	// layout dependence.
	const keyUnicode = 0x0004
	var inputs []winINPUT
	for _, r := range text {
		inputs = append(inputs, keyEventInputWithScan(uint16(r), keyUnicode))
		inputs = append(inputs, keyEventInputWithScan(uint16(r), keyUnicode|keyUp))
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

func winKeyCombo(ctx context.Context, combo string) (Result, error) {
	parts := strings.Split(combo, "+")
	var inputs []winINPUT
	var vks []uint16
	for _, part := range parts {
		name := strings.ToLower(strings.TrimSpace(part))
		vk, ok := winVK[name]
		if !ok {
			if len(name) == 1 && name[0] >= 'a' && name[0] <= 'z' {
				vk = uint16(name[0] - 'a' + 'A')
			} else if len(name) == 1 && name[0] >= '0' && name[0] <= '9' {
				vk = uint16(name[0])
			} else {
				return Result{}, fmt.Errorf("unknown key %q on Windows", part)
			}
		}
		vks = append(vks, vk)
	}
	for _, vk := range vks {
		inputs = append(inputs, keyEventInput(vk, 0))
	}
	for i := len(vks) - 1; i >= 0; i-- {
		inputs = append(inputs, keyEventInput(vks[i], keyUp))
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

func winOpenApp(ctx context.Context, p desktopParams) (Result, error) {
	target := strings.TrimSpace(p.Text)
	if p.Action == "open" && target == "" {
		return Result{}, fmt.Errorf("open requires 'text' (URL or file path)")
	}
	if target == "" {
		return Result{}, fmt.Errorf("%s requires 'text'", p.Action)
	}
	cmdline := target
	if app := strings.TrimSpace(p.App); app != "" {
		cmdline = app + " " + target
	}
	if err := runWindowsCommand(ctx, cmdline); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK: " + cmdline}, nil
}

func winListWindows() (Result, error) {
	windows, err := enumVisibleWindows()
	if err != nil {
		return Result{}, err
	}
	if len(windows) == 0 {
		return Result{Content: "no visible windows"}, nil
	}
	var sb strings.Builder
	for _, w := range windows {
		fmt.Fprintf(&sb, "%s [%s] pid=%d\n", w.title, w.class, w.pid)
	}
	return Result{Content: strings.TrimRight(sb.String(), "\n")}, nil
}

func keyEventInputWithScan(scan uint16, flags uint32) winINPUT {
	// KEYEVENTF_UNICODE path: vk stays 0, scan carries the UTF-16 code unit.
	ki := winKEYBDINPUT{
		Type:  inputKeyboard,
		Vk:    0,
		Scan:  scan,
		Flags: flags,
	}
	var in winINPUT
	in.Type = inputKeyboard
	copy(in.U[:], (*[28]byte)(unsafe.Pointer(&ki))[:])
	return in
}

func runWindowsCommand(ctx context.Context, cmdline string) error {
	// Use cmd /c start "" <cmdline> so URLs and documents open via
	// shell associations, matching the macOS `open` semantics.
	c := exec.CommandContext(ctx, "cmd", "/c", "start", "", cmdline)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("start failed: %w\n%s", err, string(out))
	}
	return nil
}
