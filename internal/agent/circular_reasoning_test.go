package agent

import (
	"strings"
	"testing"
)

func TestCircularReasoningIdentityBecause(t *testing.T) {
	text := "This is the correct approach because it's correct for the use case."
	instances := scanCircularReasoning(text)
	if len(instances) == 0 {
		t.Fatal("expected identity_because pattern to match")
	}
	if instances[0].patternID != "identity_because" {
		t.Errorf("expected identity_because, got %s", instances[0].patternID)
	}
}

func TestCircularReasoningRestateSolution(t *testing.T) {
	text := "To fix the timeout, we should fix the timeout"
	instances := scanCircularReasoning(text)
	if len(instances) == 0 {
		t.Fatal("expected restate_solution pattern to match")
	}
}

func TestCircularReasoningRestateSolutionVariant(t *testing.T) {
	text := "in order to resolve the parsing bug, we need to resolve the parsing bug"
	instances := scanCircularReasoning(text)
	if len(instances) == 0 {
		t.Fatal("expected restate_solution pattern to match with 'in order to'")
	}
}

func TestCircularReasoningCircularFailure(t *testing.T) {
	text := "The function fails because it fails when called"
	instances := scanCircularReasoning(text)
	if len(instances) == 0 {
		t.Fatal("expected circular_failure pattern to match")
	}
}

func TestCircularReasoningSynonymTautology(t *testing.T) {
	text := "This is the right way because it's the correct way to handle it."
	instances := scanCircularReasoning(text)
	if len(instances) == 0 {
		t.Fatal("expected synonym_tautology pattern to match")
	}
}

func TestCircularReasoningNeedsCircular(t *testing.T) {
	text := "We need error handling because we need error handling"
	instances := scanCircularReasoning(text)
	if len(instances) == 0 {
		t.Fatal("expected needs_circular pattern to match")
	}
}

func TestCircularReasoningPurposeEqualsAction(t *testing.T) {
	text := "The reason for caching is to caching data."
	instances := scanCircularReasoning(text)
	if len(instances) == 0 {
		t.Fatal("expected purpose_equals_action pattern to match")
	}
}

func TestCircularReasoningVacuousImplication(t *testing.T) {
	text := "Since validation is required, validation must be implemented"
	instances := scanCircularReasoning(text)
	if len(instances) == 0 {
		t.Fatal("expected vacuous_implication pattern to match")
	}
}

func TestCircularReasoningNoMatch(t *testing.T) {
	texts := []string{
		"The build failed because the import path was incorrect.",
		"I'll add a retry mechanism since the API has rate limits.",
		"This function returns an error when the input is empty.",
		"We should use a mutex to prevent concurrent map access.",
		"The test passes because the implementation correctly handles the edge case.",
	}
	for _, text := range texts {
		instances := scanCircularReasoning(text)
		if len(instances) > 0 {
			t.Errorf("expected no match for non-circular text: %q, got %d matches: %v", text, len(instances), instances[0].patternID)
		}
	}
}

func TestCircularReasoningEmptyAndShort(t *testing.T) {
	instances := scanCircularReasoning("")
	if instances != nil {
		t.Error("expected nil for empty text")
	}
	instances = scanCircularReasoning("short")
	if instances != nil {
		t.Error("expected nil for short text")
	}
}

func TestCircularReasoningDedup(t *testing.T) {
	text := "This is the correct approach because it's correct. Also that is the correct way because it's correct."
	instances := scanCircularReasoning(text)
	// Both match identity_because but with different excerpts, so should have 2
	if len(instances) < 1 {
		t.Fatal("expected at least 1 instance")
	}
}

func TestCircularReasoningStateReset(t *testing.T) {
	s := newCircularReasoningState()
	s.recordCircularReasoning("This is correct because it's correct.", 0)
	if len(s.instances) != 1 {
		t.Fatal("expected 1 instance after recording")
	}
	s.reset()
	if len(s.instances) != 0 {
		t.Error("expected 0 instances after reset")
	}
	if s.warnings != 0 {
		t.Error("expected 0 warnings after reset")
	}
}

func TestCircularReasoningWarningThreshold(t *testing.T) {
	a := &Agent{circularReasoning: newCircularReasoningState()}

	// First circular instance -- should not warn (threshold=2)
	hint := a.maybeWarnCircularReasoning("This is the correct approach because it's correct.", 0)
	if hint != "" {
		t.Fatal("expected no warning with only 1 instance")
	}

	// Second instance -- should now warn
	hint = a.maybeWarnCircularReasoning("It fails because it fails.", 1)
	if hint == "" {
		t.Fatal("expected warning with 2 instances")
	}
	if !strings.Contains(hint, "[CIRCULAR-REASONING]") {
		t.Error("expected [CIRCULAR-REASONING] prefix in hint")
	}
	if !strings.Contains(hint, "evidence") {
		t.Error("expected guidance about evidence in hint")
	}
}

func TestCircularReasoningMaxWarnings(t *testing.T) {
	a := &Agent{circularReasoning: newCircularReasoningState()}

	// Trigger warning twice
	a.maybeWarnCircularReasoning("This is correct because it's correct.", 0)
	a.maybeWarnCircularReasoning("It fails because it fails.", 1)
	if a.circularReasoning.warnings != 1 {
		t.Fatalf("expected 1 warning, got %d", a.circularReasoning.warnings)
	}

	// More instances but max warnings reached
	a.maybeWarnCircularReasoning("We need caching because we need caching.", 2)
	hint := a.maybeWarnCircularReasoning("The reason for refactoring is to refactoring code.", 3)
	// Should have warned on iteration 2 (3rd call)
	if a.circularReasoning.warnings != 2 {
		t.Fatalf("expected 2 warnings, got %d", a.circularReasoning.warnings)
	}

	// Further calls should not warn
	hint = a.maybeWarnCircularReasoning("More circular stuff because it's correct.", 4)
	if hint != "" {
		t.Error("expected no warning after max warnings reached")
	}
}

func TestCircularReasoningNilState(t *testing.T) {
	a := &Agent{circularReasoning: nil}
	hint := a.maybeWarnCircularReasoning("This is correct because it's correct.", 0)
	if hint != "" {
		t.Error("expected empty hint with nil state")
	}
}
