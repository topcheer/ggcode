package agent

import (
	"strings"
	"testing"
)

func TestCriteriaDriftReset(t *testing.T) {
	c := newCriteriaDriftState()
	c.indicators = []string{"a", "b"}
	c.warnCount = 1
	c.fired = true

	c.reset()

	if len(c.indicators) != 0 {
		t.Fatalf("expected indicators cleared, got %d", len(c.indicators))
	}
	if c.warnCount != 0 || c.fired {
		t.Fatalf("expected state reset, got warnCount=%d fired=%v", c.warnCount, c.fired)
	}
}

func TestCriteriaDriftSingleIndicatorNoWarn(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Single indicator should NOT trigger (threshold = 2)
	c.recordAssistantText("The main issue is fixed now.", 1)

	if msg := c.maybeWarn(2); msg != "" {
		t.Fatalf("expected no warning with single indicator, got: %s", msg)
	}
}

func TestCriteriaDriftTwoIndicatorsWarn(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Two indicators from different categories should trigger
	text := "The main issue is fixed. I implemented a simpler approach that covers the common case."
	c.recordAssistantText(text, 1)

	msg := c.maybeWarn(2)
	if msg == "" {
		t.Fatal("expected warning with 2 indicators, got none")
	}
	if !strings.Contains(msg, "Success Criteria Integrity") {
		t.Fatalf("warning missing expected label: %s", msg)
	}
}

func TestCriteriaDriftFiresOnce(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "The main issue is fixed. This handles the main scenario."
	c.recordAssistantText(text, 1)

	first := c.maybeWarn(2)
	if first == "" {
		t.Fatal("expected first warning")
	}

	second := c.maybeWarn(3)
	if second != "" {
		t.Fatal("expected no second warning (fired flag)")
	}
}

func TestCriteriaDriftMaxWarns(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text1 := "The main issue is fixed. I implemented a simpler approach."
	c.recordAssistantText(text1, 1)
	_ = c.maybeWarn(2)

	// Reset fired to simulate a second trigger path
	c.fired = false
	c.recordAssistantText("Is really a separate concern. Good enough for now.", 3)
	_ = c.maybeWarn(4)

	// Third attempt should be blocked by maxWarns
	c.fired = false
	c.recordAssistantText("A simpler solution. Out of scope.", 5)
	msg := c.maybeWarn(6)
	if msg != "" {
		t.Fatal("expected no third warning (maxWarns)")
	}
}

func TestCriteriaDriftNoDedup(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Same indicator twice should only count once
	c.recordAssistantText("The main issue is fixed.", 1)
	c.recordAssistantText("The main issue is resolved.", 2)

	if len(c.indicators) != 1 {
		t.Fatalf("expected 1 unique indicator, got %d", len(c.indicators))
	}
}

func TestCriteriaDriftCaseInsensitive(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "THE MAIN ISSUE IS FIXED. A SIMPLER APPROACH was used."
	c.recordAssistantText(text, 1)

	if len(c.indicators) != 2 {
		t.Fatalf("expected 2 case-insensitive matches, got %d", len(c.indicators))
	}
}

func TestCriteriaDriftReclassificationCategory(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "Rate limiting is really a separate concern. It is better addressed separately."
	c.recordAssistantText(text, 1)

	if len(c.indicators) < 2 {
		t.Fatalf("expected 2 indicators from reclassification, got %d", len(c.indicators))
	}
	msg := c.maybeWarn(2)
	if msg == "" {
		t.Fatal("expected warning with 2 reclassification indicators")
	}
}

func TestCriteriaDriftPartialCompleteCategory(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "This covers the common case. Good enough for now."
	c.recordAssistantText(text, 1)

	if len(c.indicators) < 2 {
		t.Fatalf("expected 2 indicators from partial_complete, got %d", len(c.indicators))
	}
}

func TestCriteriaDriftSubstitutionCategory(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "I took a different approach. Rather than the requested one."
	c.recordAssistantText(text, 1)

	if len(c.indicators) < 2 {
		t.Fatalf("expected 2 indicators from substitution, got %d", len(c.indicators))
	}
}

func TestCriteriaDriftCleanTextNoIndicators(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "I have implemented all requested features. The build passes and tests pass."
	c.recordAssistantText(text, 1)

	if len(c.indicators) != 0 {
		t.Fatalf("expected 0 indicators on clean text, got %d: %v", len(c.indicators), c.indicators)
	}
}

func TestCriteriaDriftWarningContent(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "The main issue is fixed. I implemented a simpler approach."
	c.recordAssistantText(text, 1)

	msg := c.maybeWarn(2)

	// Check warning contains key guidance elements
	checks := []string{
		"Success Criteria Integrity",
		"proxy gaming",
		"ORIGINAL request",
	}
	for _, check := range checks {
		if !strings.Contains(msg, check) {
			t.Errorf("warning missing %q in: %s", check, msg)
		}
	}
}

func TestCdContains(t *testing.T) {
	s := []string{"a", "b", "c"}
	if !cdContains(s, "b") {
		t.Error("expected cdContains to find 'b'")
	}
	if cdContains(s, "z") {
		t.Error("expected cdContains to NOT find 'z'")
	}
}
