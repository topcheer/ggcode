package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestPatternLearner_Basic(t *testing.T) {
	p := newPatternLearner()

	// Record a successful sequence
	p.recordToolCall("read_file", false)
	p.recordToolCall("edit_file", false)
	p.completeIteration(true)

	// Verify sequence was recorded
	p.mu.Lock()
	if len(p.successfulSeqs) != 1 {
		t.Errorf("Expected 1 successful sequence, got %d", len(p.successfulSeqs))
	}
	if len(p.successfulSeqs[0]) != 2 {
		t.Errorf("Expected sequence length 2, got %d", len(p.successfulSeqs[0]))
	}
	p.mu.Unlock()
}

func TestPatternLearner_ErrorResets(t *testing.T) {
	p := newPatternLearner()

	// Start a sequence
	p.recordToolCall("read_file", false)

	// Error occurs - should reset
	p.recordToolCall("edit_file", true) // error

	p.completeIteration(true)

	// Should have no successful sequences
	p.mu.Lock()
	if len(p.successfulSeqs) != 0 {
		t.Errorf("Expected 0 successful sequences after error, got %d", len(p.successfulSeqs))
	}
	p.mu.Unlock()
}

func TestPatternLearner_PatternExtraction(t *testing.T) {
	p := newPatternLearner()

	// Record same successful sequence 3 times
	for i := 0; i < 3; i++ {
		p.recordToolCall("grep", false)
		p.recordToolCall("read_file", false)
		p.recordToolCall("edit_file", false)
		p.completeIteration(true)
	}

	// Should have extracted the pattern
	p.mu.Lock()
	patternKey := "grep→read_file→edit_file"
	count, exists := p.patterns[patternKey]
	p.mu.Unlock()

	if !exists {
		t.Errorf("Pattern %s should exist", patternKey)
	}
	if count < 3 {
		t.Errorf("Expected pattern count >= 3, got %d", count)
	}
}

func TestPatternLearner_SuggestPatterns(t *testing.T) {
	p := newPatternLearner()

	// Train on a pattern
	for i := 0; i < patternMinOccurrences; i++ {
		p.recordToolCall("grep", false)
		p.recordToolCall("read_file", false)
		p.completeIteration(true)
	}

	// Check suggestion with partial match
	suggestion := p.suggestPatterns([]string{"grep"})
	if suggestion == "" {
		t.Errorf("Expected suggestion for partial pattern match")
	}
}

func TestPatternLearner_SuggestionLimit(t *testing.T) {
	p := newPatternLearner()

	// Train patterns
	for i := 0; i < patternMinOccurrences; i++ {
		p.recordToolCall("grep", false)
		p.recordToolCall("read_file", false)
		p.completeIteration(true)
	}

	// Exhaust suggestion limit
	for i := 0; i < patternSuggestionLimit+2; i++ {
		p.suggestPatterns([]string{"grep"})
	}

	// Should return empty after limit reached
	lastSuggestion := p.suggestPatterns([]string{"grep"})
	if lastSuggestion != "" {
		t.Errorf("Expected no suggestions after limit reached")
	}
}

func TestPatternLearner_Reset(t *testing.T) {
	p := newPatternLearner()

	// Add some data
	p.recordToolCall("grep", false)
	p.completeIteration(true)
	p.suggestPatterns([]string{"grep"})

	// Reset
	p.reset()

	// Verify cleared
	p.mu.Lock()
	if len(p.successfulSeqs) != 0 || len(p.patterns) != 0 || p.suggestions != 0 {
		t.Errorf("Reset did not clear state")
	}
	p.mu.Unlock()
}

func TestGetRecentToolNames(t *testing.T) {
	// Import provider package to use correct type
	calls := []provider.ToolCallDelta{
		{Name: "read_file"},
		{Name: "edit_file"},
		{Name: ""},
	}

	names := getRecentToolNames(calls)
	if len(names) != 2 {
		t.Errorf("Expected 2 tool names, got %d", len(names))
	}
	if names[0] != "read_file" || names[1] != "edit_file" {
		t.Errorf("Tool names incorrect: %v", names)
	}
}
