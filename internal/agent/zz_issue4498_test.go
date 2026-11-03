//go:build goolm

package agent

import (
	"strings"
	"testing"
)

func TestIssue4498_LeaveAloneExtraction(t *testing.T) {

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
