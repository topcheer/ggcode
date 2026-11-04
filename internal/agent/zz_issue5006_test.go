package agent

import "testing"

// #5006: cvExtractPathAfter's directory-suffix fallback loop was dead code -
// the end-scan truncates rest[:end] at the first whitespace, so suffixes
// with a leading space could never HasSuffix-match. "only modify the
// X module/directory/..." constraints were silently dropped.
func TestIssue5006SuffixFallbackRecoversNonKeywordModule(t *testing.T) {
	cases := []struct {
		text   string
		offset int
		want   string
	}{
		// The reported false negative: non-keyword name + directory suffix.
		{text: "only modify the billing module", offset: len("only modify "), want: "billing"},
		{text: "only touch the payments folder", offset: len("only touch "), want: "payments"},
		// Trailing punctuation no longer defeats the suffix match.
		{text: "only modify the billing module.", offset: len("only modify "), want: "billing"},
		// Keyword path stays on the moduleKeywords route (unchanged).
		{text: "only modify the config directory", offset: len("only modify "), want: "config"},
		// Slash path unchanged.
		{text: "only modify internal/agent files", offset: len("only modify "), want: "internal/agent"},
		// No suffix -> still no constraint (unchanged).
		{text: "only modify the billing thing", offset: len("only modify "), want: ""},
		// Multi-word name guard: "billing module today" must not be extracted.
		{text: "only modify the billing module today", offset: len("only modify "), want: ""},
	}
	for _, tc := range cases {
		if got := cvExtractPathAfter(tc.text, tc.offset); got != tc.want {
			t.Fatalf("cvExtractPathAfter(%q): got %q want %q", tc.text, got, tc.want)
		}
	}
}
