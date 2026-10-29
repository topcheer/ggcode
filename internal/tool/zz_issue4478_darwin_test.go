//go:build darwin

package tool

// Regression test for GitHub issue #4478 (Bug 1): the JXA find_element
// script embedded the search text via Go %q, which emits Go-only \U escapes
// for astral runes (emoji) - JavaScript/JXA rejects those with a
// SyntaxError, so any UI label containing an emoji made every
// find_element/find_and_click/wait_and_click probe fail. jxaStringLiteral
// must escape JS metacharacters, JS-escape control runes, and pass every
// other rune (BMP or astral) through as raw UTF-8.

import (
	"testing"
)

func TestIssue4478_JXAStringLiteral(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"a\"b", "a\\\"b"},
		{"a\\b", "a\\\\b"},
		{"a\nb", "a\\nb"},
		{"a\tb", "a\\tb"},
		// The #4478 repro: %q would emit "\U0001f600" here, which JXA
		// cannot parse. The emoji must survive as raw UTF-8.
		{"\U0001F600 grin", "\U0001F600 grin"},
		// Control character uses the JS \uXXXX escape form.
		{"a\x01b", "a\\u0001b"},
	}
	for _, c := range cases {
		if got := jxaStringLiteral(c.in); got != c.want {
			t.Errorf("jxaStringLiteral(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Sanity: no Go-only \U escape may appear in the escaped output.
	for _, in := range []string{"\U0001F600", "héllo", "设置"} {
		out := jxaStringLiteral(in)
		for i := 0; i+1 < len(out); i++ {
			if out[i] == '\\' && out[i+1] == 'U' {
				t.Errorf("output for %q contains Go-only \\U escape: %q", in, out)
			}
		}
	}
}
