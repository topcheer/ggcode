//go:build darwin

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func executeDesktopControl(ctx context.Context, p desktopParams) (Result, error) {
	switch p.Action {
	// ── Mouse ──
	case "click":
		return mouseClick(ctx, p.X, p.Y, "left", 1)
	case "double_click":
		return mouseClick(ctx, p.X, p.Y, "left", 2)
	case "triple_click":
		return mouseClick(ctx, p.X, p.Y, "left", 3)
	case "right_click":
		return mouseClick(ctx, p.X, p.Y, "right", 1)
	case "middle_click":
		return mouseClick(ctx, p.X, p.Y, "middle", 1)
	case "mouse_down", "mouse_up":
		return mouseButtonEvent(ctx, p.X, p.Y, p.Button, p.Action == "mouse_down")
	case "move":
		return mouseMove(ctx, p.X, p.Y)
	case "drag":
		return mouseDrag(ctx, p.X, p.Y, p.ToX, p.ToY, p.Duration)
	case "scroll":
		return mouseScroll(ctx, p.X, p.Y, p.Direction, p.Amount)
	case "modifier_click":
		return modifierClick(ctx, p.X, p.Y, p.Text)

	// ── Keyboard ──
	case "type":
		return typeText(ctx, p.Text)
	case "key_press", "key_combo":
		return keyComboResult(ctx, p.Text)
	case "hold_key":
		return holdKey(ctx, p.Text, p.Duration)

	// ── Window management ──
	case "set_window_bounds":
		return setWindowBounds(ctx, p)
	case "list_windows":
		return listWindowsAppleScript(ctx)
	case "focus_window":
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application %s
  activate
end tell`, applescriptQuote(p.Text)))
	case "close_window":
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application %s
  close front window
end tell`, applescriptQuote(p.Text)))
	case "minimize_window":
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application %s
  set miniaturized of front window to true
end tell`, applescriptQuote(p.Text)))
	case "maximize_window":
		// macOS doesn't have true maximize; use fullscreen toggle
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application %s
  set fullscreen of front window to not fullscreen of front window
end tell`, applescriptQuote(p.Text)))

	// ── Application ──
	case "open":
		return openTarget(ctx, p)
	case "launch_app":
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application %s
  activate
end tell`, applescriptQuote(p.Text)))
	case "quit_app":
		return appleScriptResult(ctx, fmt.Sprintf(`
tell application %s
  quit
end tell`, applescriptQuote(p.Text)))
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

	// ── Accessibility tree / composite ──
	case "menu_select":
		return menuSelect(ctx, p)
	case "snapshot_ui":
		return snapshotUI(ctx, p.MaxDepth)
	case "find_element":
		return findElement(ctx, p.Text, p.MaxDepth)
	case "find_and_click":
		return findAndClick(ctx, p.Text, p.MaxDepth)
	case "wait_and_click":
		return waitAndClick(ctx, p.Text, p.MaxDepth, p.TimeoutMs)
	case "display_info":
		return displayInfo(ctx)
	case "mouse_position":
		return mousePosition(ctx)

	default:
		return Result{}, fmt.Errorf("unknown action: %s", p.Action)
	}
}

// mouseClick moves to (x,y) then performs N clicks via Swift CGEvent.
// Zero external dependencies — uses system swift + CoreGraphics.
func mouseClick(ctx context.Context, x, y int, button string, count int) (Result, error) {
	downType := ".leftMouseDown"
	upType := ".leftMouseUp"
	if button == "right" {
		downType = ".rightMouseDown"
		upType = ".rightMouseUp"
	} else if button == "middle" {
		downType = ".otherMouseDown"
		upType = ".otherMouseUp"
	}
	// For the middle button, CGEvent needs mouseButton .center (button 2);
	// left/right both use .left because the event type encodes the button.
	swiftButton := ".left"
	if button == "middle" {
		swiftButton = ".center"
	}
	return runSwiftCGEvent(ctx, fmt.Sprintf(`
import CoreGraphics
let point = CGPoint(x: %d, y: %d)
for _ in 0..<%d {
    let eDown = CGEvent(mouseEventSource: nil, mouseType: CGEventType%s,
                        mouseCursorPosition: point, mouseButton: CGMouseButton%s)
    eDown?.post(tap: .cghidEventTap)
    let eUp = CGEvent(mouseEventSource: nil, mouseType: CGEventType%s,
                      mouseCursorPosition: point, mouseButton: CGMouseButton%s)
    eUp?.post(tap: .cghidEventTap)
}
`, x, y, count, downType, swiftButton, upType, swiftButton))
}

// mouseButtonEvent posts a single button-down or button-up event at
// (x,y). Splitting press/release lets agents build long-press and
// cross-call drag interactions (down ... move ... up) that a one-shot
// click cannot express.
func mouseButtonEvent(ctx context.Context, x, y int, button string, down bool) (Result, error) {
	typeStr := "Up"
	if down {
		typeStr = "Down"
	}
	switch button {
	case "", "left":
		button = "left"
	case "right", "middle":
	default:
		return Result{}, fmt.Errorf("unknown button %q (left, right, middle)", button)
	}
	var swiftType, swiftBtn string
	switch button {
	case "left":
		swiftType = ".leftMouse" + typeStr
		swiftBtn = ".left"
	case "right":
		swiftType = ".rightMouse" + typeStr
		swiftBtn = ".left"
	case "middle":
		swiftType = ".otherMouse" + typeStr
		swiftBtn = ".center"
	}
	return runSwiftCGEvent(ctx, fmt.Sprintf(`
import CoreGraphics
let point = CGPoint(x: %d, y: %d)
let e = CGEvent(mouseEventSource: nil, mouseType: CGEventType%s,
                mouseCursorPosition: point, mouseButton: CGMouseButton%s)
e?.post(tap: .cghidEventTap)
`, x, y, swiftType, swiftBtn))
}

// holdKey presses a key/combo, holds it for durationMs, then releases.
// Uses AppleScript for the press/hold/release since CGEvent key events
// require virtual-key code mapping that AppleSystemEvents gets right for
// named keys.
func holdKey(ctx context.Context, keys string, durationMs int) (Result, error) {
	duration, err := holdKeyDurationClamp(durationMs)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(keys) == "" {
		return Result{}, fmt.Errorf("hold_key requires 'text' (key or combo like 'shift' or 'cmd+tab')")
	}
	// AppleScript: key down, delay, key up.
	script := fmt.Sprintf(`
tell application "System Events"
  key down %s
  delay %f
  key up %s
end tell`, applescriptKeyLiteral(keys), float64(duration)/1000.0, applescriptKeyLiteral(keys))
	return appleScriptResult(ctx, script)
}

// applescriptKeyLiteral converts "cmd+tab" to AppleScript key down syntax:
// key down {command down, tab}. System Events accepts both key names and
// modifier-down tokens in the list.
func applescriptKeyLiteral(keys string) string {
	parts := strings.Split(keys, "+")
	var tokens []string
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		switch p {
		case "cmd", "command", "meta", "super":
			tokens = append(tokens, "command down")
		case "ctrl", "control":
			tokens = append(tokens, "control down")
		case "alt", "option", "opt":
			tokens = append(tokens, "option down")
		case "shift":
			tokens = append(tokens, "shift down")
		default:
			tokens = append(tokens, p)
		}
	}
	if len(tokens) == 1 {
		return tokens[0]
	}
	return "{" + strings.Join(tokens, ", ") + "}"
}

// mouseMove moves the cursor to (x,y) via Swift CGEvent.
func mouseMove(ctx context.Context, x, y int) (Result, error) {
	return runSwiftCGEvent(ctx, fmt.Sprintf(`
import CoreGraphics
let point = CGPoint(x: %d, y: %d)
let e = CGEvent(mouseEventSource: nil, mouseType: .mouseMoved,
                mouseCursorPosition: point, mouseButton: .left)
e?.post(tap: .cghidEventTap)
`, x, y))
}

// mouseDrag drags from (x,y) to (toX,toY) via Swift CGEvent. When durationMs
// > 0 the move is interpolated into intermediate mouseDragged events so
// trajectory-sensitive targets (sliders, Dock tear-offs, drag-and-drop with
// spring loading) register a real drag path instead of a teleport.
func mouseDrag(ctx context.Context, x, y, toX, toY, durationMs int) (Result, error) {
	steps := 0
	intervalMs := 8 // ~125 events/sec — enough for targets to track motion
	if durationMs > 0 {
		steps = durationMs / intervalMs
		if steps < 2 {
			steps = 2
		}
		if steps > 250 {
			steps = 250 // cap event count; 250 events over >=2s is still smooth
		}
	}
	return runSwiftCGEvent(ctx, fmt.Sprintf(`
import CoreGraphics
import Foundation
let start = CGPoint(x: %d, y: %d)
let end = CGPoint(x: %d, y: %d)
let eDown = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown,
                    mouseCursorPosition: start, mouseButton: .left)
eDown?.post(tap: .cghidEventTap)
let steps = %d
let intervalMs = %d.0
if steps < 1 {
    // Instant drag: single move to the end point (legacy behavior).
    let eDrag = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDragged,
                        mouseCursorPosition: end, mouseButton: .left)
    eDrag?.post(tap: .cghidEventTap)
} else {
    for i in 1...steps {
        let t = Double(i) / Double(steps)
        let p = CGPoint(x: start.x + (end.x - start.x) * t,
                        y: start.y + (end.y - start.y) * t)
        let eDrag = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDragged,
                             mouseCursorPosition: p, mouseButton: .left)
        eDrag?.post(tap: .cghidEventTap)
        if i < steps {
            usleep(useconds_t(intervalMs * 1000))
        }
    }
}
let eUp = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp,
                  mouseCursorPosition: end, mouseButton: .left)
eUp?.post(tap: .cghidEventTap)
`, x, y, toX, toY, steps, intervalMs))
}

// scrollPixelsPerStep converts the schema's "steps" contract into CGEvent
// pixels. #1003: the shared schema defines amount as "number of steps"
// (Windows sends +-120 per step, X11 repeats one click per step), but macOS
// CGEvent units:.pixel consumed amount raw - a default scroll of 1 pixel was
// imperceptible against native wheel lines of hundreds of pixels, and agents
// retried with bigger amounts that stayed sub-visible. 40px/step matches a
// typical smooth-scrolling line height, so N steps move roughly the same
// content distance on every platform.
const scrollPixelsPerStep = 40

// mouseScroll scrolls at (x,y) in the given direction via Swift CGEvent.
func mouseScroll(ctx context.Context, x, y int, direction string, amount int) (Result, error) {
	yDelta := amount * scrollPixelsPerStep
	if direction == "down" {
		yDelta = -yDelta
	}
	return runSwiftCGEvent(ctx, fmt.Sprintf(`
import CoreGraphics
let point = CGPoint(x: %d, y: %d)
let e = CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 1, wheel1: %d, wheel2: 0, wheel3: 0)
e?.location = point
e?.post(tap: .cghidEventTap)
`, x, y, yDelta))
}

// typeText types a string via Swift CGEvent keyboard events.
// Uses CGEventCreateKeyboardEvent at HID level for maximum compatibility,
// including Electron-based editors (VS Code, Cursor) where AppleScript
// keystroke is unreliable.
// listWindowsAppleScript enumerates every foreground app process and its
// windows via System Events.
func listWindowsAppleScript(ctx context.Context) (Result, error) {
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
}

func typeText(ctx context.Context, text string) (Result, error) {
	// Build a Swift script that types each character via CGEvent.
	// CGEventCreateKeyboardEvent with keyCode 0 + unicode payload works
	// for arbitrary Unicode characters without needing key code mapping.
	// Control characters MUST be escaped: a raw newline/tab inside the
	// double-quoted Swift literal is a compile error ("unterminated string
	// literal" / "unprintable ASCII character found in source file").
	return runSwiftCGEvent(ctx, fmt.Sprintf(`
import CoreGraphics
let text = "%s"
for char in text {
    let keyCode: CGKeyCode = 0
    let chars = Array(String(char).utf16)
    // Key down
    let eDown = CGEvent(keyboardEventSource: nil, virtualKey: keyCode, keyDown: true)
    eDown?.keyboardSetUnicodeString(stringLength: chars.count, unicodeString: chars)
    eDown?.post(tap: .cghidEventTap)
    // Key up
    let eUp = CGEvent(keyboardEventSource: nil, virtualKey: keyCode, keyDown: false)
    eUp?.keyboardSetUnicodeString(stringLength: chars.count, unicodeString: chars)
    eUp?.post(tap: .cghidEventTap)
}
`, swiftStringLiteral(text)))
}

// swiftStringLiteral escapes a Go string for safe embedding inside a
// Swift double-quoted string literal. Backslash and quote are escaped, and
// every control character (< 0x20) becomes a \n/\r/\t or \u{XX} escape so
// multi-line input compiles instead of failing with a Swift syntax error.
// NOTE: \u{XX} is Swift-only syntax — do NOT reuse this for AppleScript
// (osascript rejects it with -2741); use appleScriptStringLiteral there.
func swiftStringLiteral(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u{%X}`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// runSwiftCGEvent runs an inline Swift script that uses CoreGraphics
// CGEvent API for mouse events. Zero external dependencies — swift
// and CoreGraphics are pre-installed on every macOS system.
func runSwiftCGEvent(ctx context.Context, code string) (Result, error) {
	cmd := exec.CommandContext(ctx, "swift", "-e", code)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("Swift CGEvent failed: %w\n%s (ensure app has Accessibility permission)", err, string(out))
	}
	return Result{Content: "OK"}, nil
}

// modifierClick holds modifier keys down while clicking (multi-select,
// force-open-in-new-tab, etc.). modifiers is a normalized spec like
// "cmd+shift".
func modifierClick(ctx context.Context, x, y int, modifiers string) (Result, error) {
	mods, err := normalizeModifiers(modifiers)
	if err != nil {
		return Result{}, err
	}
	// Map to CGEventFlags raw values: these overlap-free masks can be OR-ed.
	var flags uint64
	for _, m := range mods {
		switch m {
		case "cmd":
			flags |= 1 << 20 // kCGEventFlagMaskCommand
		case "ctrl":
			flags |= 1 << 18 // kCGEventFlagMaskControl
		case "alt":
			flags |= 1 << 19 // kCGEventFlagMaskAlternate
		case "shift":
			flags |= 1 << 17 // kCGEventFlagMaskShift
		case "fn":
			flags |= 1 << 23 // kCGEventFlagMaskSecondaryFn
		}
	}
	return runSwiftCGEvent(ctx, fmt.Sprintf(`
import CoreGraphics
let point = CGPoint(x: %d, y: %d)
let flags = CGEventFlags(rawValue: %d)
let eDown = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown,
                    mouseCursorPosition: point, mouseButton: .left)
eDown?.flags = flags
eDown?.post(tap: .cghidEventTap)
let eUp = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp,
                  mouseCursorPosition: point, mouseButton: .left)
eUp?.flags = flags
eUp?.post(tap: .cghidEventTap)
`, x, y, flags))
}

// mousePosition returns the current cursor position in logical pixels.
func mousePosition(ctx context.Context) (Result, error) {
	// Cannot reuse runSwiftCGEvent here: it discards stdout. This action
	// must return the printed coordinates.
	cmd := exec.CommandContext(ctx, "swift", "-e", `
import CoreGraphics
import Foundation
let loc = CGEvent(source: nil)?.location ?? CGPoint(x: 0, y: 0)
// CGEvent.location is in global display (top-left origin) coordinates —
// the same logical-pixel space the click/move actions use.
print(Int(loc.x), Int(loc.y))
`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("Swift CGEvent failed: %w\n%s (ensure app has Accessibility permission)", err, string(out))
	}
	return Result{Content: strings.TrimSpace(string(out))}, nil
}

// openTarget opens a URL or file path with the default handler, or a
// specific app when p.App is set.
func openTarget(ctx context.Context, p desktopParams) (Result, error) {
	target := strings.TrimSpace(p.Text)
	if target == "" {
		return Result{}, fmt.Errorf("open requires 'text' (URL or file path)")
	}
	args := []string{target}
	if app := strings.TrimSpace(p.App); app != "" {
		args = append([]string{"-a", app}, args...)
	}
	cmd := exec.CommandContext(ctx, "open", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("open failed: %w\n%s", err, string(out))
	}
	return Result{Content: "OK: opened " + target}, nil
}

// setWindowBounds positions and sizes the frontmost window of the given
// app (or the frontmost app when p.App is empty).
func setWindowBounds(ctx context.Context, p desktopParams) (Result, error) {
	if p.ToX <= 0 || p.ToY <= 0 {
		return Result{}, fmt.Errorf("set_window_bounds requires positive to_x (width) and to_y (height)")
	}
	return appleScriptResult(ctx, fmt.Sprintf(`
tell application "System Events"
  tell (first process whose frontmost is true)
    set position of front window to {%d, %d}
    set size of front window to {%d, %d}
  end tell
end tell`, p.X, p.Y, p.ToX, p.ToY))
}

// menuSelect clicks a menu bar item by path, e.g. "File > Export…".
// Uses System Events UI scripting; works even when the app does not
// expose standard AppleScript menus.
func menuSelect(ctx context.Context, p desktopParams) (Result, error) {
	parts, err := parseMenuPath(p.Text)
	if err != nil {
		return Result{}, err
	}
	var sb strings.Builder
	sb.WriteString(`
tell application "System Events"
  tell (first process whose frontmost is true)
    tell menu bar 1
`)
	// Click the top-level menu bar item to open its menu.
	sb.WriteString(fmt.Sprintf("      click menu bar item %s\n", applescriptQuote(parts[0])))
	// Walk down: each level is "menu item X of menu X of <parent chain> of menu bar item P1".
	// Clicking an intermediate submenu item opens the next menu; the final
	// click selects. #819: a submenu is named after its PARENT item (System
	// Events), so 'menu %s' must carry parts[depth-1], not parts[depth] —
	// the old self-named chain referenced a nonexistent menu and every
	// depth>=2 selection failed with -1728.
	for depth := 1; depth < len(parts); depth++ {
		var chain strings.Builder
		chain.WriteString(fmt.Sprintf("menu item %s of menu %s",
			applescriptQuote(parts[depth]), applescriptQuote(parts[depth-1])))
		for j := depth - 1; j >= 1; j-- {
			chain.WriteString(fmt.Sprintf(" of menu item %s of menu %s",
				applescriptQuote(parts[j]), applescriptQuote(parts[j-1])))
		}
		chain.WriteString(fmt.Sprintf(" of menu bar item %s", applescriptQuote(parts[0])))
		sb.WriteString(fmt.Sprintf("      click %s\n", chain.String()))
	}
	sb.WriteString("    end tell\n  end tell\nend tell")
	return appleScriptResult(ctx, sb.String())
}

// snapshotUI returns the accessibility tree of the frontmost application
// as a JSON array of elements with role, label, frame (x,y,w,h), and enabled.
func snapshotUI(ctx context.Context, maxDepth int) (Result, error) {
	if maxDepth <= 0 {
		maxDepth = 8
	}
	script := fmt.Sprintf(`
ObjC.import('AppKit');
(function() {
  var se = Application('System Events');
  var proc = se.processes.whose({frontmost: true})[0];
  if (!proc) return JSON.stringify({error: "no frontmost process"});
  var results = [];
  var maxDepth = %d;

  function walk(elem, depth, parentPath) {
    if (depth > maxDepth) return;
    try {
      var role = elem.role();
      var name = "";
      try { name = elem.name(); } catch(e) {}
      var desc = "";
      try { desc = elem.description(); } catch(e) {}
      // #1321 (snapshotUI parity with findElement): static text often
      // only exists in AXValue while AXTitle stays empty.
      var value = "";
      try {
        var v = elem.value();
        if (typeof v === "string") value = v;
      } catch(e) {}

      var frame = null;
      try {
        var pos = elem.position();
        var size = elem.size();
        if (pos && size) {
          frame = {x: pos[0], y: pos[1], w: size[0], h: size[1]};
        }
      } catch(e) {}

      var enabled = true;
      try { enabled = elem.enabled(); } catch(e) {}

      var path = parentPath + "/" + (role || "?");
      var label = name || desc || value || "";

      if (frame || label) {
        results.push({
          role: role || "",
          label: label,
          frame: frame,
          enabled: enabled,
          depth: depth,
          path: path
        });
      }

      try {
        var children = elem.uiElements();
        for (var i = 0; i < children.length && i < 200; i++) {
          walk(children[i], depth + 1, path);
        }
      } catch(e) {}
    } catch(e) {}
  }

  walk(proc, 0, "");
  return JSON.stringify(results, null, 2);
})();
`, maxDepth)
	return runJXA(ctx, script)
}

// findElement searches the accessibility tree for elements whose label
// contains the search text (case-insensitive). Returns matching elements
// with their coordinates.
func findElement(ctx context.Context, searchText string, maxDepth int) (Result, error) {
	if maxDepth <= 0 {
		maxDepth = 8
	}
	script := fmt.Sprintf(`
ObjC.import('AppKit');
(function() {
  var se = Application('System Events');
  var proc = se.processes.whose({frontmost: true})[0];
  if (!proc) return JSON.stringify({error: "no frontmost process"});
  var search = %q;
  var results = [];
  var maxDepth = %d;

  function walk(elem, depth) {
    if (depth > maxDepth) return;
    try {
      var role = elem.role();
      var name = "";
      try { name = elem.name(); } catch(e) {}
      var desc = "";
      try { desc = elem.description(); } catch(e) {}
      // #1321: static text lives in AXValue, AXTitle is often empty -
      // without this, find_element by visible text returns nothing.
      var value = "";
      try {
        var v = elem.value();
        if (typeof v === "string") value = v;
      } catch(e) {}
      var label = (name + " " + desc + " " + value).toLowerCase();

      if (label.indexOf(search.toLowerCase()) >= 0) {
        var frame = null;
        try {
          var pos = elem.position();
          var size = elem.size();
          if (pos && size) {
            frame = {x: pos[0], y: pos[1], w: size[0], h: size[1]};
          }
        } catch(e) {}
        results.push({
          role: role || "",
          label: name || desc || value || "",
          frame: frame,
          depth: depth
        });
      }

      try {
        var children = elem.uiElements();
        for (var i = 0; i < children.length && i < 200; i++) {
          walk(children[i], depth + 1);
        }
      } catch(e) {}
    } catch(e) {}
  }

  walk(proc, 0, "");
  return JSON.stringify(results, null, 2);
})();
`, searchText, maxDepth)
	return runJXA(ctx, script)
}

// findAndClick finds a UI element by text and clicks its center.
func findAndClick(ctx context.Context, searchText string, maxDepth int) (Result, error) {
	findRes, err := findElement(ctx, searchText, maxDepth)
	if err != nil {
		return Result{}, fmt.Errorf("find_element failed: %w", err)
	}

	type elemInfo struct {
		Role  string `json:"role"`
		Label string `json:"label"`
		Frame *struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
			W float64 `json:"w"`
			H float64 `json:"h"`
		} `json:"frame"`
	}
	var matches []elemInfo
	if err := json.Unmarshal([]byte(findRes.Content), &matches); err != nil || len(matches) == 0 {
		return Result{Content: fmt.Sprintf("No UI element found matching %q", searchText)}, nil
	}

	for _, m := range matches {
		if m.Frame != nil && m.Frame.W > 0 && m.Frame.H > 0 {
			cx := int(m.Frame.X + m.Frame.W/2)
			cy := int(m.Frame.Y + m.Frame.H/2)
			_, err := mouseClick(ctx, cx, cy, "left", 1)
			if err != nil {
				return Result{}, fmt.Errorf("click at (%d,%d) failed: %w", cx, cy, err)
			}
			return Result{Content: fmt.Sprintf("Found %q at (%d,%d) size %.0fx%.0f, clicked center (%d,%d)",
				m.Label, int(m.Frame.X), int(m.Frame.Y), m.Frame.W, m.Frame.H, cx, cy)}, nil
		}
	}
	return Result{Content: fmt.Sprintf("Found %d matches for %q but none had clickable coordinates", len(matches), searchText)}, nil
}

// waitAndClick polls for a UI element and clicks it when it appears.
func waitAndClick(ctx context.Context, searchText string, maxDepth, timeoutMs int) (Result, error) {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	pollInterval := 200 * time.Millisecond
	for time.Now().Before(deadline) {
		res, err := findAndClick(ctx, searchText, maxDepth)
		if err == nil && !strings.Contains(res.Content, "No UI element") && !strings.Contains(res.Content, "none had clickable") {
			return res, nil
		}
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	return Result{Content: fmt.Sprintf("Timed out waiting for element %q after %dms", searchText, timeoutMs)}, nil
}

// runJXA runs a JavaScript for Automation script via osascript.
func runJXA(ctx context.Context, script string) (Result, error) {
	cmd := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("JXA failed: %w (ensure Terminal/your app has Accessibility permissions in System Settings > Privacy & Security)", err)
	}
	result := strings.TrimSpace(string(out))
	if result == "" {
		result = "[]"
	}
	return Result{Content: result}, nil
}

// displayInfo returns logical and physical display dimensions and the scale factor.
func displayInfo(ctx context.Context) (Result, error) {
	script := `
ObjC.import('AppKit');
(function() {
  var results = [];
  var screens = $.NSScreen.screens;
  for (var i = 0; i < screens.count; i++) {
    var screen = screens.objectAtIndex(i);
    var frame = screen.frame;
    var bscale = screen.backingScaleFactor;
    var logicalW = frame.size.width;
    var logicalH = frame.size.height;
    results.push({
      index: i,
      logical: {width: logicalW, height: logicalH},
      physical: {width: Math.round(logicalW * bscale), height: Math.round(logicalH * bscale)},
      scale: bscale,
      isRetina: bscale > 1
    });
  }
  return JSON.stringify(results, null, 2);
})();
`
	return runJXA(ctx, script)
}

// keyComboResult translates key combo strings (e.g. "cmd+c", "ctrl+shift+tab")
// to AppleScript System Events key code commands.
func keyComboResult(ctx context.Context, combo string) (Result, error) {
	parts := strings.Split(combo, "+")
	if len(parts) == 0 {
		return Result{}, fmt.Errorf("empty key combo")
	}

	modifiers := []string{}
	for _, mod := range parts[:len(parts)-1] {
		mod = strings.TrimSpace(strings.ToLower(mod))
		switch mod {
		case "cmd", "command":
			modifiers = append(modifiers, "command down")
		case "ctrl", "control":
			modifiers = append(modifiers, "control down")
		case "alt", "option":
			modifiers = append(modifiers, "option down")
		case "shift":
			modifiers = append(modifiers, "shift down")
		case "fn":
			modifiers = append(modifiers, "fn down")
		default:
			return Result{}, fmt.Errorf("unknown modifier: %s", mod)
		}
	}

	key := strings.TrimSpace(parts[len(parts)-1])
	keyLower := strings.ToLower(key)

	keyCodeMap := map[string]int{
		"return": 36, "enter": 76, "tab": 48, "space": 49,
		"delete": 51, "esc": 53, "escape": 53,
		"home": 115, "end": 119,
		"pageup": 116, "page-up": 116, "pagedown": 121, "page-down": 121,
		"arrowleft": 123, "arrow-left": 123, "arrowright": 124, "arrow-right": 124,
		"arrowdown": 125, "arrow-down": 125, "arrowup": 126, "arrow-up": 126,
		"f1": 122, "f2": 120, "f3": 99, "f4": 118,
		"f5": 96, "f6": 97, "f7": 98, "f8": 100,
		"f9": 101, "f10": 109, "f11": 103, "f12": 111,
	}

	modList := ""
	if len(modifiers) > 0 {
		modList = " using {" + strings.Join(modifiers, ", ") + "}"
	}

	var script string
	if code, ok := keyCodeMap[keyLower]; ok {
		script = fmt.Sprintf(`tell application "System Events" to key code %d%s`, code, modList)
	} else {
		// Escape the key for embedding in an AppleScript double-quoted
		// string; applies to the single-char branch too (a literal " would
		// otherwise break the script). Control characters must NOT use the
		// Swift \u{XX} form — osascript rejects it with -2741 (#217); emit
		// them via `character id N` instead.
		if containsControlChar(key) {
			script = fmt.Sprintf(`tell application "System Events" to keystroke (character id %d)%s`, firstControlChar(key), modList)
			return appleScriptResult(ctx, script)
		}
		escaped := appleScriptStringLiteral(key)
		script = fmt.Sprintf(`tell application "System Events" to keystroke "%s"%s`, escaped, modList)
	}

	return appleScriptResult(ctx, script)
}

// appleScriptStringLiteral escapes for AppleScript double-quoted strings.
// AppleScript recognizes \\ \" \n \r \t but NOT Swift's \u{XX} — control
// characters other than those five are not embeddable at all; callers
// route them through `character id N` (#217).
func appleScriptStringLiteral(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r >= 0x20 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// containsControlChar reports whether s has any C0 control character that
// AppleScript literals cannot represent (#217).
func containsControlChar(s string) bool {
	for _, r := range s {
		if r < 0x20 {
			return true
		}
	}
	return false
}

// firstControlChar returns the first C0 control code in s (callers only use
// it after containsControlChar).
func firstControlChar(s string) int {
	for _, r := range s {
		if r < 0x20 {
			return int(r)
		}
	}
	return 0
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

// applescriptQuote escapes a user-supplied string for safe embedding inside
// AppleScript double-quoted strings.
func applescriptQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}
