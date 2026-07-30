package agent

import (
	"strings"
	"testing"
)

func TestAnalyzeUserPrompt_SimpleRequest(t *testing.T) {
	p := newPlanState()
	// Simple request: single file, single action, short.
	score := p.analyzeUserPrompt("What does this file do?")
	if score >= complexityThreshold {
		t.Errorf("simple request scored %d (threshold %d), want < %d", score, complexityThreshold, complexityThreshold)
	}

	score = p.analyzeUserPrompt("Fix the typo in main.go")
	if score >= complexityThreshold {
		t.Errorf("simple fix scored %d (threshold %d), want < %d", score, complexityThreshold, complexityThreshold)
	}
}

func TestAnalyzeUserPrompt_ComplexMultiFile(t *testing.T) {
	p := newPlanState()
	// Complex request: multiple files, multiple verbs, sequential markers.
	prompt := "Add error handling across agent.go, prompt.go, and config.go. " +
		"First update the error types, then add retry logic, and finally add tests."
	score := p.analyzeUserPrompt(prompt)
	if score < complexityThreshold {
		t.Errorf("complex multi-file request scored %d (threshold %d), want >= %d", score, complexityThreshold, complexityThreshold)
	}
}

func TestAnalyzeUserPrompt_RefactorPattern(t *testing.T) {
	p := newPlanState()
	// Refactor is a known multi-step pattern. Combined with architecture language
	// and a broad scope indicator, it should score as complex.
	prompt := "Refactor the agent package to use interfaces across all modules."
	score := p.analyzeUserPrompt(prompt)
	if score < complexityThreshold {
		t.Errorf("refactor request scored %d (threshold %d), want >= %d", score, complexityThreshold, complexityThreshold)
	}
}

func TestAnalyzeUserPrompt_LongDetailedPrompt(t *testing.T) {
	p := newPlanState()
	// Very long prompt with architecture language.
	prompt := "Design a new architecture for the authentication system. " +
		"Create a new module called auth/ with the following components: " +
		"a token service, a session manager, and a middleware layer. " +
		"Also add logging throughout all existing handlers and add metrics " +
		"to track authentication success and failure rates. Update the config " +
		"system to support the new settings. Add comprehensive tests for all " +
		"new components. Migrate the existing user model to the new package."
	score := p.analyzeUserPrompt(prompt)
	if score < complexityThreshold {
		t.Errorf("long detailed request scored %d (threshold %d), want >= %d", score, complexityThreshold, complexityThreshold)
	}
}

func TestAnalyzeUserPrompt_EmptyString(t *testing.T) {
	p := newPlanState()
	score := p.analyzeUserPrompt("")
	if score != 0 {
		t.Errorf("empty prompt scored %d, want 0", score)
	}
}

func TestPlanState_ShouldSuggestPlan(t *testing.T) {
	p := newPlanState()
	p.setIsComplex(true, "complex prompt here")

	// At iteration 0, should not suggest yet.
	if p.shouldSuggestPlan(0) {
		t.Error("should not suggest at iteration 0")
	}

	// At iteration 1, should suggest.
	if !p.shouldSuggestPlan(planSuggestionIter) {
		t.Error("should suggest at planSuggestionIter")
	}

	p.markSuggested()

	// After suggestion, should not suggest again.
	if p.shouldSuggestPlan(planSuggestionIter + 1) {
		t.Error("should not suggest again after already suggested")
	}
}

func TestPlanState_ShouldNotSuggestWhenTodoCreated(t *testing.T) {
	p := newPlanState()
	p.setIsComplex(true, "complex prompt")
	p.markTodoCreated()

	if p.shouldSuggestPlan(planSuggestionIter) {
		t.Error("should not suggest when todo was already created")
	}
}

func TestPlanState_ReminderAfterSuggestion(t *testing.T) {
	p := newPlanState()
	p.setIsComplex(true, "complex prompt")

	// Suggest first.
	if !p.shouldSuggestPlan(planSuggestionIter) {
		t.Error("should suggest at planSuggestionIter")
	}
	p.markSuggested()

	// Should not remind before planReminderIter.
	if p.shouldRemindPlan(planReminderIter - 1) {
		t.Error("should not remind before planReminderIter")
	}

	// Should remind at planReminderIter.
	if !p.shouldRemindPlan(planReminderIter) {
		t.Error("should remind at planReminderIter")
	}
	p.markReminded()

	// Should not remind again.
	if p.shouldRemindPlan(planReminderIter + 1) {
		t.Error("should not remind again")
	}
}

func TestPlanState_Reset(t *testing.T) {
	p := newPlanState()
	p.setIsComplex(true, "prompt")
	p.markSuggested()
	p.markReminded()
	p.markTodoCreated()

	p.reset()

	if p.isComplex || p.suggested || p.reminded || p.todoCreated {
		t.Error("state not fully reset")
	}
}

func TestCountFileReferences(t *testing.T) {
	// No files.
	if count := countFileReferences("hello world"); count != 0 {
		t.Errorf("countFileReferences('hello world') = %d, want 0", count)
	}

	// Single file.
	if count := countFileReferences("edit main.go"); count < 1 {
		t.Errorf("countFileReferences('edit main.go') = %d, want >= 1", count)
	}

	// Multiple files.
	count := countFileReferences("edit main.go, utils.go, and handler.go")
	if count < 3 {
		t.Errorf("countFileReferences with 3 files = %d, want >= 3", count)
	}

	// Path-based references.
	count = countFileReferences("update internal/agent/ and internal/config/")
	if count < 1 {
		t.Errorf("countFileReferences with paths = %d, want >= 1", count)
	}
}

func TestCountFileReferences_ExcludesURLs(t *testing.T) {
	// URLs should not count as file references. The URL stripping logic
	// removes protocol+host+path before counting slash-separated paths.
	count := countFileReferences("check https://example.com/path/to/page and also http://test.org/another/path")
	if count > 0 {
		t.Errorf("URLs counted as files: %d", count)
	}
}

func TestCountKeywordMatches(t *testing.T) {
	// No keywords.
	if c := countKeywordMatches("hello world", []string{"foo", "bar"}); c != 0 {
		t.Errorf("expected 0, got %d", c)
	}

	// One keyword.
	if c := countKeywordMatches("create something", []string{"create", "delete"}); c != 1 {
		t.Errorf("expected 1, got %d", c)
	}

	// Multiple keywords (count each once).
	if c := countKeywordMatches("create and delete and create again", []string{"create", "delete"}); c != 2 {
		t.Errorf("expected 2, got %d", c)
	}
}

func TestPlanSuggestionText(t *testing.T) {
	text := planSuggestionText()
	if text == "" {
		t.Fatal("planSuggestionText() returned empty string")
	}
	if len(text) > maxPlanSuggestionLen {
		t.Errorf("planSuggestionText() is %d chars, exceeds max %d", len(text), maxPlanSuggestionLen)
	}
	if !strings.Contains(text, "todo_write") {
		t.Error("planSuggestionText() should mention todo_write")
	}
}

func TestPlanReminderText(t *testing.T) {
	text := planReminderText()
	if text == "" {
		t.Fatal("planReminderText() returned empty string")
	}
	if !strings.Contains(text, "todo_write") {
		t.Error("planReminderText() should mention todo_write")
	}
}
