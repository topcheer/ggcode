package tool

import (
	"runtime"
	"strings"
	"testing"
)

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

	if runtime.GOOS != "linux" {
		if got := diagnoseShellCompat("timeout 5 sleep 1", "", ""); !strings.Contains(got, "timeout") {
			t.Errorf("sibling proactive pattern (timeout) stopped firing: %q", got)
		}
	}

	if got := diagnoseShellCompat("SORT -V FILE", "", ""); got != "" {
		t.Errorf("mixed-case entry must stay silent, got %q", got)
	}
}
