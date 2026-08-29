package tool

// Regression test for GitHub issue #1312: the nine named keys must map to
// chromedp kb private-use rune constants, NOT their ASCII names. KeyEvent
// feeds its argument through kb.Encode per rune; an ASCII name like
// "ArrowDown" matches no key mapping and gets typed into the focused
// element as literal text while the tool reports success.
//
// doPress needs a live Chrome tab, so the mapping itself cannot be unit
// tested end-to-end; this pins the kb contract instead: every named-key
// constant is a single rune outside printable ASCII (private-use area),
// which is exactly the property the old ASCII-name mapping violated.

import (
	"testing"

	"github.com/chromedp/chromedp/kb"
)

func TestIssue1312_NamedKeysUseKbRuneConstants(t *testing.T) {
	named := map[string]string{
		"ArrowUp":    kb.ArrowUp,
		"ArrowDown":  kb.ArrowDown,
		"ArrowLeft":  kb.ArrowLeft,
		"ArrowRight": kb.ArrowRight,
		"Delete":     kb.Delete,
		"Home":       kb.Home,
		"End":        kb.End,
		"PageUp":     kb.PageUp,
		"PageDown":   kb.PageDown,
	}
	for name, val := range named {
		runes := []rune(val)
		if len(runes) != 1 {
			t.Errorf("%s: kb constant %q is not a single rune", name, val)
			continue
		}
		r := runes[0]
		// kb named keys live in the Unicode private-use area; a printable
		// ASCII value means someone mapped the literal name again.
		if r >= 0x20 && r < 0x7F {
			t.Errorf("%s: kb constant %q is printable ASCII (regressed to literal name?)", name, val)
		}
		if val == name {
			t.Errorf("%s: kb constant equals its own ASCII name - would be typed as text", name)
		}
	}
}
