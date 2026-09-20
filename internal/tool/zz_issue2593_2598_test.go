//go:build darwin || linux

package tool

import (
	"strings"
	"testing"
)

// #2593: the three broadcast scripts must target the real iTerm2 menu tree:
// items live in the "Broadcast Input" SUBMENU of Shell with the actual nib
// titles. The old titles appear 0 times in MainMenu.nib (iTerm2 3.7.x).
func TestIssue2593_BroadcastScriptsTargetRealMenuTree(t *testing.T) {
	// Each sub-action's script comes from buildBroadcastScript; assert the
	// real-nib fragments (invalid sub-actions return ok=false).
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
	// The dead titles must be gone from every valid script.
	for sub := range subs {
		src, _ := buildBroadcastScript(sub)
		for _, dead := range []string{"Toggle Broadcasting to Current Split Pane", "Stop Broadcasting Input"} {
			if strings.Contains(src, dead) {
				t.Errorf("broadcast %s still references dead nib-absent title %q", sub, dead)
			}
		}
		// Bare "Broadcast Input" as a DIRECT Shell child (the submenu-parent
		// fake-success path) must not appear.
		if strings.Contains(src, `menu item "Broadcast Input" of menu "Shell"`) {
			t.Errorf("broadcast %s must not click the submenu PARENT as a direct Shell child", sub)
		}
	}
}

// #2598 case A: F-keys must resolve to escape sequences (keyboard events),
// not fall through to the regular-character branch that types literal "f5".
func TestIssue2598_FKeysResolveToEscapeSeq(t *testing.T) {
	want := map[string]string{
		"f1": "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS",
		"f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~",
		"f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
	}
	for key, seq := range want {
		got, ok := kittyKeyEscapeSeq(key)
		if !ok || got != seq {
			t.Errorf("kittyKeyEscapeSeq(%q) = %q,%v want %q,true", key, got, ok, seq)
		}
	}
	// Unknown multi-character keys still report false (callers handle).
	if _, ok := kittyKeyEscapeSeq("zoom"); ok {
		t.Error("unknown key must stay unmapped")
	}
}

// #2598 case B: positional text payloads must be separated from options
// with "--" so "-"-prefixed text is sent as text, not parsed as an option.
func TestIssue2598_SendTextArgsCarrySeparator(t *testing.T) {
	args := kittySendTextArgs(0, "--help")
	want := []string{"send-text", "--", "--help"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args = %v, want %v", args, want)
	}
	args = kittySendTextArgs(7, "plain text")
	want = []string{"send-text", "--match=id:7", "--", "plain text"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args with match = %v, want %v", args, want)
	}
}
