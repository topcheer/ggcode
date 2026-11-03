package tool

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

		if r >= 0x20 && r < 0x7F {
			t.Errorf("%s: kb constant %q is printable ASCII (regressed to literal name?)", name, val)
		}
		if val == name {
			t.Errorf("%s: kb constant equals its own ASCII name - would be typed as text", name)
		}
	}
}
