package markdown

// #1644-7: a tab-delimited ATX closing sequence ('## heading\t#') must
// strip just like a space-delimited one; '## C#' (word char before #)
// keeps its '#'; all-# heading text stays as-is.

import "testing"

func Test1644Case7_TabClosingSequenceStrips(t *testing.T) {
	cases := map[string]string{
		"## heading\t#":   "heading", // tab closer - the #1644-7 gap
		"## heading #":    "heading", // space closer - #1588-C behavior kept
		"## heading\t###": "heading", // multi-# tab closer
		"## C#":           "C#",      // word char before # - keep
		"### ###":         "###",     // all-# heading text - keep as-is
		"## plain":        "plain",   // no closer - unchanged
	}
	for in, want := range cases {
		got, ok := normalizeHeading(in)
		if !ok {
			t.Errorf("normalizeHeading(%q) not a heading", in)
			continue
		}
		if got != want {
			t.Errorf("normalizeHeading(%q) = %q, want %q", in, got, want)
		}
	}
}
