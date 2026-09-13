package util

// #1834 case 1 regression: ':' is a legal ECMA-48 parameter
// subseparator - colon-separated truecolor SGR (\e[38:2:r:g:bm, the
// kitty/modern-CLI default) matched NOTHING and leaked whole into
// context.

import "testing"

func TestStripANSIColonSGR(t *testing.T) {
	in := "\x1b[38:2:255:0:0mred\x1b[0m plain"
	out := StripANSI(in)
	if out != "red plain" {
		t.Fatalf("colon-SGR must strip: %q", out)
	}
	// Mixed classic + colon forms.
	in2 := "\x1b[1;32mgreen\x1b[38:2:0:0:255mblue\x1b[0m"
	if out2 := StripANSI(in2); out2 != "greenblue" {
		t.Fatalf("mixed forms must strip: %q", out2)
	}
}
