package agent

import (
	"strings"
	"testing"
)

// #582 Bug 1: Test that threshold counts indicators, not categories.
// Single-category progressive narrowing (2+ indicators from same category) should trigger.
func TestIssue582Bug1_IndicatorCountThreshold(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Same narrowing category, two different indicators within the window.
	// This is the "single-category progressive drift" pattern that should fire.
	c.recordAssistantText("The requirement is really only the login form.", 1)
	c.recordAssistantText("The essential requirement is just authentication.", 2)

	// Check that we have 2 indicators from 1 category
	if len(c.indicators) != 2 {
		t.Fatalf("expected 2 indicators, got %d: %v", len(c.indicators), c.indicators)
	}

	// This SHOULD trigger because we have 2+ indicators (not 2+ categories)
	msg := c.maybeWarn(3)
	if msg == "" {
		t.Fatal("Bug 1: expected warning with 2 indicators from same category, got none")
	}
	if !strings.Contains(msg, "Success Criteria Integrity") {
		t.Fatalf("warning missing expected label: %s", msg)
	}
}

// #582 Bug 1: Test that single indicator does not trigger.
func TestIssue582Bug1_SingleIndicatorNoWarn(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Only one indicator from narrowing category
	c.recordAssistantText("The requirement is really only the login form.", 1)

	// Should NOT trigger - need 2+ indicators
	msg := c.maybeWarn(2)
	if msg != "" {
		t.Fatalf("expected no warning with single indicator, got: %s", msg)
	}
}

// #582 Bug 3: Test that user-authorized descoping is exempt.
// Agent faithfully executing user instruction should NOT be flagged as proxy gaming.
func TestIssue582Bug3_AuthorizedDescopingExempt(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// User explicitly says "rate limiting is out of scope, skip it"
	// Agent faithfully responds: "as requested, that requirement is out of scope"
	// This should NOT trigger because of the "as requested" authorization marker
	text := "As you requested, rate limiting is out of scope for this phase."
	c.recordAssistantText(text, 1)

	// Should have 0 indicators because the reclassification pattern is exempt
	if len(c.indicators) != 0 {
		t.Fatalf("Bug 3: expected 0 indicators for authorized descoping, got %d: %v", len(c.indicators), c.indicators)
	}

	msg := c.maybeWarn(2)
	if msg != "" {
		t.Fatalf("Bug 3: expected no warning for authorized descoping, got: %s", msg)
	}
}

// #582 Bug 3: Test multiple authorization markers.
func TestIssue582Bug3_MultipleAuthMarkers(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	testCases := []struct {
		text   string
		exempt bool
	}{
		{"Per your instruction, that requirement is out of scope.", true},
		{"User asked to defer that part of the request.", true},
		{"As requested, that requirement falls outside the scope.", true},
		{"That requirement is out of scope.", false},                 // No auth marker
		{"That part of the request is out of scope for now.", false}, // No auth marker
	}

	for i, tc := range testCases {
		c.reset()
		c.recordAssistantText(tc.text, 1)
		if tc.exempt {
			if len(c.indicators) != 0 {
				t.Errorf("case %d: expected exemption (0 indicators) for %q, got %d", i, tc.text, len(c.indicators))
			}
		} else {
			if len(c.indicators) == 0 {
				t.Errorf("case %d: expected indicator (no exemption) for %q", i, tc.text)
			}
		}
	}
}

// #582 Bug 3: Test that authorization markers only exempt reclassification.
// Other categories should not be affected by authorization markers.
func TestIssue582Bug3_AuthExemptionReclassificationOnly(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// "as requested" with narrowing pattern should still register
	// (auth exemption only applies to reclassification category)
	text := "As requested, the requirement is really only the happy path."
	c.recordAssistantText(text, 1)

	if len(c.indicators) != 1 {
		t.Fatalf("expected 1 indicator (narrowing not exempt by auth), got %d: %v", len(c.indicators), c.indicators)
	}
}

// #582 Bug 3: Test that authorization markers must be in the same sentence as the pattern.
// Text containing auth phrases in a different context should not cause false exemptions.
func TestIssue582Bug3_AuthMarkerSelfReference(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// "as requested" appears in sentence 1, reclassification pattern in sentence 2.
	// They are in different semantic contexts, so the pattern should NOT be exempt.
	text := "The pattern 'as requested' is being tested. That requirement is out of scope for this test."
	c.recordAssistantText(text, 1)

	// Should NOT be exempt because auth marker is in a different sentence
	if len(c.indicators) != 1 {
		t.Fatalf("expected 1 indicator (auth marker in different sentence), got %d: %v", len(c.indicators), c.indicators)
	}
}

// #582 Bug 3: Test that authorization marker must precede the pattern.
func TestIssue582Bug3_AuthMarkerMustPrecede(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Authorization marker AFTER the pattern should not exempt
	text := "That requirement is out of scope. Per your instruction, I skipped it."
	c.recordAssistantText(text, 1)

	// Should NOT be exempt because auth marker is after the pattern
	if len(c.indicators) != 1 {
		t.Fatalf("expected 1 indicator (auth marker after pattern), got %d: %v", len(c.indicators), c.indicators)
	}
}

// #582 Bug 2: Test that seenCategories is maintained for compatibility.
// After Bug 1 fix, the threshold uses indicator count, not category count.
// seenCategories is kept as a legacy field for test compatibility.
func TestIssue582Bug2_SeenCategoriesLegacyField(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Record indicators from multiple categories
	c.recordAssistantText("The requirement is really only login. Rather than the requested cache, I used a stub.", 1)

	// seenCategories should still be tracked (for compatibility)
	if len(c.seenCategories) < 2 {
		t.Fatalf("expected 2+ categories tracked in seenCategories, got %d: %v", len(c.seenCategories), c.seenCategories)
	}

	// But the warning decision is based on indicator count, not category count
	msg := c.maybeWarn(2)
	if msg == "" {
		t.Fatal("expected warning based on indicator count (2 indicators)")
	}
}

// #582 Combined: Test that 2 indicators from same category + 1 from another triggers.
func TestIssue582Combined_MixedIndicators(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// 2 narrowing indicators + 1 substitution indicator = 3 total indicators
	c.recordAssistantText("The requirement is really only login. The essential requirement is just auth.", 1)
	c.recordAssistantText("Rather than the requested cache, I used direct queries.", 2)

	if len(c.indicators) != 3 {
		t.Fatalf("expected 3 indicators, got %d: %v", len(c.indicators), c.indicators)
	}

	msg := c.maybeWarn(3)
	if msg == "" {
		t.Fatal("expected warning with 3 indicators across 2 categories")
	}
}
