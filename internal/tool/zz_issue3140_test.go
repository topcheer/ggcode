package tool

import (
	"os"
	"strings"
	"testing"
)

// V1 structural: the darwin tab/window builders carry the cd-only branch
// (#3140 marker in both new_tab and new_window of both implementations).
// Source-level check because the darwin files are build-tagged and
// AppleScript automation needs a GUI session.
func TestIssue3140_DarwinTemplatesContainCdOnlyBranches(t *testing.T) {
	for _, f := range []string{"ghostty_darwin.go", "iterm2_darwin.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Skipf("source not readable: %v", err)
		}
		if got := strings.Count(string(b), "#3140"); got < 2 {
			t.Fatalf("%s: expected >=2 #3140 cd-only markers (tab+window), got %d", f, got)
		}
	}
}
