//go:build darwin

package tool

import (
	"strings"
	"testing"
)

// TestIssue2587_MenuSelectAppParamAddressesNamedProcess pins #2587: the
// schema promises `app` selects whose menu bar menu_select drives
// ("or the app whose menu bar to use (for 'menu_select', default
// frontmost)"), but the darwin script hardcoded `first process whose
// frontmost is true` - a menu_select(app: "Safari") with Chrome
// frontmost silently clicked Chrome's menus and reported OK.
// Pure string assertions on the generated AppleScript (no osascript run).
func TestIssue2587_MenuSelectAppParamAddressesNamedProcess(t *testing.T) {
	// app set -> address the process BY NAME, never frontmost.
	s, err := buildMenuSelectScript(desktopParams{
		Action: "menu_select", Text: "File > Export...", App: "Safari",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `tell (first application process whose name is "Safari")`) {
		t.Fatalf("app= must address the named process, got script:\n%s", s)
	}
	if strings.Contains(s, "frontmost") {
		t.Fatalf("app= set must not fall back to frontmost addressing, got script:\n%s", s)
	}
	// Menu walk itself must be untouched (#819 parent-named chain intact).
	if !strings.Contains(s, `click menu bar item "File"`) {
		t.Fatalf("top-level menu click missing, got script:\n%s", s)
	}
	if !strings.Contains(s, `menu item "Export..." of menu "File"`) {
		t.Fatalf("submenu chain missing, got script:\n%s", s)
	}
}

// Empty app keeps the default frontmost addressing (schema default).
func TestIssue2587_MenuSelectDefaultsToFrontmostWhenAppEmpty(t *testing.T) {
	for _, app := range []string{"", "   "} {
		s, err := buildMenuSelectScript(desktopParams{
			Action: "menu_select", Text: "Edit > Copy", App: app,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(s, "tell (first process whose frontmost is true)") {
			t.Fatalf("empty app (=%q) must keep frontmost default, got script:\n%s", app, s)
		}
	}
}
