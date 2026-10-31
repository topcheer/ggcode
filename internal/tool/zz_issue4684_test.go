package tool

import (
	"os"
	"strings"
	"testing"
)

// #4684: files dropped by the total-terms budget must be remembered in a
// persisted exclusion set - otherwise every 5-minute scan tick re-marked
// them dirty (refreshing lastActivity so the idle release never fired) and
// rebuildDirty re-ReadFile'd each before dropping it again.
func TestIssue4684_BudgetExclusionWired(t *testing.T) {
	data, err := os.ReadFile("code_index_manager.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	checks := []struct {
		name, want string
	}{
		{"persisted field", "Excluded []persistedExcluded"},
		{"build records overflow (fresh path)", "overflow[relPath] = mtime // #4684"},
		{"publish resets set", "m.budgetExcluded = overflow"},
		{"persist writes set", "pi.Excluded = make([]persistedExcluded, 0, len(excluded))"},
		{"scan treats excluded as known", "if !isExcluded && (!wasIndexed || cachedMtime != mtime)"},
		{"rebuildDirty filters before ReadFile", "continue // #4684: budget-dropped file"},
		{"load restores set", "m.budgetExcluded[e.Path] = e.Mtime"},
	}
	for _, c := range checks {
		if !strings.Contains(text, c.want) {
			t.Fatalf("%s missing: %q (#4684)", c.name, c.want)
		}
	}
	// Both budget-break sites must record (cached-hit and fresh-tokenize).
	if got := strings.Count(text, "overflow[relPath] = mtime"); got != 2 {
		t.Fatalf("both budget-break sites must record overflow, found %d (#4684)", got)
	}
}
