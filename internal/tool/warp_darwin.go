//go:build darwin

package tool

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// executeStatus reports whether Warp is detected and basic info.
func (w *WarpTool) executeStatus() Result {
	if !warpAvailable() {
		return Result{Content: "warp: not detected (TERM_PROGRAM != WarpTerminal)"}
	}
	return Result{Content: fmt.Sprintf("warp: detected\nworking dir: %s", w.workingDir())}
}

// clickMenu clicks a menu item in Warp via System Events.
// Warp has full menu structure: File, Edit, View, Tab, Blocks, AI, Drive, Window, Help.
func (w *WarpTool) clickMenu(menuItem string) Result {
	// First activate Warp to ensure menu is accessible
	activate := `tell application "Warp" to activate`
	exec.Command("osascript", "-e", activate).Run()

	// Small delay to ensure Warp is frontmost
	time.Sleep(50 * time.Millisecond)

	// Click the menu item by searching all menu bar items
	// Warp's menus: File, Edit, View, Tab, Blocks, AI, Drive, Window, Help
	script := fmt.Sprintf(`
tell application "System Events"
    tell process "Warp"
        set targetItem to %q
        set menuNames to {"File", "Edit", "View", "Tab", "Blocks", "AI", "Drive", "Window", "Help"}
        repeat with mb in menuNames
            try
                tell menu bar 1
                    tell menu 1 of menu bar item mb
                        set itemCount to count of menu items
                        repeat with i from 1 to itemCount
                            if name of menu item i is targetItem then
                                click menu item i
                                return "ok"
                            end if
                        end repeat
                    end tell
                end tell
            end try
        end repeat
    end tell
end tell
return "not found"`, menuItem)

	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("warp: failed to click menu %q: %v: %s", menuItem, err, strings.TrimSpace(string(out)))}
	}
	result := strings.TrimSpace(string(out))
	if result == "not found" {
		return Result{IsError: true, Content: fmt.Sprintf("warp: menu item %q not found in any menu", menuItem)}
	}
	return Result{Content: fmt.Sprintf("warp: %s", menuItem)}
}

// executeInput sends text to the currently focused Warp pane.
func (w *WarpTool) executeInput(text string) Result {
	if text == "" {
		return Result{IsError: true, Content: "text is required for input action"}
	}
	w.sendKeys(text)
	preview := text
	if len(preview) > 60 {
		preview = preview[:60] + "..."
	}
	return Result{Content: fmt.Sprintf("warp: input sent: %s", preview)}
}

// executeSendKey sends a keyboard event to Warp.
func (w *WarpTool) executeSendKey(key string, modifiers string) Result {
	if key == "" {
		return Result{IsError: true, Content: "key is required for send_key action"}
	}

	keyMap := map[string]string{
		"enter": "36", "return": "36",
		"escape": "53", "esc": "53",
		"tab":       "48",
		"space":     "49",
		"backspace": "51", "delete": "51",
		"up": "126", "down": "125", "left": "123", "right": "124",
		"home": "115", "end": "119",
		"pageup": "116", "pagedown": "121",
		"f1": "122", "f2": "120", "f3": "99", "f4": "118",
		"f5": "96", "f6": "97", "f7": "98", "f8": "100",
		"f9": "101", "f10": "109", "f11": "103", "f12": "111",
	}

	keyCode, ok := keyMap[strings.ToLower(key)]
	if !ok {
		// Single character — use keystroke
		w.sendKeystroke(key, modifiers)
		return Result{Content: fmt.Sprintf("warp: key sent: %s (%s)", key, formatMods(modifiers))}
	}

	// Use key code for special keys
	w.sendKeyCode(keyCode, modifiers)
	return Result{Content: fmt.Sprintf("warp: key sent: %s (%s)", key, formatMods(modifiers))}
}

// sendKeys types text into Warp using System Events keystroke.
func (w *WarpTool) sendKeys(text string) {
	// Activate Warp first
	exec.Command("osascript", "-e", `tell application "Warp" to activate`).Run()
	time.Sleep(30 * time.Millisecond)

	// Escape double quotes and backslashes for AppleScript string
	escaped := strings.ReplaceAll(text, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")

	script := fmt.Sprintf(`tell application "System Events" to keystroke "%s"`, escaped)
	exec.Command("osascript", "-e", script).Run()
}

// sendKeystroke sends a single character keystroke with optional modifiers.
func (w *WarpTool) sendKeystroke(key string, modifiers string) {
	exec.Command("osascript", "-e", `tell application "Warp" to activate`).Run()
	time.Sleep(30 * time.Millisecond)

	mods := parseModifiers(modifiers)
	script := fmt.Sprintf(`tell application "System Events" to keystroke %q%s`, key, mods)
	exec.Command("osascript", "-e", script).Run()
}

// sendKeyCode sends a key code with optional modifiers.
func (w *WarpTool) sendKeyCode(code string, modifiers string) {
	exec.Command("osascript", "-e", `tell application "Warp" to activate`).Run()
	time.Sleep(30 * time.Millisecond)

	mods := parseModifiers(modifiers)
	script := fmt.Sprintf(`tell application "System Events" to key code %s%s`, code, mods)
	exec.Command("osascript", "-e", script).Run()
}

// parseModifiers converts "control,shift" to " using {control down, shift down}"
func parseModifiers(mods string) string {
	if mods == "" {
		return ""
	}
	parts := strings.Split(mods, ",")
	var valid []string
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		switch p {
		case "shift", "control", "option", "command":
			valid = append(valid, p+" down")
		}
	}
	if len(valid) == 0 {
		return ""
	}
	return " using {" + strings.Join(valid, ", ") + "}"
}

func formatMods(mods string) string {
	if mods == "" {
		return "none"
	}
	return mods
}
