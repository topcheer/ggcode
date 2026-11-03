package tool

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4684_BudgetExclusionWired(t *testing.T) {
	data, err := os.ReadFile("code_index_manager.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	checks := []struct {
		name, want string
	}{
		{"persisted field", "type persistedExcluded struct {"},
		{"build records overflow (fresh path)", "overflow[relPath] = mtime"},
		{"publish resets set", "m.budgetExcluded = overflow"},
		{"persist writes set", "pi.Excluded = make([]persistedExcluded, 0, len(excluded))"},
		{"scan treats excluded as known", "if !isExcluded && (!wasIndexed || cachedMtime != mtime)"},
		{"rebuildDirty filters before ReadFile", "if _, ex := m.budgetExcluded[k]; ex {"},
		{"load restores set (local assembly, #4735)", "excluded[e.Path] = e.Mtime"},
		{"load publishes under lock (#4735)", "m.budgetExcluded = excluded"},
	}
	for _, c := range checks {
		if !strings.Contains(text, c.want) {
			t.Fatalf("%s missing: %q (#4684)", c.name, c.want)
		}
	}

	if got := strings.Count(text, "overflow[relPath] = mtime"); got != 2 {
		t.Fatalf("both budget-break sites must record overflow, found %d (#4684)", got)
	}
}
