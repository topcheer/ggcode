package markdown

import "testing"

func Test1644Case7_TabClosingSequenceStrips(t *testing.T) {
	cases := map[string]string{
		"## heading\t#":   "## heading",
		"## heading #":    "## heading",
		"## heading\t###": "## heading",
		"## C#":           "## C#",
		"### ###":         "### ###",
		"## plain":        "## plain",
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
