package tool

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3140_DarwinTemplatesContainCdOnlyBranches(t *testing.T) {
	for _, f := range []string{"ghostty_darwin.go", "iterm2_darwin.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Skipf("source not readable: %v", err)
		}
		cdOnly := 0
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "cd '%s'") && !strings.Contains(line, "&&") {
				cdOnly++
			}
		}
		if cdOnly < 2 {
			t.Fatalf("%s: expected >=2 cd-only template branches (tab+window, #3140), got %d", f, cdOnly)
		}
	}
}
