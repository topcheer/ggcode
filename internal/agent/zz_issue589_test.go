package agent

import (
	"strings"
	"testing"
)

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
			expectExempt: false,
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
			expectExempt: true,
		},
		{
			name:         "mentioned in distant sentence should NOT exempt (different sentence)",
			text:         "As you requested earlier. You mentioned it before, but that requirement is out of scope.",
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
			expectExempt: false,
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
			expectExempt: false,
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
			expectExempt: true,
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

func TestIssue589N4_ColonNewlineSplittingTradeoff(t *testing.T) {

	text := "As requested:\n- that requirement is out of scope for this phase."
	result := cdIsAuthExempt(text, "that requirement is out of scope", "reclassification")
	if result {
		t.Error("Expected NO exemption when marker and pattern are separated by colon+newline (known F2 strictness tradeoff)")
	}

	textSameLine := "As requested, that requirement is out of scope for this phase."
	resultSameLine := cdIsAuthExempt(textSameLine, "that requirement is out of scope", "reclassification")
	if !resultSameLine {
		t.Error("Expected exemption when marker and pattern are on same line")
	}
}

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

func TestIssue589_IndicatorAccumulation(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	c.recordAssistantText("Per your instruction about the deadline, that requirement is out of scope.", 1)
	if len(c.indicators) != 0 {
		t.Errorf("Turn 1: Expected 0 indicators (exempt), got %d", len(c.indicators))
	}

	c.recordAssistantText("You asked me about the scope, that requirement is out of scope.", 2)
	if len(c.indicators) != 1 {
		t.Errorf("Turn 2: Expected 1 indicator, got %d", len(c.indicators))
	}

	c.recordAssistantText("Not as requested, that requirement is out of scope.", 3)
	if len(c.indicators) != 2 {
		t.Errorf("Turn 3: Expected 2 indicators, got %d", len(c.indicators))
	}

	msg := c.maybeWarn(4)
	if msg == "" {
		t.Error("Expected warning after 2 indicators accumulated")
	}

	if !strings.Contains(msg, "Success Criteria Integrity") {
		t.Errorf("Warning missing expected label: %s", msg)
	}
}
