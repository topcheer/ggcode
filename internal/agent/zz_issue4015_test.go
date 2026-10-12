package agent

import "testing"

// #4015: the completion-marker strip used a strings.ToUpper copy's byte
// offset to slice the ORIGINAL summary. Case mappings that grow in bytes
// (ß U+00DF -> SS, ﬁ U+FB01 -> FI) shift the marker's offset in the copy,
// so the slice cut into the original landed mid-rune/late and corrupted
// the displayed completion summary. stripStrategistCompleteMarker folds
// in place (indexFoldASCII) so offsets are valid on the original string,
// and the original casing is preserved.
func TestIssue4015StripMarkerOffsetStaysOnOriginalString(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			// ß before the marker grows +1 byte in the ToUpper copy: the old
			// code sliced one byte late into a 3-byte CJK rune.
			name: "eszett before marker",
			in:   "Straße fertig. <GOAL_ACHIEVED/> 登录页修复完成",
			want: "登录页修复完成",
		},
		{
			// ﬁ ligature also grows +1 byte under ToUpper.
			name: "fi ligature before marker",
			in:   "ﬁnal check done <GOAL_ACHIEVED/> all tests pass",
			want: "all tests pass",
		},
		{
			// Markdown-wrapped marker, several length-changing chars before it.
			name: "multiple growing chars",
			in:   "ﬂow: Straße ﬁx `✓` <GOAL_ACHIEVED/> done",
			want: "done",
		},
		{
			// Case-insensitive detection is preserved from the old
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
			// No marker: pass through unchanged (autoupdate path where the
			// strategist talks about the tag without emitting it).
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
