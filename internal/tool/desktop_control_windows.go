//go:build windows

package tool

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

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
	pGetWindowRect            = user32.NewProc("GetWindowRect")
	pMoveWindow               = user32.NewProc("MoveWindow")
)

const (
	wmClose    = 0x0010
	swMinimize = 6
	swMaximize = 3
	swRestore  = 9
)

var winVK = map[string]uint16{
	"ctrl":      0x11,
	"control":   0x11,
	"alt":       0x12,
	"option":    0x12,
	"opt":       0x12,
	"shift":     0x10,
	"cmd":       0x5B,
	"command":   0x5B,
	"meta":      0x5B,
	"super":     0x5B,
	"win":       0x5B,
	"fn":        0x5D,
	"enter":     0x0D,
	"return":    0x0D,
	"tab":       0x09,
	"esc":       0x1B,
	"escape":    0x1B,
	"escape2":   0x1B,
	"backspace": 0x08,
	"delete":    0x2E,
	"up":        0x26,
	"down":      0x28,
	"left":      0x25,
	"right":     0x27,
	"space":     0x20,
	"home":      0x24,
	"end":       0x23,
	"pageup":    0x21,
	"pagedown":  0x22,
}

type winINPUT struct {
	Type uint32
	_    uint32
	U    [32]byte
}

type winMOUSEINPUT struct {
	Dx, Dy    int32
	MouseData uint32
	DwFlags   uint32
	Time      uint32
	_         uint32
	ExtraInfo uintptr
}

type winKEYBDINPUT struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	_         uint32
	ExtraInfo uintptr
}

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove        = 0x0001
	mouseAbsolute    = 0x8000
	mouseVirtualDesk = 0x4000
	mouseLeftDown    = 0x0002
	mouseLeftUp      = 0x0004
	mouseRightDown   = 0x0008
	mouseRightUp     = 0x0010
	mouseMiddleDown  = 0x0020
	mouseMiddleUp    = 0x0040
	mouseWheel       = 0x0800

	keyUp = 0x0002
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
		Dx:      dx,
		Dy:      dy,
		DwFlags: flags,
	}
	var in winINPUT
	in.Type = inputMouse

	copy(in.U[:], (*[32]byte)(unsafe.Pointer(&mi))[:])
	return in
}

func wheelEventInput(delta int32) winINPUT {
	mi := winMOUSEINPUT{
		MouseData: uint32(delta),
		DwFlags:   mouseWheel,
	}
	var in winINPUT
	in.Type = inputMouse
	copy(in.U[:], (*[32]byte)(unsafe.Pointer(&mi))[:])
	return in
}

func keyEventInput(vk uint16, flags uint32) winINPUT {
	ki := winKEYBDINPUT{
		Vk:    vk,
		Flags: flags,
	}
	var in winINPUT
	in.Type = inputKeyboard
	copy(in.U[:], (*[32]byte)(unsafe.Pointer(&ki))[:])
	return in
}

func absCoord(v, origin, total int32) int32 {
	if total <= 0 {
		return 0
	}
	rel := v - origin
	if rel < 0 {
		rel = 0
	}
	if rel > total {
		rel = total
	}
	return int32((int64(rel) * 65535) / int64(total))
}

func virtualScreen() (vsx, vsy, vsw, vsh int32) {
	p := user32.NewProc("GetSystemMetrics")

	x, _, _ := p.Call(76)
	y, _, _ := p.Call(77)
	w, _, _ := p.Call(78)
	h, _, _ := p.Call(79)
	return int32(x), int32(y), int32(w), int32(h)
}

func executeDesktopControl(ctx context.Context, p desktopParams) (Result, error) {
	switch p.Action {

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
		return winModifierClick(ctx, p.X, p.Y, p.Text, p.Button)
	case "mouse_position":
		var pt struct{ X, Y int32 }
		r, _, err := pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		if r == 0 {
			return Result{}, fmt.Errorf("GetCursorPos failed: %v", err)
		}
		return Result{Content: fmt.Sprintf("%d %d", pt.X, pt.Y)}, nil

	case "type":
		return winType(ctx, p.Text)
	case "key_press", "key_combo":
		return winKeyCombo(ctx, p.Text)
	case "hold_key":
		return winHoldKey(ctx, p)

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

		pShowWindow.Call(uintptr(h), swMaximize)
		return Result{Content: "OK: maximized " + windowText(h)}, nil

	case "set_window_bounds":
		if p.ToX <= 0 || p.ToY <= 0 {
			return Result{}, fmt.Errorf("set_window_bounds requires positive to_x (width) and to_y (height)")
		}

		var h uintptr
		if strings.TrimSpace(p.Text) == "" {
			fh, _, _ := user32.NewProc("GetForegroundWindow").Call()
			if fh == 0 {
				return Result{}, fmt.Errorf("no foreground window to move")
			}
			h = fh
		} else {
			var err error
			h, err = findWindowByTitle(p.Text)
			if err != nil {
				return Result{}, err
			}
		}
		r, _, _ := pMoveWindow.Call(uintptr(h), uintptr(int32(p.X)), uintptr(int32(p.Y)),
			uintptr(int32(p.ToX)), uintptr(int32(p.ToY)), 1)
		if r == 0 {
			return Result{}, fmt.Errorf("MoveWindow failed")
		}
		return Result{Content: fmt.Sprintf("OK: window at (%d,%d) size %dx%d", p.X, p.Y, p.ToX, p.ToY)}, nil

	case "quit_app":

		if strings.TrimSpace(p.Text) == "" {
			return Result{}, fmt.Errorf("quit_app requires a window title text; refusing to match all windows")
		}
		windows, err := enumVisibleWindows()
		if err != nil {
			return Result{}, err
		}
		needle := strings.ToLower(p.Text)
		closed := 0
		for _, w := range windows {
			if strings.Contains(strings.ToLower(w.title), needle) {
				pPostMessageW.Call(uintptr(w.handle), wmClose, 0, 0)
				closed++
			}
		}
		if closed == 0 {
			return Result{Content: "no windows found matching " + p.Text}, nil
		}
		return Result{Content: fmt.Sprintf("OK: close requested for %d window(s) matching %q", closed, p.Text)}, nil

	case "display_info":

		p := user32.NewProc("GetSystemMetrics")
		cx, _, _ := p.Call(78)
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

var enumCBOnce sync.Once

var enumCB uintptr

func initEnumCB() {
	enumCB = syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		out := (*[]winWindowInfo)(unsafe.Pointer(lparam))
		vis, _, _ := pIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1
		}
		title := windowText(hwnd)
		if strings.TrimSpace(title) == "" {
			return 1
		}
		var pid uint32
		pGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		*out = append(*out, winWindowInfo{
			handle: hwnd,
			title:  title,
			class:  className(hwnd),
			pid:    int(pid),
		})
		return 1
	})
}

func enumVisibleWindows() ([]winWindowInfo, error) {
	enumCBOnce.Do(initEnumCB)
	var out []winWindowInfo
	r, _, err := pEnumWindows.Call(enumCB, uintptr(unsafe.Pointer(&out)))
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

func winClick(ctx context.Context, x, y int, button string, count int) (Result, error) {
	vsx, vsy, vsw, vsh := virtualScreen()
	var inputs []winINPUT
	inputs = append(inputs, mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
		absCoord(int32(x), vsx, vsw), absCoord(int32(y), vsy, vsh)))
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

func winMouseButtonEvent(ctx context.Context, x, y int, button string, down bool) (Result, error) {
	vsx, vsy, vsw, vsh := virtualScreen()
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
			absCoord(int32(x), vsx, vsw), absCoord(int32(y), vsy, vsh)),
		mouseEventInput(event, 0, 0),
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

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
	vsx, vsy, vsw, vsh := virtualScreen()
	inputs := []winINPUT{mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
		absCoord(int32(x), vsx, vsw), absCoord(int32(y), vsy, vsh))}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

func winDrag(ctx context.Context, x, y, toX, toY int) (Result, error) {
	vsx, vsy, vsw, vsh := virtualScreen()
	inputs := []winINPUT{
		mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
			absCoord(int32(x), vsx, vsw), absCoord(int32(y), vsy, vsh)),
		mouseEventInput(mouseLeftDown, 0, 0),
		mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
			absCoord(int32(toX), vsx, vsw), absCoord(int32(toY), vsy, vsh)),
		mouseEventInput(mouseLeftUp, 0, 0),
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

func winScroll(ctx context.Context, direction string, amount int) (Result, error) {

	d := int32(-120)
	if direction == "up" {
		d = 120
	}
	var inputs []winINPUT
	for i := 0; i < amount; i++ {
		inputs = append(inputs, wheelEventInput(d))
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

func winModifierClick(ctx context.Context, x, y int, modifierSpec, button string) (Result, error) {
	mods, err := normalizeModifiers(modifierSpec)
	if err != nil {
		return Result{}, err
	}

	var down, up uint32 = mouseLeftDown, mouseLeftUp
	switch button {
	case "right":
		down, up = mouseRightDown, mouseRightUp
	case "middle":
		down, up = mouseMiddleDown, mouseMiddleUp
	case "", "left":
	default:
		return Result{}, fmt.Errorf("unsupported button %q (supported: left, right, middle)", button)
	}
	vsx, vsy, vsw, vsh := virtualScreen()
	var inputs []winINPUT
	inputs = append(inputs, mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
		absCoord(int32(x), vsx, vsw), absCoord(int32(y), vsy, vsh)))
	for _, m := range mods {
		inputs = append(inputs, keyEventInput(winVK[m], 0))
	}
	inputs = append(inputs, mouseEventInput(down, 0, 0))
	inputs = append(inputs, mouseEventInput(up, 0, 0))
	for i := len(mods) - 1; i >= 0; i-- {
		inputs = append(inputs, keyEventInput(winVK[mods[i]], keyUp))
	}
	if err := sendInput(inputs); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK"}, nil
}

func winType(ctx context.Context, text string) (Result, error) {

	const keyUnicode = 0x0004
	var inputs []winINPUT
	for _, u := range utf16.Encode([]rune(text)) {
		inputs = append(inputs, keyEventInputWithScan(u, keyUnicode))
		inputs = append(inputs, keyEventInputWithScan(u, keyUnicode|keyUp))
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

	var argv []string
	if app := strings.TrimSpace(p.App); app != "" {
		argv = append(argv, app)
	}
	if p.Action == "launch_app" {
		argv = append(argv, strings.Fields(target)...)
	} else {
		argv = append(argv, target)
	}
	if err := runWindowsCommand(ctx, argv...); err != nil {
		return Result{}, err
	}
	return Result{Content: "OK: " + strings.Join(argv, " ")}, nil
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

	ki := winKEYBDINPUT{
		Vk:    0,
		Scan:  scan,
		Flags: flags,
	}
	var in winINPUT
	in.Type = inputKeyboard
	copy(in.U[:], (*[32]byte)(unsafe.Pointer(&ki))[:])
	return in
}

func runWindowsCommand(ctx context.Context, argv ...string) error {

	startArgs := append([]string{"/c", "start", ""}, argv...)
	c := exec.CommandContext(ctx, "cmd", startArgs...)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("start failed: %w\n%s", err, string(out))
	}
	return nil
}
