package agent

import (
	"strings"
	"testing"
)

func TestIssue582Bug1_IndicatorCountThreshold(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	c.recordAssistantText("The requirement is really only the login form.", 1)
	c.recordAssistantText("The essential requirement is just authentication.", 2)

	if len(c.indicators) != 2 {
		t.Fatalf("expected 2 indicators, got %d: %v", len(c.indicators), c.indicators)
	}

	msg := c.maybeWarn(3)
	if msg == "" {
		t.Fatal("Bug 1: expected warning with 2 indicators from same category, got none")
	}
	if !strings.Contains(msg, "Success Criteria Integrity") {
		t.Fatalf("warning missing expected label: %s", msg)
	}
}

func TestIssue582Bug1_SingleIndicatorNoWarn(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	c.recordAssistantText("The requirement is really only the login form.", 1)

	msg := c.maybeWarn(2)
	if msg != "" {
		t.Fatalf("expected no warning with single indicator, got: %s", msg)
	}
}

func TestIssue582Bug3_AuthorizedDescopingExempt(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "As you requested, that requirement is out of scope for this phase."
	c.recordAssistantText(text, 1)

	if len(c.indicators) != 0 {
		t.Fatalf("Bug 3: expected 0 indicators for authorized descoping, got %d: %v", len(c.indicators), c.indicators)
	}

	msg := c.maybeWarn(2)
	if msg != "" {
		t.Fatalf("Bug 3: expected no warning for authorized descoping, got: %s", msg)
	}

	textNoAuth := "That requirement is out of scope for this phase."
	if cdIsAuthExempt(textNoAuth, "that requirement is out of scope", "reclassification") {
		t.Fatal("Bug 3: expected no exemption without authorization marker")
	}

	if !cdIsAuthExempt(text, "that requirement is out of scope", "reclassification") {
		t.Fatal("Bug 3: expected exemption with authorization marker")
	}
}

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
		{"That requirement is out of scope.", false},
		{"That part of the request is out of scope for now.", false},
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

func TestIssue582Bug3_AuthExemptionAllCategories(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "As requested, the requirement is really only the happy path."
	c.recordAssistantText(text, 1)

	if len(c.indicators) != 0 {
		t.Fatalf("expected 0 indicators (narrowing with auth is now exempt), got %d: %v", len(c.indicators), c.indicators)
	}
}

func TestIssue582Bug3_AuthMarkerSelfReference(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "The pattern 'as requested' is being tested. That requirement is out of scope for this test."
	c.recordAssistantText(text, 1)

	if len(c.indicators) != 1 {
		t.Fatalf("expected 1 indicator (auth marker in different sentence), got %d: %v", len(c.indicators), c.indicators)
	}
}

func TestIssue582Bug3_AuthMarkerMustPrecede(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	text := "That requirement is out of scope. Per your instruction, I skipped it."
	c.recordAssistantText(text, 1)

	if len(c.indicators) != 1 {
		t.Fatalf("expected 1 indicator (auth marker after pattern), got %d: %v", len(c.indicators), c.indicators)
	}
}

func TestIssue582Bug2_SeenCategoriesLegacyField(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

	c.recordAssistantText("The requirement is really only login. Rather than the requested cache, I used a stub.", 1)

	if len(c.seenCategories) < 2 {
		t.Fatalf("expected 2+ categories tracked in seenCategories, got %d: %v", len(c.seenCategories), c.seenCategories)
	}

	msg := c.maybeWarn(2)
	if msg == "" {
		t.Fatal("expected warning based on indicator count (2 indicators)")
	}
}

func TestIssue582Combined_MixedIndicators(t *testing.T) {
	c := newCriteriaDriftState()
	defer c.reset()

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
