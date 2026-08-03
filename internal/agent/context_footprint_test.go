package agent

import (
	"strings"
	"testing"
)

func TestContextFootprint_BasicTracking(t *testing.T) {
	f := newContextFootprintState()

	// Record some results across categories
	f.recordResult("read_file", strings.Repeat("x", 1000), 1)  // ~270 tokens
	f.recordResult("grep", strings.Repeat("y", 2000), 2)       // ~540 tokens
	f.recordResult("run_command", strings.Repeat("z", 500), 3) // ~135 tokens

	if f.totalTokens == 0 {
		t.Fatal("totalTokens should be > 0 after recording")
	}
	if f.categoryTotals[footprintCatRead] == 0 {
		t.Error("read category should have tokens")
	}
	if f.categoryTotals[footprintCatSearch] == 0 {
		t.Error("search category should have tokens")
	}
	if f.categoryTotals[footprintCatCommand] == 0 {
		t.Error("command category should have tokens")
	}
}

func TestContextFootprint_DominanceDetection(t *testing.T) {
	f := newContextFootprintState()

	// Make search tools dominate: 10K tokens of grep results
	for i := 0; i < 10; i++ {
		f.recordResult("grep", strings.Repeat("match", 200), i) // ~270 tokens each
	}
	// Small amount from other categories
	f.recordResult("read_file", "hello", 11)

	// Total is ~2700, below the 8000 minimum threshold
	msg := f.check()
	if msg != "" {
		t.Fatal("should not fire below minimum threshold")
	}

	// Now add enough to exceed the threshold, still search-dominated
	for i := 0; i < 30; i++ {
		f.recordResult("search_files", strings.Repeat("data", 200), i+12)
	}

	// Now total should be > 8000 and search should dominate
	msg = f.check()
	if msg == "" {
		t.Fatal("should fire when search category dominates above threshold")
	}
	if !strings.Contains(msg, "Search/grep") {
		t.Errorf("message should mention Search/grep category, got: %s", msg)
	}
	if !strings.Contains(msg, "context footprint") {
		t.Errorf("message should contain header, got: %s", msg)
	}
}

func TestContextFootprint_NoFalsePositiveBalanced(t *testing.T) {
	f := newContextFootprintState()

	// Balanced usage across categories - no single category dominates
	for i := 0; i < 20; i++ {
		f.recordResult("read_file", strings.Repeat("a", 1000), i)
		f.recordResult("grep", strings.Repeat("b", 1000), i)
		f.recordResult("run_command", strings.Repeat("c", 1000), i)
	}

	msg := f.check()
	if msg != "" {
		t.Fatalf("should not fire when categories are balanced, got: %s", msg)
	}
}

func TestContextFootprint_WarnOncePerCategory(t *testing.T) {
	f := newContextFootprintState()

	// Fill with search-dominated content
	for i := 0; i < 40; i++ {
		f.recordResult("grep", strings.Repeat("match", 200), i)
	}

	msg1 := f.check()
	if msg1 == "" {
		t.Fatal("first check should fire")
	}

	// Add more, should not fire again for same category
	for i := 0; i < 10; i++ {
		f.recordResult("search_files", strings.Repeat("data", 200), i+40)
	}

	msg2 := f.check()
	if msg2 != "" {
		t.Fatal("second check for same category should not fire")
	}
}

func TestContextFootprint_Cooldown(t *testing.T) {
	f := newContextFootprintState()

	// Search-dominated
	for i := 0; i < 40; i++ {
		f.recordResult("grep", strings.Repeat("match", 200), i)
	}

	// First fires for search
	msg1 := f.check()
	if msg1 == "" {
		t.Fatal("first check should fire for search")
	}

	// Now make command category dominant too
	for i := 0; i < 50; i++ {
		f.recordResult("run_command", strings.Repeat("out", 200), i+40)
	}

	// Should be on cooldown
	msg2 := f.check()
	if msg2 != "" {
		t.Fatal("should respect cooldown period")
	}
}

func TestContextFootprint_Reset(t *testing.T) {
	f := newContextFootprintState()

	f.recordResult("read_file", "hello world", 1)
	f.recordResult("grep", "test", 2)

	f.reset()

	if f.totalTokens != 0 {
		t.Error("totalTokens should be 0 after reset")
	}
	if len(f.categoryTotals) != 0 {
		t.Error("categoryTotals should be empty after reset")
	}
	if len(f.warned) != 0 {
		t.Error("warned should be empty after reset")
	}
}

func TestContextFootprint_Summary(t *testing.T) {
	f := newContextFootprintState()

	f.recordResult("read_file", strings.Repeat("a", 1000), 1)
	f.recordResult("grep", strings.Repeat("b", 2000), 2)

	s := f.summary()
	if s == "" {
		t.Fatal("summary should not be empty")
	}
	if !strings.Contains(s, "read") || !strings.Contains(s, "Search") {
		t.Errorf("summary should mention categories, got: %s", s)
	}
}

func TestContextFootprint_SummaryEmpty(t *testing.T) {
	f := newContextFootprintState()
	s := f.summary()
	if s != "" {
		t.Fatal("summary should be empty with no data")
	}
}

func TestClassifyFootprintTool(t *testing.T) {
	tests := []struct {
		tool string
		want footprintCategory
	}{
		{"grep", footprintCatSearch},
		{"search_files", footprintCatSearch},
		{"code_search", footprintCatSearch},
		{"glob", footprintCatSearch},
		{"read_file", footprintCatRead},
		{"multi_file_read", footprintCatRead},
		{"list_directory", footprintCatRead},
		{"run_command", footprintCatCommand},
		{"start_command", footprintCatCommand},
		{"wait_command", footprintCatCommand},
		{"lsp_symbols", footprintCatLSP},
		{"lsp_definition", footprintCatLSP},
		{"edit_file", footprintCatOther},
		{"write_file", footprintCatOther},
	}

	for _, tt := range tests {
		got := classifyFootprintTool(tt.tool)
		if got != tt.want {
			t.Errorf("classifyFootprintTool(%q) = %v, want %v", tt.tool, got, tt.want)
		}
	}
}

func TestEstimateResultTokens(t *testing.T) {
	if estimateResultTokens("") != 0 {
		t.Error("empty string should be 0 tokens")
	}
	tokens := estimateResultTokens("hello world")
	if tokens <= 0 {
		t.Error("non-empty string should estimate > 0 tokens")
	}
	// ~3.7 chars per token
	if tokens > 5 {
		t.Errorf("11 chars should be ~3 tokens, got %d", tokens)
	}
}

func TestContextFootprint_CategoryHint(t *testing.T) {
	// All categories should have non-empty hints
	for _, cat := range []footprintCategory{footprintCatSearch, footprintCatRead, footprintCatCommand, footprintCatLSP, footprintCatOther} {
		hint := cat.hint(1000)
		if hint == "" {
			t.Errorf("category %v should have non-empty hint", cat)
		}
	}
}
