//go:build darwin

package tool

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

		{"\U0001F600 grin", "\U0001F600 grin"},

		{"a\x01b", "a\\u0001b"},
	}
	for _, c := range cases {
		if got := jxaStringLiteral(c.in); got != c.want {
			t.Errorf("jxaStringLiteral(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	for _, in := range []string{"\U0001F600", "héllo", "设置"} {
		out := jxaStringLiteral(in)
		for i := 0; i+1 < len(out); i++ {
			if out[i] == '\\' && out[i+1] == 'U' {
				t.Errorf("output for %q contains Go-only \\U escape: %q", in, out)
			}
		}
	}
}
