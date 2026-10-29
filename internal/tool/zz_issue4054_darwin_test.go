//go:build darwin

package tool

import (
	"strings"
	"testing"
)

// #4054/#4055 companions (darwin surface): the key code map must carry the
// backspace name (previously missing -> keystroke "backspace" typed 9
// literal letters into the focused control with a fake success) and the
// unambiguous forward-delete names; "delete" keeps its legacy macOS
// backspace mapping for compatibility.
func TestIssue4054KeyCodeMapEntries(t *testing.T) {
	cases := []struct {
		key  string
		want int
	}{
		{"backspace", 51},       // #4054: was missing entirely
		{"delete", 51},          // legacy macOS mapping preserved (#4055)
		{"forward-delete", 117}, // #4055: Fn+Delete, unambiguous forward
		{"forwarddelete", 117},  // #4055: spelling variant
	}
	for _, tc := range cases {
		got, ok := keyCodeMap[tc.key]
		if !ok || got != tc.want {
			t.Errorf("keyCodeMap[%q] = (%d, %v), want (%d, true)", tc.key, got, ok, tc.want)
		}
	}
}

// #4054: an unknown multi-char purely-alphabetic key name must fail loudly
// instead of being keystroked literally. This path returns before any
// osascript execution, so it is safe to invoke directly.
func TestIssue4054UnknownAlphaKeyNameRejected(t *testing.T) {
	for _, key := range []string{"backspac", "whatever", "fifteen"} {
		res, err := keyComboResult(t.Context(), key)
		if err != nil {
			t.Fatalf("keyComboResult(%q) unexpected err: %v", key, err)
		}
		if !res.IsError {
			t.Errorf("keyComboResult(%q) must be IsError (unknown key name typed literally = silent wrong action), got: %s", key, res.Content)
		}
		if !strings.Contains(res.Content, "unknown key name") {
			t.Errorf("error should name the problem and list supported keys, got: %s", res.Content)
		}
	}

	// Digits and mixed strings keep the literal fallback (key_combo tails
	// like cmd+shift+3): they must NOT be rejected here - they proceed to
	// the keystroke branch (osascript), so only assert they are not the
	// alpha-name rejection.
	res, err := keyComboResult(t.Context(), "3")
	if err != nil {
		t.Fatalf("single digit: unexpected err: %v", err)
	}
	if strings.Contains(res.Content, "unknown key name") {
		t.Errorf("single digit must not hit the unknown-key-name rejection, got: %s", res.Content)
	}
}
