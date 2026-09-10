//go:build darwin

package tool

import (
	"strings"
	"testing"
)

// #1691 case 5: menu lookup must not embed Go %q escapes (\x1b / \u{})
// into the AppleScript literal. escapeAS lives in ghostty_darwin.go, so
// this pin is darwin-only (upstream #1944 shipped it untagged, breaking
// non-darwin test builds).
func TestIterm2MenuScriptHasNoGoEscapes1691(t *testing.T) {
	// Control char path: script must quote via escapeAS, whose output for
	// dropped runes never contains Go escape sequences.
	got := escapeAS("menu\x1bitem")
	if strings.Contains(got, "\\x1b") || strings.Contains(got, "\\u") {
		t.Fatalf("escapeAS must not emit Go escapes: %q", got)
	}
}
