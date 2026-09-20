package tool

import (
	"strings"
	"testing"
)

// #2607: the sort -V pattern was dead on both arms (proactive arm compared
// capital "-V" tokens against the lowercased command; reactive arm expected
// GNU-style error text real macOS/BSD never emits) while current macOS
// natively supports sort -V. Removed. Pin: through the REAL production
// entry (diagnoseShellCompat), sort -V invocations no longer fire any
// advisory, and the removal did not disturb sibling patterns.
func TestIssue2607_SortVPatternRemoved(t *testing.T) {
	for _, cmd := range []string{
		"sort -V versions.txt",
		"git tag | sort -V",
		"sort --version-sort foo",
	} {
		if got := diagnoseShellCompat(cmd, "", ""); got != "" {
			t.Errorf("diagnoseShellCompat(%q) = %q, want \"\" (pattern removed)", cmd, got)
		}
	}
	// Sibling patterns still work through the same entry (removal did not
	// break the loop): GNU readlink on a mac-style error, proactive timeout.
	if got := diagnoseShellCompat("timeout 5 sleep 1", "", ""); !strings.Contains(got, "timeout") {
		t.Errorf("sibling proactive pattern (timeout) stopped firing: %q", got)
	}
	// The lowercase-command convention holds: mixed-case real-world spelling
	// also stays silent.
	if got := diagnoseShellCompat("SORT -V FILE", "", ""); got != "" {
		t.Errorf("mixed-case entry must stay silent, got %q", got)
	}
}
