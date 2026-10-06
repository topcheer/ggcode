package tool

import (
	"strings"
	"testing"
)

func TestExtractAcceptanceCriteriaMarkdown(t *testing.T) {
	task := "Implement feature X.\n\n## Acceptance Criteria\n- tests pass with -tags goolm\n- go build clean\n- docs updated\n\n## Constraints\n- no new deps"
	got := extractAcceptanceCriteria(task)
	if len(got) != 3 {
		t.Fatalf("want 3 criteria, got %d: %v", len(got), got)
	}
	if got[0] != "tests pass with -tags goolm" {
		t.Errorf("first criterion wrong: %q", got[0])
	}
}

func TestExtractAcceptanceCriteriaDoneWhen(t *testing.T) {
	task := "Fix the bug.\nDone when:\n1. repro test green\n2. no race in -race run"
	got := extractAcceptanceCriteria(task)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d: %v", len(got), got)
	}
	if got[1] != "no race in -race run" {
		t.Errorf("numbered item not stripped: %q", got[1])
	}
}

func TestExtractAcceptanceCriteriaNone(t *testing.T) {
	if got := extractAcceptanceCriteria("Just do the thing, no criteria section."); got != nil {
		t.Errorf("want nil, got %v", got)
	}
	if got := extractAcceptanceCriteria(""); got != nil {
		t.Errorf("empty task want nil, got %v", got)
	}
}

func TestExtractAcceptanceCriteriaCapAndTrim(t *testing.T) {
	var b strings.Builder
	b.WriteString("## Acceptance\n")
	for i := 0; i < 15; i++ {
		b.WriteString("- item ")
		b.WriteString(strings.Repeat("x", 300))
		b.WriteString("\n")
	}
	got := extractAcceptanceCriteria(b.String())
	if len(got) != maxAcceptanceItems {
		t.Fatalf("cap not enforced: %d", len(got))
	}
	if !strings.HasSuffix(got[0], "...") || len(got[0]) > maxAcceptanceItemLen+3 {
		t.Errorf("long item not trimmed: %d chars", len(got[0]))
	}
}

func TestAcceptanceReminder(t *testing.T) {
	task := "Task.\n## Acceptance Criteria\n- criterion A\n- criterion B"
	r := acceptanceReminder(task, "did the work, trust me")
	if !strings.Contains(r, acceptanceMarker) {
		t.Error("marker missing")
	}
	if !strings.Contains(r, "1. criterion A") || !strings.Contains(r, "2. criterion B") {
		t.Error("criteria not enumerated")
	}
	if !strings.Contains(r, "self-report") {
		t.Error("validator guidance missing")
	}
	// No criteria -> no reminder.
	if r := acceptanceReminder("no criteria here", "result"); r != "" {
		t.Errorf("want empty reminder, got %q", r)
	}
	// No result -> no reminder.
	if r := acceptanceReminder(task, ""); r != "" {
		t.Errorf("empty result should skip, got %q", r)
	}
	// Re-poll idempotency: marker already present.
	if r := acceptanceReminder(task, "result "+acceptanceMarker); r != "" {
		t.Errorf("re-poll should skip, got %q", r)
	}
}
