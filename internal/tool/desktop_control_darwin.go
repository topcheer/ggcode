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
	case "right_click":
		return mouseClick(ctx, p.X, p.Y, "right", 1)
	case "move":
		return mouseMove(ctx, p.X, p.Y)
	case "drag":
		return mouseDrag(ctx, p.X, p.Y, p.ToX, p.ToY)
	case "scroll":
		return mouseScroll(ctx, p.X, p.Y, p.Direction, p.Amount)

	// ── Keyboard ──
	case "type":
		return typeText(ctx, p.Text)
	case "key_press", "key_combo":
		return keyComboResult(ctx, p.Text)

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
	}
	return runSwiftCGEvent(ctx, fmt.Sprintf(`
import CoreGraphics
let point = CGPoint(x: %d, y: %d)
for _ in 0..<%d {
    let eDown = CGEvent(mouseEventSource: nil, mouseType: CGEventType%s,
                        mouseCursorPosition: point, mouseButton: .left)
    eDown?.post(tap: .cghidEventTap)
    let eUp = CGEvent(mouseEventSource: nil, mouseType: CGEventType%s,
                      mouseCursorPosition: point, mouseButton: .left)
    eUp?.post(tap: .cghidEventTap)
}
`, x, y, count, downType, upType))
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

// mouseDrag drags from (x,y) to (toX,toY) via Swift CGEvent.
func mouseDrag(ctx context.Context, x, y, toX, toY int) (Result, error) {
	return runSwiftCGEvent(ctx, fmt.Sprintf(`
import CoreGraphics
let start = CGPoint(x: %d, y: %d)
let end = CGPoint(x: %d, y: %d)
let eDown = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown,
                    mouseCursorPosition: start, mouseButton: .left)
eDown?.post(tap: .cghidEventTap)
let eDrag = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDragged,
                     mouseCursorPosition: end, mouseButton: .left)
eDrag?.post(tap: .cghidEventTap)
let eUp = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp,
                  mouseCursorPosition: end, mouseButton: .left)
eUp?.post(tap: .cghidEventTap)
`, x, y, toX, toY))
}

// mouseScroll scrolls at (x,y) in the given direction via Swift CGEvent.
func mouseScroll(ctx context.Context, x, y int, direction string, amount int) (Result, error) {
	yDelta := amount
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
func typeText(ctx context.Context, text string) (Result, error) {
	// Build a Swift script that types each character via CGEvent.
	// CGEventCreateKeyboardEvent with keyCode 0 + unicode payload works
	// for arbitrary Unicode characters without needing key code mapping.
	escaped := strings.ReplaceAll(text, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
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
`, escaped))
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
      var label = name || desc || "";

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
      var label = (name + " " + desc).toLowerCase();

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
          label: name || desc || "",
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
				matches[0].Label, int(m.Frame.X), int(m.Frame.Y), m.Frame.W, m.Frame.H, cx, cy)}, nil
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
		// otherwise break the script).
		escaped := strings.ReplaceAll(key, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		script = fmt.Sprintf(`tell application "System Events" to keystroke "%s"%s`, escaped, modList)
	}

	return appleScriptResult(ctx, script)
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
