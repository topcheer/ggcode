package agent

import (
	"strings"
	"testing"
)

// #589 N1: Test that "per your instruction about X" is authorization (not a question).
// The fix should distinguish between:
// - "you asked me about X" = question/non-authorization
// - "per your instruction about X" = true authorization
func TestIssue589N1_PerInstructionAboutIsAuthorization(t *testing.T) {
	testCases := []struct {
		name         string
		text         string
		pattern      string
		expectExempt bool
	}{
		{
			name:         "per your instruction about X should exempt (true authorization)",
			text:         "Per your instruction about the deadline, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: true,
		},
		{
			name:         "you asked me about X should NOT exempt (question form)",
			text:         "You asked me about the scope. That requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
		},
		{
			name:         "user asked me about X should NOT exempt (question form)",
			text:         "User asked me about the deadline, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
		},
		{
			name:         "as you asked about X should NOT exempt (question form)",
			text:         "As you asked about the scope, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
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

// #589 N2: Test that "noted"/"mentioned" only checked in bounded window.
// The fix should only scan ~20 chars after marker, not entire remaining text.
func TestIssue589N2_NotedMentionedBoundedWindow(t *testing.T) {
	testCases := []struct {
		name         string
		text         string
		pattern      string
		expectExempt bool
	}{
		{
			name:         "immediate 'noted' in suffix should NOT exempt",
			text:         "As you requested earlier (you noted this too), that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false, // "you noted" is in the immediate context (~20 chars) after marker
		},
		{
			name:         "immediate 'mentioned' in suffix should NOT exempt",
			text:         "As requested (you mentioned this), that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
		},
		{
			name:         "noted in distant parenthesis (30+ chars) should exempt",
			text:         "As requested earlier (and you noted this requirement separately in another conversation), that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: true, // "noted" is far away (>20 chars), should not block authorization
		},
		{
			name:         "mentioned in distant sentence should NOT exempt (different sentence)",
			text:         "As you requested earlier. You mentioned it before, but that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false, // Marker and pattern are in different sentences (separated by period)
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

// #589 F1-B: Test that negation at any position in sentence prevents exemption.
// The fix should scan to sentence start, not just 5 chars with idx>5 limit.
func TestIssue589F1B_NegationAnyPositionInSentence(t *testing.T) {
	testCases := []struct {
		name         string
		text         string
		pattern      string
		expectExempt bool
	}{
		{
			name:         "Not as requested at idx=4 (<5) should NOT exempt",
			text:         "Not as requested, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false, // "Not" at position 0, marker at 4, should still detect negation
		},
		{
			name:         "never as requested should NOT exempt",
			text:         "Never as requested, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
		},
		{
			name:         "didn't as requested should NOT exempt",
			text:         "I didn't do as requested. That requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
		},
		{
			name:         "negation earlier in sentence should NOT exempt",
			text:         "It's not as you requested, so that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
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

// #589 F1-C: Test that negation beyond 5-char window still prevents exemption.
func TestIssue589F1C_NegationBeyond5CharWindow(t *testing.T) {
	testCases := []struct {
		name         string
		text         string
		pattern      string
		expectExempt bool
	}{
		{
			name:         "not only as requested (negation 8 chars before) should NOT exempt",
			text:         "not only as requested but also more, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false, // "not" is 8 chars before marker, should still detect
		},
		{
			name:         "never really as requested (negation 6 chars before) should NOT exempt",
			text:         "never really as you requested, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
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

// #589 N3: Test that marker side checks all occurrences, not just first.
// If first occurrence is negated but second is clean, should exempt.
func TestIssue589N3_MarkerSideChecksAllOccurrences(t *testing.T) {
	testCases := []struct {
		name         string
		text         string
		pattern      string
		expectExempt bool
	}{
		{
			name:         "first 'as requested' negated, second clean should exempt",
			text:         "Earlier it was not as requested, so now redoing it as requested: that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: true, // Second "as requested" is clean and precedes pattern
		},
		{
			name:         "multiple markers, first negated, last clean should exempt",
			text:         "Not as you requested. Later, as you requested properly: that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: true,
		},
		{
			name:         "all markers negated should NOT exempt",
			text:         "Not as requested and never as requested, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
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

// #589 N4: Design choice - colon+newline splitting does not exempt.
// This documents the known tradeoff of F2 strictness.
func TestIssue589N4_ColonNewlineSplittingTradeoff(t *testing.T) {
	// "As requested:\n- that requirement is out of scope"
	// The colon+newline creates a sentence boundary, so marker and pattern are in different sentences.
	// This is a known tradeoff of F2 strictness - not considered a defect.
	text := "As requested:\n- that requirement is out of scope for this phase."
	result := cdIsAuthExempt(text, "that requirement is out of scope", "reclassification")
	if result {
		t.Error("Expected NO exemption when marker and pattern are separated by colon+newline (known F2 strictness tradeoff)")
	}
	// Same line should still exempt
	textSameLine := "As requested, that requirement is out of scope for this phase."
	resultSameLine := cdIsAuthExempt(textSameLine, "that requirement is out of scope", "reclassification")
	if !resultSameLine {
		t.Error("Expected exemption when marker and pattern are on same line")
	}
}

// #589 Combined: Test all fixes together in realistic scenarios.
func TestIssue589_CombinedScenario(t *testing.T) {
	testCases := []struct {
		name         string
		text         string
		pattern      string
		expectExempt bool
		reason       string
	}{
		{
			name:         "true authorization with 'per your instruction about'",
			text:         "Per your instruction about the deadline, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: true,
			reason:       "N1 fix: 'per your instruction about' is authorization, not question",
		},
		{
			name:         "question form 'you asked me about' should NOT exempt",
			text:         "You asked me about the scope, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
			reason:       "N1 fix: 'you asked me about' is question, not authorization",
		},
		{
			name:         "distant 'noted' should not block authorization",
			text:         "As requested earlier (and you noted this separately), that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: true,
			reason:       "N2 fix: bounded window for 'noted' check",
		},
		{
			name:         "immediate 'noted' should block authorization",
			text:         "As requested (you noted this), that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
			reason:       "N2 fix: immediate 'noted' blocks authorization",
		},
		{
			name:         "negation at idx<5 should NOT exempt",
			text:         "Not as requested, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
			reason:       "F1-B fix: scan to sentence start, no idx>5 limit",
		},
		{
			name:         "negation beyond 5-char window should NOT exempt",
			text:         "not only as requested but more, that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: false,
			reason:       "F1-C fix: remove 5-char window limit",
		},
		{
			name:         "first marker negated, second clean should exempt",
			text:         "Not as requested. As you requested properly: that requirement is out of scope.",
			pattern:      "that requirement is out of scope",
			expectExempt: true,
			reason:       "N3 fix: check all marker occurrences",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := cdIsAuthExempt(tc.text, tc.pattern, "reclassification")
			if result != tc.expectExempt {
				t.Errorf("%s: cdIsAuthExempt(%q, %q, reclassification) = %v, want %v\nReason: %s",
					tc.name, tc.text, tc.pattern, result, tc.expectExempt, tc.reason)
			}
		})
	}
}

// #589 Integration: Test that indicators accumulate correctly with the fixes.
func TestIssue589_IndicatorAccumulation(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Turn 1: True authorization (should be exempt)
	c.recordAssistantText("Per your instruction about the deadline, that requirement is out of scope.", 1)
	if len(c.indicators) != 0 {
		t.Errorf("Turn 1: Expected 0 indicators (exempt), got %d", len(c.indicators))
	}

	// Turn 2: Non-authorization question (should count)
	c.recordAssistantText("You asked me about the scope, that requirement is out of scope.", 2)
	if len(c.indicators) != 1 {
		t.Errorf("Turn 2: Expected 1 indicator, got %d", len(c.indicators))
	}

	// Turn 3: Negated marker (should count)
	c.recordAssistantText("Not as requested, that requirement is out of scope.", 3)
	if len(c.indicators) != 2 {
		t.Errorf("Turn 3: Expected 2 indicators, got %d", len(c.indicators))
	}

	// Turn 4: Should trigger with 2 indicators
	msg := c.maybeWarn(4)
	if msg == "" {
		t.Error("Expected warning after 2 indicators accumulated")
	}

	// Verify warning content
	if !strings.Contains(msg, "Success Criteria Integrity") {
		t.Errorf("Warning missing expected label: %s", msg)
	}
}
