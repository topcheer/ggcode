package tool

import (
	"regexp"
	"testing"
)

// TestBatchReplaceInvalidGroupRefs verifies #385: regex replacements
// referencing capture groups beyond the pattern's group count are rejected
// up front instead of silently expanding to empty strings.
func TestBatchReplaceInvalidGroupRefs(t *testing.T) {
	cases := []struct {
		name        string
		pattern     string
		replacement string
		wantBad     bool
	}{
		{"out of range $5", `USD`, "$" + "5", true},
		{"braced out of range", `USD`, "${" + "9}", true},
		{"valid $1", `(US)D`, "$" + "1", false},
		{"literal dollar escaped", `USD`, "$$ok", false},
		{"no refs", `USD`, "EUR", false},
		{"exact boundary group", `(a)(b)`, "$" + "2", false},
		{"named group", `(?P<x>a)`, "$x", false},
		{"unknown named group", `(?P<x>a)`, "$y", true},
	}
	for _, tc := range cases {
		re := regexp.MustCompile(tc.pattern)
		got := invalidGroupRefs(re, tc.replacement)
		if tc.wantBad && got == "" {
			t.Errorf("%s: expected rejection, got none", tc.name)
		}
		if !tc.wantBad && got != "" {
			t.Errorf("%s: unexpected rejection %q", tc.name, got)
		}
	}
}
