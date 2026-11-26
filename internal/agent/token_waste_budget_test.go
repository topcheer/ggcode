package agent

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input    string
		min, max int
	}{
		{"", 0, 0},
		{"hello world", 1, 5},
		{strings.Repeat("a", 400), 80, 120},
		{strings.Repeat("a", 4000), 900, 1100},
	}
	for _, tt := range tests {
		got := estimateTokens(tt.input)
		if tt.input == "" {
			if got != 0 {
				t.Errorf("estimateTokens(\"\") = %d, want 0", got)
			}
			continue
		}
		if got < tt.min || got > tt.max {
			t.Errorf("estimateTokens(%d chars) = %d, want %d-%d", len(tt.input), got, tt.min, tt.max)
		}
	}
}

func TestTokenWasteBudgetState_RecordAndRatio(t *testing.T) {
	s := newTokenWasteBudgetState()

	s.recordToolResult("read_file", strings.Repeat("x", 4000), false, false, nil)
	if s.wasteTokens != 0 {
		t.Errorf("expected 0 waste tokens, got %d", s.wasteTokens)
	}

	s.recordToolResult("edit_file", "error: file not found", true, false, nil)
	if s.wasteTokens == 0 {
		t.Error("expected waste tokens > 0 after error")
	}

	s.recordToolResult("grep", "  \n  ", false, false, nil)
	if s.catTotals[wasteEmpty] == 0 {
		t.Error("expected wasteEmpty category to have tokens")
	}

	before := s.catTotals[wasteEmpty]
	s.recordToolResult("grep", "no matches found for FooBar", false, false, nil)
	if s.catTotals[wasteEmpty] != before {
		t.Error("negative-result search must be exempt from wasteEmpty")
	}

	s.recordToolResult("read_file", strings.Repeat("y", 2000), false, true, nil)
	if s.catTotals[wasteRedundant] == 0 {
		t.Error("expected wasteRedundant category to have tokens")
	}

	if s.totalTokens == 0 {
		t.Error("expected totalTokens > 0")
	}
	if s.wasteTokens > s.totalTokens {
		t.Errorf("waste (%d) > total (%d)", s.wasteTokens, s.totalTokens)
	}
}

func TestTokenWasteBudgetState_ExpiredMarking(t *testing.T) {
	s := newTokenWasteBudgetState()

	s.recordToolResult("read_file", strings.Repeat("code", 500), false, false, []string{"/path/file.go"})
	if s.wasteTokens != 0 {
		t.Errorf("expected 0 waste before edit, got %d", s.wasteTokens)
	}

	s.markFileEdited("/path/file.go")
	if s.wasteTokens == 0 {
		t.Error("expected waste tokens > 0 after expiration")
	}
	if s.catTotals[wasteExpired] == 0 {
		t.Error("expected wasteExpired category to have tokens")
	}

	before := s.wasteTokens
	s.markFileEdited("/path/file.go")
	if s.wasteTokens != before {
		t.Errorf("double edit changed waste from %d to %d", before, s.wasteTokens)
	}
}

func TestTokenWasteBudgetState_Reset(t *testing.T) {
	s := newTokenWasteBudgetState()
	s.recordToolResult("grep", "error", true, false, nil)
	s.recordToolResult("read_file", strings.Repeat("a", 2000), false, false, []string{"/f.go"})
	s.warnings = 1

	s.reset()

	if len(s.entries) != 0 || s.totalTokens != 0 || s.wasteTokens != 0 || s.warnings != 0 {
		t.Error("reset did not clear all state")
	}
	if len(s.readPaths) != 0 || len(s.catTotals) != 0 {
		t.Error("reset did not clear maps")
	}
}
