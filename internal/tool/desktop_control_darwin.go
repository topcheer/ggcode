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
		return cliclickResult(ctx, fmt.Sprintf("c:%d,%d", p.X, p.Y))
	case "double_click":
		return cliclickResult(ctx, fmt.Sprintf("dc:%d,%d", p.X, p.Y))
	case "right_click":
		return cliclickResult(ctx, fmt.Sprintf("rc:%d,%d", p.X, p.Y))
	case "move":
		return cliclickResult(ctx, fmt.Sprintf("m:%d,%d", p.X, p.Y))
	case "drag":
		// dd: mouse down at start, dm: move to target, du: mouse up at target
		return cliclickResult(ctx, fmt.Sprintf("dd:%d,%d dm:%d,%d du:%d,%d", p.X, p.Y, p.ToX, p.ToY, p.ToX, p.ToY))
	case "scroll":
		// cliclick uses negative for down, positive for up
		amt := p.Amount
		if p.Direction == "down" {
			amt = -amt
		}
		return cliclickResult(ctx, fmt.Sprintf("scroll:%d,%d,%d", p.X, p.Y, amt))

	// ── Keyboard ──
	case "type":
		// Escape double quotes in the text for cliclick
		escaped := strings.ReplaceAll(p.Text, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		return cliclickResult(ctx, fmt.Sprintf("t:\"%s\"", escaped))
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

// snapshotUI returns the accessibility tree of the frontmost application
// as a JSON array of elements with role, label, frame (x,y,w,h), and enabled.
// Uses AppleScript + System Events to traverse the accessibility tree.
func snapshotUI(ctx context.Context, maxDepth int) (Result, error) {
	if maxDepth <= 0 {
		maxDepth = 8
	}
	// Build a JXA (JavaScript for Automation) script that walks the
	// accessibility tree and returns JSON. JXA is more practical for
	// recursive tree traversal than AppleScript.
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

      // Get position and size
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

      // Only include elements with meaningful data
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

      // Recurse into children
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
// This is a composite action that reduces LLM coordinate reasoning.
func findAndClick(ctx context.Context, searchText string, maxDepth int) (Result, error) {
	findRes, err := findElement(ctx, searchText, maxDepth)
	if err != nil {
		return Result{}, fmt.Errorf("find_element failed: %w", err)
	}

	// Parse the JSON to extract the first match's frame center
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

	// Find first match with a valid frame
	for _, m := range matches {
		if m.Frame != nil && m.Frame.W > 0 && m.Frame.H > 0 {
			cx := int(m.Frame.X + m.Frame.W/2)
			cy := int(m.Frame.Y + m.Frame.H/2)
			clickRes, err := cliclickResult(ctx, fmt.Sprintf("c:%d,%d", cx, cy))
			if err != nil {
				return Result{}, fmt.Errorf("click at (%d,%d) failed: %w", cx, cy, err)
			}
			_ = clickRes
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

// displayInfo returns logical and physical display dimensions and the
// scale factor. This is critical for Retina/HiDPI: screenshots are in
// physical pixels but cliclick coordinates are in logical points.
// Example: a 2x Retina display has logical 1728x1117 but physical 3456x2234.
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

// cliclickResult runs a cliclick command. cliclick is a macOS utility
// for sending mouse/keyboard events. If not installed, it tries to
// install via Homebrew.
func cliclickResult(ctx context.Context, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, "cliclick", args...)
	out, err := cmd.Output()
	if err != nil {
		// Check if cliclick is missing
		if isCommandNotFound(err) {
			// Try to install via brew
			installOut, installErr := exec.CommandContext(ctx, "brew", "install", "cliclick").CombinedOutput()
			if installErr != nil {
				return Result{}, fmt.Errorf("cliclick is not installed and brew install failed: %w\n%s", installErr, string(installOut))
			}
			// Retry
			out, err = exec.CommandContext(ctx, "cliclick", args...).Output()
			if err != nil {
				return Result{}, fmt.Errorf("cliclick failed after install: %w", err)
			}
		} else {
			return Result{}, fmt.Errorf("cliclick failed: %w", err)
		}
	}
	result := fmt.Sprintf("OK: cliclick %s", strings.Join(args, " "))
	if len(out) > 0 {
		result += "\n" + strings.TrimSpace(string(out))
	}
	return Result{Content: result}, nil
}

func keyComboResult(ctx context.Context, combo string) (Result, error) {
	parts := strings.Split(combo, "+")
	if len(parts) == 0 {
		return Result{}, fmt.Errorf("empty key combo")
	}

	// Parse modifiers (all parts except the last)
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
	} else if len(key) == 1 {
		script = fmt.Sprintf(`tell application "System Events" to keystroke "%s"%s`, key, modList)
	} else {
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

func isCommandNotFound(err error) bool {
	if exitErr, ok := err.(*exec.ExitError); ok {
		// On macOS, command not found gives exit status 127
		return exitErr.ExitCode() == 127
	}
	return strings.Contains(err.Error(), "executable file not found") ||
		strings.Contains(err.Error(), "no such file or directory")
}

// Ensure desktopControlResult is used (placeholder for JSON marshaling consistency)
var _ = json.Marshal

// applescriptQuote escapes a user-supplied string for safe embedding inside
// AppleScript double-quoted strings. It wraps the value in escaped quotes.
func applescriptQuote(s string) string {
	// Escape backslashes first, then double quotes
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}
