package agent

import (
	"testing"
	"unicode/utf8"
)

// #4015: the strategist completion-marker strip must slice the ORIGINAL
// summary, not a strings.ToUpper copy whose byte offsets can drift.
// Empirically corrected trigger family (probe-verified; Go's ToUpper is a
// SIMPLE mapping, no special casing — the issue's ß/ﬁ examples do NOT
// change byte length): grow ɐ U+0250(2B)→Ɐ U+2C6F(3B) +1B; shrink
// ſ U+017F(2B)→S(1B) −1B and ı U+0131(2B)→I(1B) −1B.
// stripStrategistCompleteMarker fold-scans in place (indexFoldASCII), so
// offsets are valid on the original string and casing is preserved.
func TestIssue4015StripMarkerOffsetStaysOnOriginalString(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			// ɐ grows +1B in the ToUpper copy: the old code sliced one byte
			// late and ate the first rune of the remainder.
			name: "growth char ɐ before marker",
			in:   "注意 ɐ done <GOAL_ACHIEVED/> Shipping complete.",
			want: "Shipping complete.",
		},
		{
			// ſ shrinks −1B: the old slice started one byte early, leaving
			// a stale prefix byte.
			name: "shrink char ſ before marker",
			in:   "ļ ſ ok <GOAL_ACHIEVED/> Done, all tests green.",
			want: "Done, all tests green.",
		},
		{
			// ı shrinks −1B (dev seat probe, #4022).
			name: "shrink char ı before marker",
			in:   "ıstanbul check <GOAL_ACHIEVED/> 登录页修复完成",
			want: "登录页修复完成",
		},
		{
			// ß does NOT drift under Go's simple ToUpper (2B→2B) — kept as
			// a no-drift sanity case for the corrected trigger family.
			name: "no-drift char ß still strips correctly",
			in:   "Straße fertig. <GOAL_ACHIEVED/> 登录页修复完成",
			want: "登录页修复完成",
		},
		{
			// Case-insensitive detection preserved from the old
			// ToUpper-based behavior.
			name: "lowercase marker still stripped",
			in:   "done with everything <goal_achieved/> ship it",
			want: "ship it",
		},
		{
			name: "marker at start",
			in:   "<GOAL_ACHIEVED/> shipped",
			want: "shipped",
		},
		{
			// No marker: pass through unchanged (the strategist discusses
			// the tag without emitting it).
			name: "no marker passthrough",
			in:   "do NOT emit the GOAL_ACHIEVED tag yet, more work remains",
			want: "do NOT emit the GOAL_ACHIEVED tag yet, more work remains",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripStrategistCompleteMarker(tc.in); got != tc.want {
				t.Fatalf("stripStrategistCompleteMarker(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The old misaligned slice could land mid-rune and emit invalid UTF-8; the
// fix must never do that.
func TestIssue4015StripMarkerResultIsValidUTF8(t *testing.T) {
	in := "ɐɐɐ <GOAL_ACHIEVED/> 完成了所有目标。"
	got := stripStrategistCompleteMarker(in)
	if got != "完成了所有目标。" {
		t.Errorf("utf8 case: got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("result is not valid UTF-8: %q", got)
	}
}
