//go:build darwin

package tool

import (
	"strings"
	"testing"
)

func TestIssue4054KeyCodeMapEntries(t *testing.T) {
	cases := []struct {
		key  string
		want int
	}{
		{"backspace", 51},
		{"delete", 51},
		{"forward-delete", 117},
		{"forwarddelete", 117},
	}
	for _, tc := range cases {
		got, ok := keyCodeMap[tc.key]
		if !ok || got != tc.want {
			t.Errorf("keyCodeMap[%q] = (%d, %v), want (%d, true)", tc.key, got, ok, tc.want)
		}
	}
}

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

	if unknownAlphaKeyName("3") {
		t.Error(`unknownAlphaKeyName("3") must be false: single digits keep the literal keystroke fallback, not the alpha-name rejection`)
	}

	if !unknownAlphaKeyName("backspac") {
		t.Error(`unknownAlphaKeyName("backspac") must be true (alpha-name gate inert = contract untested)`)
	}
}
