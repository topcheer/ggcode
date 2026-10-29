//go:build goolm

package agent

import (
	"strings"
	"testing"
)

// #4498: the "leave ... alone" scope pattern used [^n] (excluding the
// LETTER n, not the newline) with a 5-char gap - "leave main alone" (gap
// contains 'n') and "leave the config alone" (12-char gap) both failed to
// extract. The char class is now [^\n.] with a 20-char gap, matching the
// other three patterns in the set.
func TestIssue4498_LeaveAloneExtraction(t *testing.T) {
	// With trailing context, as the other three pattern families require
	// (the bare sentence-final form is a shared tail-`{3,60}` trait of all
	// four patterns, not this fix's scope). Pre-fix these failed: the gap
	// contained the LETTER 'n' or exceeded 5 chars.
	cases := []string{
		"leave main alone during the sweep",
		"leave the config alone when refactoring",
		"Please leave my tests alone until Friday",
	}
	for _, text := range cases {
		results := extractConstraints(text)
		found := false
		for _, r := range results {
			if strings.Contains(strings.ToLower(r), "leave") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("constraint not extracted from %q: %v", text, results)
		}
	}
}
