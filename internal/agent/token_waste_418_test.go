package agent

import (
	"strings"
	"testing"
)

func TestTokenWasteMultiReadAllExpired(t *testing.T) {
	s := newTokenWasteBudgetState()

	s.recordToolResult("read_file", strings.Repeat("x", 4000), false, false, []string{"a.go"})

	s.recordToolResult("read_file", strings.Repeat("x", 4000), false, true, []string{"a.go"})

	s.markFileEdited("a.go")

	if s.wasteTokens != s.totalTokens {
		t.Errorf("expected all %d tokens to be waste after edit (got %d waste)",
			s.totalTokens, s.wasteTokens)
	}
	if s.catTotals[wasteExpired] != s.totalTokens {
		t.Errorf("expected wasteExpired=%d, got %d", s.totalTokens, s.catTotals[wasteExpired])
	}
}

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
