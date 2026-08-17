package agent

import (
	"strings"
	"testing"
)

func TestCriteriaDriftReset(t *testing.T) {
	c := newCriteriaDriftState()
	c.indicators = []cdIndicator{{pattern: "a", iter: 1}, {pattern: "b", iter: 1}}
	c.warnCount = 1

	c.reset()

	if len(c.indicators) != 0 {
		t.Fatalf("expected indicators cleared, got %d", len(c.indicators))
	}
	if c.warnCount != 0 {
		t.Fatalf("expected state reset, got warnCount=%d", c.warnCount)
	}
}

func TestCriteriaDriftSingleIndicatorNoWarn(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Single indicator should NOT trigger (threshold = 2)
	c.recordAssistantText("The requirement is really only the happy path.", 1)

	if msg := c.maybeWarn(2); msg != "" {
		t.Fatalf("expected no warning with single indicator, got: %s", msg)
	}
}

func TestCriteriaDriftTwoIndicatorsWarn(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Two indicators from different categories should trigger
	text := "The requirement is really only the login form. Rather than the requested caching, I used direct queries."
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

	text := "The requirement is really only the happy path. Good enough for the requirement overall."
	c.recordAssistantText(text, 1)

	first := c.maybeWarn(2)
	if first == "" {
		t.Fatal("expected first warning")
	}

	// #332: consumed indicators are cleared after a warning — the same batch
	// cannot immediately re-trigger; a second warning requires fresh
	// indicators within the turn window.
	second := c.maybeWarn(3)
	if second != "" {
		t.Fatal("expected no second warning from consumed indicators")
	}

	// New indicators within the window DO allow the second warning.
	// ("Is really a separate concern" = reclassification, "I took a different
	// approach" = substitution — 2 distinct categories.)
	c.recordAssistantText("That requirement is out of scope for now. Rather than the requested X I used a stub.", 3)
	third := c.maybeWarn(4)
	if third == "" {
		t.Fatal("expected second warning from fresh indicators (warnCount=1 < cdMaxWarns=2)")
	}

	// Fourth attempt blocked by warnCount >= cdMaxWarns
	c.recordAssistantText("A simpler solution. Good enough for now.", 5)
	if msg := c.maybeWarn(6); msg != "" {
		t.Fatal("expected no third warning (warnCount >= cdMaxWarns)")
	}
}

func TestCriteriaDriftMaxWarns(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text1 := "The requirement is really only login. Substituting the requirement with a stub."
	c.recordAssistantText(text1, 1)
	_ = c.maybeWarn(2)

	// Add new drift categories within the window to trigger a second warning
	c.recordAssistantText("Is really a separate concern. Good enough for now.", 3)
	_ = c.maybeWarn(4)

	// Third attempt should be blocked by maxWarns (warnCount >= cdMaxWarns=2)
	c.recordAssistantText("A simpler solution. Out of scope.", 5)
	msg := c.maybeWarn(6)
	if msg != "" {
		t.Fatal("expected no third warning (maxWarns)")
	}
}

// #332: indicators from distant turns must not be stitched into a warning.
func TestCriteriaDriftDistantTurnsNoWarn(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// iter 3: legitimate root-cause analysis (narrowing category)
	c.recordAssistantText("The core problem is the nil map at line 42.", 3)
	// iters 4-8: neutral tool turns
	// iter 9: legitimate follow-up suggestion (reclassification category)
	c.recordAssistantText("The flaky test should be a follow-up task.", 9)

	if msg := c.maybeWarn(10); msg != "" {
		t.Fatalf("expected no warning for distant-turn indicators, got: %s", msg)
	}
}

// #332: same-turn (adjacent) multi-category indicators still fire.
func TestCriteriaDriftAdjacentTurnsWarn(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	c.recordAssistantText("The requirement is really only the nil map fix.", 8)
	c.recordAssistantText("Deferring the requested requirement until later.", 9)

	if msg := c.maybeWarn(10); msg == "" {
		t.Fatal("expected warning for indicators within window")
	}
}

func TestCriteriaDriftNoDedup(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// Intra-turn dedup still applies: the same phrase twice in ONE response
	// (even across two sentences) counts once.
	c.recordAssistantText("The requirement is really only login. The requirement is really only logout.", 1)
	if len(c.indicators) != 1 {
		t.Fatalf("expected 1 unique indicator within a turn, got %d", len(c.indicators))
	}

	// #589: Cross-turn repetition counts again. Progressive drift often
	// repeats the same phrase in later turns; the turn window in maybeWarn
	// bounds accumulation instead of a global dedup that would swallow the
	// drift signal.
	c.recordAssistantText("The requirement is really only login, again.", 2)
	if len(c.indicators) != 2 {
		t.Fatalf("expected 2 indicators after cross-turn repetition, got %d", len(c.indicators))
	}
}

func TestCriteriaDriftCaseInsensitive(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "THE REQUIREMENT IS REALLY ONLY LOGIN. RATHER THAN THE REQUESTED CACHE a stub was used."
	c.recordAssistantText(text, 1)

	if len(c.indicators) != 2 {
		t.Fatalf("expected 2 case-insensitive matches, got %d", len(c.indicators))
	}
}

func TestCriteriaDriftReclassificationCategory(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	// #582: Threshold now uses indicator count (not category count).
	// "that requirement is out of scope for now" → reclassification
	// "rather than the requested caching" → substitution
	// Total: 2 indicators from 2 categories, should trigger.
	text := "That requirement is out of scope for now. Rather than the requested caching I used a stub."
	c.recordAssistantText(text, 1)

	// seenCategories is still tracked for compatibility (legacy field)
	if len(c.seenCategories) < 2 {
		t.Fatalf("expected 2+ categories tracked, got %d: %v", len(c.seenCategories), c.seenCategories)
	}
	msg := c.maybeWarn(2)
	if msg == "" {
		t.Fatal("expected warning with 2+ indicators across categories")
	}
}

func TestCriteriaDriftPartialCompleteCategory(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "Good enough for the requirement. Meets the acceptance criteria enough."
	c.recordAssistantText(text, 1)

	if len(c.indicators) < 2 {
		t.Fatalf("expected 2 indicators from partial_complete, got %d", len(c.indicators))
	}
}

func TestCriteriaDriftSubstitutionCategory(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "Rather than the requested one. Substituting the requirement with a stub."
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

	text := "The requirement is really only login. Rather than the requested cache I used a stub."
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
	s := []cdIndicator{{pattern: "a", iter: 1}, {pattern: "b", iter: 2}, {pattern: "c", iter: 3}}
	if !cdContains(s, "b") {
		t.Error("expected cdContains to find 'b'")
	}
	if cdContains(s, "z") {
		t.Error("expected cdContains to NOT find 'z'")
	}
}
