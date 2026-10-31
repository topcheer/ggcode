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
	// like cmd+shift+3): they must NOT be rejected here. #4750: asserting
	// this via keyComboResult("3") actually EXECUTES the keystroke branch -
	// a real osascript `keystroke "3"` that types "3" into whatever app is
	// frontmost (observed: the developer's terminal running `go test`).
	// Assert the digit contract through the pure gate instead: the literal
	// fallback is reached exactly when the alpha-name rejection does not
	// fire, so unknownAlphaKeyName("3") == false IS the contract, with zero
	// side effects.
	if unknownAlphaKeyName("3") {
		t.Error(`unknownAlphaKeyName("3") must be false: single digits keep the literal keystroke fallback, not the alpha-name rejection`)
	}
	// Sanity: the gate that the first loop exercises still rejects the
	// alpha names (guards against a regression that makes the gate inert,
	// which would silently pass the digit assertion above).
	if !unknownAlphaKeyName("backspac") {
		t.Error(`unknownAlphaKeyName("backspac") must be true (alpha-name gate inert = contract untested)`)
	}
}
