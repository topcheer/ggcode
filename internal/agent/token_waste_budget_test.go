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

	// Record productive result (large, non-error).
	s.recordToolResult("read_file", strings.Repeat("x", 4000), false, false, nil)
	if s.wasteTokens != 0 {
		t.Errorf("expected 0 waste tokens, got %d", s.wasteTokens)
	}

	// Record error result (waste).
	s.recordToolResult("edit_file", "error: file not found", true, false, nil)
	if s.wasteTokens == 0 {
		t.Error("expected waste tokens > 0 after error")
	}

	// Record empty result (waste).
	s.recordToolResult("grep", "no matches", false, false, nil)
	if s.catTotals[wasteEmpty] == 0 {
		t.Error("expected wasteEmpty category to have tokens")
	}

	// Record redundant result (waste).
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

	// Record a read of a file.
	s.recordToolResult("read_file", strings.Repeat("code", 500), false, false, []string{"/path/file.go"})
	if s.wasteTokens != 0 {
		t.Errorf("expected 0 waste before edit, got %d", s.wasteTokens)
	}

	// Mark the file as edited - should expire the prior read.
	s.markFileEdited("/path/file.go")
	if s.wasteTokens == 0 {
		t.Error("expected waste tokens > 0 after expiration")
	}
	if s.catTotals[wasteExpired] == 0 {
		t.Error("expected wasteExpired category to have tokens")
	}

	// Double-edit should not double-count (already expired).
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

func TestMaybeWarnTokenWaste_BelowThreshold(t *testing.T) {
	a := &Agent{tokenWasteBudget: newTokenWasteBudgetState()}

	// Record only productive results.
	for i := 0; i < 6; i++ {
		a.tokenWasteBudget.recordToolResult("read_file", strings.Repeat("x", 2000), false, false, nil)
	}

	// Waste ratio is 0% - should not warn.
	if hint := a.maybeWarnTokenWaste(); hint != "" {
		t.Errorf("expected no warning at 0%% waste, got: %s", hint)
	}
}

func TestMaybeWarnTokenWaste_AboveThreshold(t *testing.T) {
	a := &Agent{tokenWasteBudget: newTokenWasteBudgetState()}

	// Record enough waste to exceed both thresholds.
	// 6 large errors (waste) + 1 small productive result.
	for i := 0; i < 6; i++ {
		a.tokenWasteBudget.recordToolResult("edit_file", strings.Repeat("error ", 1000), true, false, nil)
	}
	a.tokenWasteBudget.recordToolResult("read_file", "small ok result", false, false, nil)

	hint := a.maybeWarnTokenWaste()
	if hint == "" {
		t.Error("expected warning when waste > 40%")
	}
	if !strings.Contains(hint, "token-waste") {
		t.Errorf("warning missing [token-waste] tag: %s", hint)
	}
}

func TestMaybeWarnTokenWaste_InsufficientData(t *testing.T) {
	a := &Agent{tokenWasteBudget: newTokenWasteBudgetState()}

	// Only 2 results, below wasteMinToolResults.
	a.tokenWasteBudget.recordToolResult("edit_file", "error", true, false, nil)
	a.tokenWasteBudget.recordToolResult("edit_file", "error2", true, false, nil)

	if hint := a.maybeWarnTokenWaste(); hint != "" {
		t.Errorf("expected no warning with insufficient data, got: %s", hint)
	}
}

func TestMaybeWarnTokenWaste_MaxWarnings(t *testing.T) {
	a := &Agent{tokenWasteBudget: newTokenWasteBudgetState()}
	a.tokenWasteBudget.warnings = wasteMaxWarnings

	if hint := a.maybeWarnTokenWaste(); hint != "" {
		t.Errorf("expected no warning after max reached, got: %s", hint)
	}
}

func TestWasteCategoryString(t *testing.T) {
	tests := []struct {
		cat  wasteCategory
		want string
	}{
		{wasteNone, "productive"},
		{wasteError, "error"},
		{wasteEmpty, "empty"},
		{wasteRedundant, "redundant"},
		{wasteExpired, "expired"},
	}
	for _, tt := range tests {
		if got := tt.cat.String(); got != tt.want {
			t.Errorf("wasteCategory(%d).String() = %s, want %s", tt.cat, got, tt.want)
		}
	}
}
