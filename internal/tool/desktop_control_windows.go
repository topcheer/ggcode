//go:build windows

package tool

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// Windows implementation via the SendInput API (core subset: mouse,
// keyboard, application launch). Window-management and UI-tree actions
// require UI Automation / Win32 APIs and are reported as unsupported for
// now so the agent gets a clear message instead of "unknown action".

var (
	user32        = syscall.NewLazyDLL("user32.dll")
	pSendInput    = user32.NewProc("SendInput")
	pGetCursorPos = user32.NewProc("GetCursorPos")
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
		sw, sh := screenSpan()
		var inputs []winINPUT
		inputs = append(inputs, mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
			absCoord(int32(p.X), sw), absCoord(int32(p.Y), sh)))
		btn := uint32(mouseLeftDown)
		btnUp := uint32(mouseLeftUp)
		if p.Button == "right" {
			btn = mouseRightDown
			btnUp = mouseRightUp
		}
		for i := 0; i < count; i++ {
			inputs = append(inputs, mouseEventInput(btn, 0, 0))
			inputs = append(inputs, mouseEventInput(btnUp, 0, 0))
		}
		if err := sendInput(inputs); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil
	case "move":
		sw, sh := screenSpan()
		inputs := []winINPUT{mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
			absCoord(int32(p.X), sw), absCoord(int32(p.Y), sh))}
		if err := sendInput(inputs); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil
	case "drag":
		sw, sh := screenSpan()
		var inputs []winINPUT
		inputs = append(inputs, mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
			absCoord(int32(p.X), sw), absCoord(int32(p.Y), sh)))
		inputs = append(inputs, mouseEventInput(mouseLeftDown, 0, 0))
		inputs = append(inputs, mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
			absCoord(int32(p.ToX), sw), absCoord(int32(p.ToY), sh)))
		inputs = append(inputs, mouseEventInput(mouseLeftUp, 0, 0))
		if err := sendInput(inputs); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil
	case "scroll":
		d := int32(120) // WHEEL_DELTA per step
		if p.Direction == "up" {
			d = -120
		}
		var inputs []winINPUT
		for i := 0; i < p.Amount; i++ {
			inputs = append(inputs, mouseEventInput(mouseWheel, 0, d))
		}
		if err := sendInput(inputs); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil
	case "modifier_click":
		mods, err := normalizeModifiers(p.Text)
		if err != nil {
			return Result{}, err
		}
		sw, sh := screenSpan()
		var inputs []winINPUT
		inputs = append(inputs, mouseEventInput(mouseAbsolute|mouseVirtualDesk|mouseMove,
			absCoord(int32(p.X), sw), absCoord(int32(p.Y), sh)))
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
	case "mouse_position":
		var pt struct{ X, Y int32 }
		r, _, err := pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		if r == 0 {
			return Result{}, fmt.Errorf("GetCursorPos failed: %v", err)
		}
		return Result{Content: fmt.Sprintf("%d %d", pt.X, pt.Y)}, nil

	// ── Keyboard ──
	case "type":
		// Per-character: modifier-free chars via shifted VK when possible,
		// otherwise unicode via KEYEVENTF_UNICODE (0x0004).
		const keyUnicode = 0x0004
		var inputs []winINPUT
		for _, r := range p.Text {
			inputs = append(inputs, keyEventInputWithScan(uint16(r), keyUnicode))
			inputs = append(inputs, keyEventInputWithScan(uint16(r), keyUnicode|keyUp))
		}
		if err := sendInput(inputs); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK"}, nil
	case "key_press", "key_combo":
		parts := strings.Split(p.Text, "+")
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

	// ── Application ──
	case "launch_app", "open":
		target := strings.TrimSpace(p.Text)
		if p.Action == "open" && target == "" {
			return Result{}, fmt.Errorf("open requires 'text' (URL or file path)")
		}
		if target == "" {
			return Result{}, fmt.Errorf("%s requires 'text'", p.Action)
		}
		app := ""
		if a := strings.TrimSpace(p.App); a != "" {
			app = a
		}
		cmdline := target
		if app != "" {
			cmdline = app + " " + target
		}
		if err := runWindowsCommand(ctx, cmdline); err != nil {
			return Result{}, err
		}
		return Result{Content: "OK: " + cmdline}, nil

	case "list_windows", "focus_window", "close_window", "minimize_window",
		"maximize_window", "set_window_bounds", "quit_app", "list_apps",
		"active_app", "snapshot_ui", "find_element", "find_and_click",
		"wait_and_click", "display_info", "menu_select":
		return Result{}, fmt.Errorf("desktop_control: action %q is not yet supported on Windows (requires Win32/UI Automation integration)", p.Action)

	default:
		return Result{}, fmt.Errorf("unknown action: %s", p.Action)
	}
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
