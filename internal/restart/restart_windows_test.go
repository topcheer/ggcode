//go:build windows

package restart

import (
	"strings"
	"testing"
)

func TestWinEscape(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`C:\path\binary.exe`, `"C:\path\binary.exe"`},
		{`say "hi"`, `"say ""hi"""`},
		{`100%`, `"100%%"`},
		{``, `""`},
	}
	for _, tc := range cases {
		if got := winEscape(tc.in); got != tc.want {
			t.Errorf("winEscape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	for _, arg := range []string{`--prompt "say \"hi\""`, `a"b`, `"`} {
		esc := winEscape(arg)
		if n := strings.Count(esc, `"`); n%2 != 0 {
			t.Errorf("unbalanced quotes for %q: %q", arg, esc)
		}
	}
}
