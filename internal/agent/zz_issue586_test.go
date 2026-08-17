package agent

import (
	"strings"
	"testing"
)

// #586 F1: Test that negation prevents false authorization exemptions.
// "NOT as requested" should NOT exempt a drift pattern.
func TestIssue586F1_NegationPreventsExemption(t *testing.T) {
	testCases := []struct {
		name         string
		text         string
		pattern      string
		category     string
		expectExempt bool
	}{
		{
			name:         "NOT as requested should NOT exempt",
			text:         "This is NOT as requested, and that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			category:     "reclassification",
			expectExempt: false,
		},
		{
			name:         "never as requested should NOT exempt",
			text:         "This is never as requested. That requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			category:     "reclassification",
			expectExempt: false,
		},
		{
			name:         "n't before marker should NOT exempt",
			text:         "I didn't do as requested. That requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			category:     "reclassification",
			expectExempt: false,
		},
		{
			name:         "normal as requested should exempt",
			text:         "As you requested, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			category:     "reclassification",
			expectExempt: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := cdIsAuthExempt(tc.text, tc.pattern, tc.category)
			if result != tc.expectExempt {
				t.Errorf("cdIsAuthExempt(%q, %q, %s) = %v, want %v", tc.text, tc.pattern, tc.category, result, tc.expectExempt)
			}
		})
	}
}

// #586 F1: Test that "you asked me about X" is not authorization.
func TestIssue586F1_NonAuthorizationVerbs(t *testing.T) {
	testCases := []struct {
		name         string
		text         string
		expectExempt bool
	}{
		{
			name:         "you asked me about X is question, not authorization",
			text:         "User asked me about the scope. That requirement is out of scope.",
			expectExempt: false,
		},
		{
			name:         "mentioned X is not authorization",
			text:         "User mentioned the requirement. That requirement is out of scope.",
			expectExempt: false,
		},
		{
			name:         "noted X is not authorization",
			text:         "User noted this. That requirement is out of scope.",
			expectExempt: false,
		},
		{
			name:         "user asked without 'about' should exempt (same sentence)",
			text:         "User asked to skip, and that requirement is out of scope.",
			expectExempt: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := cdIsAuthExempt(tc.text, "that requirement is out of scope", "reclassification")
			if result != tc.expectExempt {
				t.Errorf("cdIsAuthExempt(%q, ...) = %v, want %v", tc.text, result, tc.expectExempt)
			}
		})
	}
}

// #586 F2: Test that sentence splitting includes semicolons and newlines.
func TestIssue586F2_SentenceBoundariesIncludeSemicolonAndNewline(t *testing.T) {
	testCases := []struct {
		name         string
		text         string
		pattern      string
		marker       string
		expectExempt bool
	}{
		{
			name:         "semicolon is sentence boundary - marker in different clause should NOT exempt",
			text:         "The user said skip; that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			marker:       "skip",
			expectExempt: false,
		},
		{
			name:         "newline is sentence boundary - marker on different line should NOT exempt",
			text:         "User said skip\nThat requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			marker:       "skip",
			expectExempt: false,
		},
		{
			name:         "semicolon is sentence boundary - marker before semicolon should NOT exempt pattern after",
			text:         "As requested; that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false, // Semicolon IS a sentence boundary per F2 fix
		},
		{
			name:         "same sentence before newline - marker should exempt pattern on same line",
			text:         "As requested, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := cdIsAuthExempt(tc.text, tc.pattern, "reclassification")
			if result != tc.expectExempt {
				t.Errorf("cdIsAuthExempt(%q, %q, reclassification) = %v, want %v", tc.text, tc.pattern, result, tc.expectExempt)
			}
		})
	}
}

// #586 F2: Test that markdown bullet lists don't merge into one giant sentence.
func TestIssue586F2_MarkdownNewlinesCreateSentenceBoundaries(t *testing.T) {
	// In markdown output, bullets often have no punctuation:
	// - User said skip feature A
	// - Feature B is out of scope
	// These should be treated as separate sentences.
	text := "- User said skip feature A\n- That requirement is out of scope for B."
	result := cdIsAuthExempt(text, "that requirement is out of scope", "reclassification")
	if result {
		t.Errorf("Expected no exemption when marker and pattern are on different lines (markdown bullets)")
	}

	// Same line should still exempt
	text2 := "- As requested, that requirement is out of scope for this phase."
	result2 := cdIsAuthExempt(text2, "that requirement is out of scope", "reclassification")
	if !result2 {
		t.Errorf("Expected exemption when marker and pattern are on same line")
	}
}

// #586 F3: Test that exemption applies to all 4 categories.
func TestIssue586F3_ExemptionAppliesToAllCategories(t *testing.T) {
	testCases := []struct {
		name         string
		category     string
		text         string
		expectExempt bool
	}{
		{
			name:         "narrowing with authorization",
			category:     "narrowing",
			text:         "As you requested, the essential requirement is just the core feature.",
			expectExempt: true,
		},
		{
			name:         "reclassification with authorization",
			category:     "reclassification",
			text:         "As requested, that requirement is out of scope for this phase.",
			expectExempt: true,
		},
		{
			name:         "substitution with authorization",
			category:     "substitution",
			text:         "As requested, rather than the requested complex cache, I used a simple in-memory map.",
			expectExempt: true,
		},
		{
			name:         "partial_complete with authorization",
			category:     "partial_complete",
			text:         "As requested, good enough for the requirement is acceptable.",
			expectExempt: true,
		},
		{
			name:         "narrowing without authorization",
			category:     "narrowing",
			text:         "The essential requirement is just the core feature.",
			expectExempt: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Find a pattern from the category that actually occurs in the text
			patterns := criteriaDriftPatterns[tc.category]
			if len(patterns) == 0 {
				t.Fatalf("No patterns found for category %s", tc.category)
			}
			// Find the pattern that actually matches in the text
			var pattern string
			lowerText := strings.ToLower(tc.text)
			for _, pat := range patterns {
				if strings.Contains(lowerText, pat) {
					pattern = pat
					break
				}
			}
			if pattern == "" {
				t.Fatalf("No pattern from category %s found in text %q", tc.category, tc.text)
			}
			result := cdIsAuthExempt(tc.text, pattern, tc.category)
			if result != tc.expectExempt {
				t.Errorf("cdIsAuthExempt(%q, %q, %s) = %v, want %v", tc.text, pattern, tc.category, result, tc.expectExempt)
			}
		})
	}
}

// #586 F5: Test that "as you requested" is in marker list.
func TestIssue586F5_AsYouRequestedInMarkerList(t *testing.T) {
	// The fixer's own test text should work
	text := "As you requested, that requirement is out of scope for this phase."
	c := newCriteriaDriftState()
	defer c.reset()

	c.recordAssistantText(text, 1)
	if len(c.indicators) != 0 {
		t.Errorf("Expected 0 indicators with 'As you requested' marker, got %d", len(c.indicators))
	}

	// Verify cdIsAuthExempt returns true
	if !cdIsAuthExempt(text, "that requirement is out of scope", "reclassification") {
		t.Errorf("Expected cdIsAuthExempt to return true for 'As you requested'")
	}
}

// #586 F6: Test that multiple patterns in same sentence are handled correctly.
// Marker should only exempt patterns that appear AFTER it in the same sentence.
func TestIssue586F6_MarkerOnlyExemptsSubsequentPatterns(t *testing.T) {
	testCases := []struct {
		name              string
		text              string
		exemptPatterns    []string // patterns that should be exempt
		nonExemptPatterns []string // patterns that should NOT be exempt
	}{
		{
			name:              "marker after pattern should not exempt",
			text:              "That requirement is out of scope. As requested, deferring the requested requirement.",
			exemptPatterns:    []string{"deferring the requested requirement"},
			nonExemptPatterns: []string{"that requirement is out of scope"},
		},
		{
			name:              "marker in middle exempts only subsequent patterns",
			text:              "As you requested, that requirement is out of scope for this phase and deferring the other requested requirement.",
			exemptPatterns:    []string{"that requirement is out of scope", "deferring the other requested requirement"},
			nonExemptPatterns: []string{},
		},
		{
			name:              "marker at start exempts both patterns",
			text:              "As requested, that requirement is out of scope and deferring the requested requirement.",
			exemptPatterns:    []string{"that requirement is out of scope", "deferring the requested requirement"},
			nonExemptPatterns: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, pat := range tc.exemptPatterns {
				result := cdIsAuthExempt(tc.text, pat, "reclassification")
				if !result {
					t.Errorf("Expected pattern %q to be exempt in %q", pat, tc.text)
				}
			}
			for _, pat := range tc.nonExemptPatterns {
				result := cdIsAuthExempt(tc.text, pat, "reclassification")
				if result {
					t.Errorf("Expected pattern %q NOT to be exempt in %q", pat, tc.text)
				}
			}
		})
	}
}

// #586 F7: Test that two synonymous phrases in same sentence count as one indicator.
func TestIssue586F7_SameSentenceDeduplication(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Two narrowing phrases in the same sentence
	// Should only count as 1 indicator (same category, same sentence)
	text := "The requirement is really only the login form and the essential requirement is just authentication."
	c.recordAssistantText(text, 1)

	if len(c.indicators) != 1 {
		t.Fatalf("Expected 1 indicator (deduplicated same sentence), got %d: %v", len(c.indicators), c.indicators)
	}

	// Should NOT trigger because we only have 1 indicator
	msg := c.maybeWarn(2)
	if msg != "" {
		t.Fatalf("Expected no warning with single deduplicated indicator, got: %s", msg)
	}
}

// #586 F7: Test that different categories in same sentence still count separately.
func TestIssue586F7_DifferentCategoriesCountSeparately(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Narrowing and substitution in same sentence
	// Should count as 2 indicators (different categories)
	text := "The requirement is really only login and rather than the requested cache, I used a stub."
	c.recordAssistantText(text, 1)

	if len(c.indicators) != 2 {
		t.Fatalf("Expected 2 indicators (different categories), got %d: %v", len(c.indicators), c.indicators)
	}

	// Should trigger with 2 indicators
	msg := c.maybeWarn(2)
	if msg == "" {
		t.Fatal("Expected warning with 2 indicators from different categories")
	}
}

// #586 F7: Regression test - #582's TestIssue582Bug1 must still pass.
// Same-category progressive narrowing across different iterations should trigger.
func TestIssue586F7_Regression582Bug1(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Two narrowing indicators in DIFFERENT iterations (not same sentence)
	c.recordAssistantText("The requirement is really only the login form.", 1)
	c.recordAssistantText("The essential requirement is just authentication.", 2)

	if len(c.indicators) != 2 {
		t.Fatalf("Expected 2 indicators, got %d: %v", len(c.indicators), c.indicators)
	}

	// This SHOULD trigger (2 indicators from same category, different iterations)
	msg := c.maybeWarn(3)
	if msg == "" {
		t.Fatal("Regression: expected warning with 2 indicators from same category across iterations")
	}
	if !strings.Contains(msg, "Success Criteria Integrity") {
		t.Fatalf("warning missing expected label: %s", msg)
	}
}

// #586 F8: Test that comments match implementation.
// This is a documentation test - ensure the code behavior matches the comment.
func TestIssue586F8_CommentsMatchImplementation(t *testing.T) {
	// Test that marker must precede pattern (comment says this)
	textMarkerBefore := "As you requested, that requirement is out of scope."
	if !cdIsAuthExempt(textMarkerBefore, "that requirement is out of scope", "reclassification") {
		t.Error("Comment says marker must precede pattern, but marker before pattern did NOT exempt")
	}

	// Test that marker after pattern does NOT exempt
	textMarkerAfter := "That requirement is out of scope. As requested, I skipped it."
	if cdIsAuthExempt(textMarkerAfter, "that requirement is out of scope", "reclassification") {
		t.Error("Comment says marker must precede pattern, but marker after pattern DID exempt")
	}

	// Test that marker and pattern must be in same sentence
	textDifferentSentence := "The pattern 'as requested' is being tested. That requirement is out of scope."
	if cdIsAuthExempt(textDifferentSentence, "that requirement is out of scope", "reclassification") {
		t.Error("Comment says marker and pattern must be in same sentence, but different sentences DID exempt")
	}
}

// #586 Combined: Test all fixes together in a realistic scenario.
func TestIssue586_CombinedScenario(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Turn 1: User-authorized descoping (should be exempt)
	c.recordAssistantText("As you requested, that requirement is out of scope for this phase.", 1)
	if len(c.indicators) != 0 {
		t.Errorf("Turn 1: Expected 0 indicators (exempt), got %d", len(c.indicators))
	}

	// Turn 2: Unauthorized drift without marker (should count)
	c.recordAssistantText("The requirement is really only the core feature.", 2)
	if len(c.indicators) != 1 {
		t.Errorf("Turn 2: Expected 1 indicator, got %d", len(c.indicators))
	}

	// Turn 3: Two narrowing phrases in same sentence (should dedupe to 1)
	c.recordAssistantText("The essential requirement is just authentication and the requirement is really only login.", 3)
	// Should still have 2 total indicators (1 from turn 2, 1 deduped from turn 3)
	if len(c.indicators) != 2 {
		t.Errorf("Turn 3: Expected 2 indicators total (1+1 deduped), got %d", len(c.indicators))
	}

	// Turn 4: Should trigger with 2 indicators
	msg := c.maybeWarn(4)
	if msg == "" {
		t.Error("Expected warning after 2 indicators accumulated")
	}

	// Turn 5: After warning, indicators should be consumed
	if len(c.indicators) != 0 {
		t.Errorf("After warning: Expected 0 indicators (consumed), got %d", len(c.indicators))
	}
}
