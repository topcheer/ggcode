package agent

import (
	"strings"
	"testing"
)

// #418: editing a file after MULTIPLE reads must expire ALL of them, not
// just the last — the old overwrite-style readPaths kept only the final
// index, and if that one was redundant the earlier productive read was
// never reclassified (waste systematically undercounted).
func TestTokenWasteMultiReadAllExpired(t *testing.T) {
	s := newTokenWasteBudgetState()

	// Read a.go productively (1000 productive tokens).
	s.recordToolResult("read_file", strings.Repeat("x", 4000), false, false, []string{"a.go"})
	// Read a.go AGAIN (flagged redundant).
	s.recordToolResult("read_file", strings.Repeat("x", 4000), false, true, []string{"a.go"})
	// Edit a.go — both reads are now expired waste.
	s.markFileEdited("a.go")

	if s.wasteTokens != s.totalTokens {
		t.Errorf("expected all %d tokens to be waste after edit (got %d waste)",
			s.totalTokens, s.wasteTokens)
	}
	if s.catTotals[wasteExpired] != s.totalTokens {
		t.Errorf("expected wasteExpired=%d, got %d", s.totalTokens, s.catTotals[wasteExpired])
	}
}

// #419: negative/exclusion results are exempt from wasteEmpty.
func TestTokenWasteNegativeResultExempt(t *testing.T) {
	s := newTokenWasteBudgetState()
	negatives := []string{
		"No matches found for FooBar",
		"0 results",
		"nothing found",
		"On branch main\nnothing to commit, working tree clean",
	}
	for _, n := range negatives {
		s.recordToolResult("grep", n, false, false, nil)
	}
	if s.wasteTokens != 0 {
		t.Errorf("negative results must not count as waste, got %d waste tokens", s.wasteTokens)
	}
}
