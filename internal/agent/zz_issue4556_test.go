//go:build goolm

package agent

import "strings"
import "testing"

// Issue #4556: the caveat negation check used bare strings.HasSuffix with no
// word boundary, so any word ending in "no"/"not" ("piano", "dynamo",
// "cannot") neutralized the caveat and a hedged declaration was recorded as
// unhedged.
func TestIssue4556NegationWordBoundary(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		decl     string
		expected bool // sdHasCaveatWindowed should find a caveat
	}{
		{
			// "with piano" ends in "no" but "piano" is one word - the
			// "however" caveat must still count.
			name:     "piano-suffix-must-not-negate",
			text:     "the parser works with piano however the e2e suite is red",
			decl:     "the parser works",
			expected: true,
		},
		{
			// "cannot" ends in "not" but is a single word.
			name:     "cannot-suffix-must-not-negate",
			text:     "i cannot however confirm the build passes",
			decl:     "i cannot",
			expected: true,
		},
		{
			// Genuine negation still neutralizes the caveat: "no" directly
			// before the caveat word "remaining" is the documented
			// affirmation shape ("no remaining issues").
			name:     "true-negation-still-negates",
			text:     "all green no remaining work",
			decl:     "all green",
			expected: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			declIdx := strings.Index(tc.text, tc.decl)
			if declIdx < 0 {
				t.Fatalf("decl phrase %q not found in text", tc.decl)
			}
			got := sdHasCaveatWindowed(tc.text, declIdx, declIdx+len(tc.decl))
			if got != tc.expected {
				t.Errorf("sdHasCaveatWindowed = %v, want %v", got, tc.expected)
			}
		})
	}
}
