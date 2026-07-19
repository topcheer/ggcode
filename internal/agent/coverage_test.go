package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// --- appendSyntheticToolResults tests ---

func TestAppendSyntheticToolResults_Empty(t *testing.T) {
	msgs := []provider.Message{{Role: "assistant"}}
	result := appendSyntheticToolResults(msgs, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 message (unchanged), got %d", len(result))
	}
}

func TestAppendSyntheticToolResults_SingleOpenCall(t *testing.T) {
	msgs := []provider.Message{{Role: "assistant"}}
	open := []openCall{{id: "call_1", name: "edit_file"}}
	result := appendSyntheticToolResults(msgs, open)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[1].Role != "user" {
		t.Errorf("expected role 'user', got %q", result[1].Role)
	}
	if len(result[1].Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result[1].Content))
	}
	block := result[1].Content[0]
	if block.Type != "tool_result" {
		t.Errorf("expected tool_result type, got %q", block.Type)
	}
	if block.ToolID != "call_1" {
		t.Errorf("expected tool_id 'call_1', got %q", block.ToolID)
	}
	if !block.IsError {
		t.Error("expected IsError=true for synthetic result")
	}
}

func TestAppendSyntheticToolResults_EmptyName(t *testing.T) {
	msgs := []provider.Message{{Role: "assistant"}}
	open := []openCall{{id: "call_1", name: ""}}
	result := appendSyntheticToolResults(msgs, open)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	// Empty name should be replaced with "unknown"
	if result[1].Content[0].ToolName != "unknown" {
		t.Errorf("expected tool name 'unknown', got %q", result[1].Content[0].ToolName)
	}
}

func TestAppendSyntheticToolResults_Multiple(t *testing.T) {
	msgs := []provider.Message{}
	open := []openCall{
		{id: "call_1", name: "edit_file"},
		{id: "call_2", name: "run_command"},
	}
	result := appendSyntheticToolResults(msgs, open)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if len(result[0].Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(result[0].Content))
	}
}

// --- SplitRunEntries / MergeInsights tests ---

func TestSplitRunEntries_Empty(t *testing.T) {
	entries := SplitRunEntries("")
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestSplitRunEntries_SingleEntry(t *testing.T) {
	input := "## Run Reflection\nDid some work."
	entries := SplitRunEntries(input)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestSplitRunEntries_MultipleEntries(t *testing.T) {
	input := "## Run Reflection\nEntry 1\n\n## Run Reflection\nEntry 2"
	entries := SplitRunEntries(input)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(entries), entries)
	}
}

func TestSplitRunEntries_WithPreamble(t *testing.T) {
	input := "# Insights\n\nSome preamble text\n\n## Run Reflection\nEntry 1"
	entries := SplitRunEntries(input)
	// Preamble should be captured as its own entry
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (preamble + run), got %d: %v", len(entries), entries)
	}
}

func TestMergeInsights_BasicMerge(t *testing.T) {
	existing := "## Run Reflection\nOld entry"
	newEntry := "## Run Reflection\nNew entry"
	result := MergeInsights(existing, newEntry)
	entries := SplitRunEntries(result)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after merge, got %d", len(entries))
	}
}

func TestMergeInsights_CapAt10(t *testing.T) {
	var existing string
	for i := 0; i < 15; i++ {
		existing = MergeInsights(existing, "## Run Reflection\nEntry")
	}
	entries := SplitRunEntries(existing)
	if len(entries) > 10 {
		t.Errorf("expected at most 10 entries, got %d", len(entries))
	}
}

// --- ShouldReflect tests ---

func TestShouldReflect_TooFewToolCalls(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{"edit_file": 2},
	}
	if ShouldReflect(stats) {
		t.Error("expected false for <3 tool calls and no files/commands")
	}
}

func TestShouldReflect_EnoughToolCalls(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{"edit_file": 3},
		Success:   true,
	}
	if !ShouldReflect(stats) {
		t.Error("expected true for 3+ tool calls with success")
	}
}

func TestShouldReflect_FailureSingleIteration(t *testing.T) {
	stats := RunStats{
		ToolCalls:  map[string]int{"edit_file": 5},
		Success:    false,
		Iterations: 1,
	}
	if ShouldReflect(stats) {
		t.Error("expected false for failed run with <=1 iteration")
	}
}

func TestShouldReflect_FailureMultipleIterations(t *testing.T) {
	stats := RunStats{
		ToolCalls:  map[string]int{"edit_file": 5},
		Success:    false,
		Iterations: 3,
	}
	if !ShouldReflect(stats) {
		t.Error("expected true for failed run with >1 iteration")
	}
}

func TestShouldReflect_FilesEditedNoToolCalls(t *testing.T) {
	stats := RunStats{
		ToolCalls:   map[string]int{},
		FilesEdited: []string{"/tmp/test.go"},
		Success:     true,
	}
	if !ShouldReflect(stats) {
		t.Error("expected true when files were edited")
	}
}
