//go:build darwin

package tool

import (
	"strings"
	"testing"
)

// #2593: the three broadcast scripts must target the real iTerm2 menu tree:
// items live in the "Broadcast Input" SUBMENU of Shell with the actual nib
// titles. The old titles appear 0 times in MainMenu.nib (iTerm2 3.7.x).
// buildBroadcastScript lives in iterm2_darwin.go, hence the darwin tag.
func TestIssue2593_BroadcastScriptsTargetRealMenuTree(t *testing.T) {
	subs := map[string]string{
		"toggle": `click menu item "Toggle Broadcast Input to Current Session" of menu "Broadcast Input" of menu bar item "Shell"`,
		"on":     `click menu item "Broadcast Input to All Panes in Current Tab" of menu "Broadcast Input" of menu bar item "Shell"`,
		"off":    `click menu item "Send Input to Current Session Only" of menu "Broadcast Input" of menu bar item "Shell"`,
	}
	for sub, frag := range subs {
		src, ok := buildBroadcastScript(sub)
		if !ok {
			t.Fatalf("broadcast %q must be a valid sub-action", sub)
		}
		if !strings.Contains(src, frag) {
			t.Errorf("broadcast %s must click the real nib title via the Broadcast Input submenu:\nwant fragment: %s\ngot: %s", sub, frag, src)
		}
	}
	if _, ok := buildBroadcastScript("bogus"); ok {
		t.Error("invalid sub-action must be rejected")
	}
	for sub := range subs {
		src, _ := buildBroadcastScript(sub)
		for _, dead := range []string{"Toggle Broadcasting to Current Split Pane", "Stop Broadcasting Input"} {
			if strings.Contains(src, dead) {
				t.Errorf("broadcast %s still references dead nib-absent title %q", sub, dead)
			}
		}
		if strings.Contains(src, `menu item "Broadcast Input" of menu "Shell"`) {
			t.Errorf("broadcast %s must not click the submenu PARENT as a direct Shell child", sub)
		}
	}
}
