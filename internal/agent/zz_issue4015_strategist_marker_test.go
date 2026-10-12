package agent

import "testing"

// #4015: the completion-marker strip used a byte offset taken from a
// strings.ToUpper() copy to slice the ORIGINAL summary. Any rune whose
// uppercase form changes byte length before the marker shifts the cut
// (ﬁ U+FB01 3B→FI 2B shrinks, ǰ U+01F0 2B→J̌ 3B grows), corrupting the
// visible "[Strategist: goal achieved ...]" summary. The fix matches
// case-insensitively on the original string via indexFoldASCII.
func TestStripStrategistCompleteMarkerByteOffset(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", strategistCompleteMarker + " all tests pass", "all tests pass"},
		{"absent unchanged", "no marker here at all", "no marker here at all"},
		{"lowercase marker still matches", "<goal_achieved/> done", "done"},
		// ﬁ shrinks 3B→2B under ToUpper: the old ToUpper-copy offset cut one
		// byte too EARLY, leaking a marker fragment into the summary.
		{"shrink ligature before marker", "ﬁnal check " + strategistCompleteMarker + " shipped", "shipped"},
		// ǰ grows 2B→3B under ToUpper: the old offset cut too LATE, dropping
		// the first bytes of the real summary.
		{"grow caron before marker", "ǰust verifying " + strategistCompleteMarker + " clean tail", "clean tail"},
		// Multiple shifters plus mixed-case marker text.
		{"mixed shifters", "ﬁx ǰ " + strategistCompleteMarker + " exact tail", "exact tail"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripStrategistCompleteMarker(tc.in); got != tc.want {
				t.Errorf("stripStrategistCompleteMarker(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Regression lock on the old failure mode: with the ToUpper-copy offset the
// ﬁ case returned a corrupted tail (marker fragment / missing head bytes).
// This test documents that the original text after the marker survives
// byte-exact, including case and multi-byte runes.
func TestStripStrategistCompleteMarkerPreservesOriginalBytes(t *testing.T) {
	summary := "ﬁnalyse: Straße ǰ " + strategistCompleteMarker + " Ärgerfrei ✓"
	want := "Ärgerfrei ✓"
	if got := stripStrategistCompleteMarker(summary); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
