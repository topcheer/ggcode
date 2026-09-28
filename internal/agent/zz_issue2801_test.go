package agent

import (
	"strings"
	"testing"
)

// zz_issue2801_test.go guards against the constraint_violation dead-pattern
// recurrence (#2801): "leave the config files alone" extracted pattern
// "config files" (prefix-only strip left the " files" suffix), and a pattern
// containing a space never matches any path segment - the whole avoid
// constraint went silent.

func TestIssue2801LeaveAloneFilesSuffix(t *testing.T) {
	cs := cvExtractConstraints("I'll leave the config files alone and only fix the auth bug.", 0)
	found := false
	for _, c := range cs {
		if c.constraintT == "avoid" {
			if c.pattern == "config" {
				found = true
			}
			if strings.Contains(c.pattern, " ") {
				t.Errorf("avoid pattern %q contains a space - dead pattern (#2801 recurrence)", c.pattern)
			}
		}
	}
	if !found {
		t.Error(`no avoid constraint with pattern "config" extracted from "leave the config files alone"`)
	}
}

func TestIssue2801LeaveAloneSingular(t *testing.T) {
	cs := cvExtractConstraints("Leaving the config file alone for now.", 0)
	found := false
	for _, c := range cs {
		if c.constraintT == "avoid" && c.pattern == "config" {
			found = true
		}
	}
	if !found {
		t.Error(`no avoid constraint with pattern "config" extracted from "leaving the config file alone"`)
	}
}

func TestIssue2801QuotedFilesSuffix(t *testing.T) {
	if got := cvExtractPathAfter(`"config files"`, 0); got != "config" {
		t.Errorf(`cvExtractPathAfter("\"config files\"") = %q, want "config"`, got)
	}
}

func TestIssue2801NormalizeTarget(t *testing.T) {
	cases := []struct{ in, want string }{
		{"config files", "config"},
		{"config file", "config"},
		{"config", "config"},
		{"", ""},
		{"internal/agent files", "internal/agent"},
	}
	for _, tc := range cases {
		if got := cvNormalizeTarget(tc.in); got != tc.want {
			t.Errorf("cvNormalizeTarget(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIssue2801NormalizedPatternMatchesRealEdits(t *testing.T) {
	// The normalized pattern must actually catch edits under the directory -
	// this is the protection that was silently lost with the dead pattern.
	if !cvPathMatchesPattern("internal/config/config.go", "config") {
		t.Error(`normalized pattern "config" no longer matches internal/config/config.go`)
	}
	// Root-cause demonstration: the pre-fix dead pattern with the space
	// suffix indeed never matched (why the fix normalizes it away).
	if cvPathMatchesPattern("internal/config/config.go", "config files") {
		t.Error(`dead pattern "config files" unexpectedly matches (matcher semantics changed?)`)
	}
}
