//go:build windows

package restart

import (
	"strings"
	"testing"
)

// #1310: the old escape used `\"` - cmd.exe treats backslash as a literal
// character, so the escaped quote CLOSED the quoted region and shifted the
// whole command line whenever an arg contained a double quote.
func TestWinEscape(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`C:\path\binary.exe`, `"C:\path\binary.exe"`}, // backslashes untouched
		{`say "hi"`, `"say ""hi"""`},                   // quotes doubled, state balanced
		{`100%`, `"100%%"`},                            // % doubled for batch parse
		{``, `""`},
	}
	for _, tc := range cases {
		if got := winEscape(tc.in); got != tc.want {
			t.Errorf("winEscape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// The quoting state of a generated arg line must stay balanced: every
	// embedded quote contributes two, plus the wrapping pair.
	for _, arg := range []string{`--prompt "say \"hi\""`, `a"b`, `"`} {
		esc := winEscape(arg)
		if n := strings.Count(esc, `"`); n%2 != 0 {
			t.Errorf("unbalanced quotes for %q: %q", arg, esc)
		}
	}
}
