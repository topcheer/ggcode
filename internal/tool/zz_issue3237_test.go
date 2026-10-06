package tool

// Regression probe for #3237: spawn_agent stores the task WITH the
// read-back nudge appended (readBackNudge template lines), and wait-side
// extraction used to swallow those nudge lines as acceptance criteria -
// every rule-following sub-agent got 5+ phantom MISSING verdicts on the
// standard task shape. The fix strips the nudge (marker Cut) before both
// reminders and makes "=== " a section boundary in extraction.

import (
	"strings"
	"testing"
)

func TestIssue3237_NudgeDoesNotPolluteExtraction(t *testing.T) {
	clean := "## Task\nfix the bug\n\n## Acceptance Criteria\n- all unit tests pass\n- go vet is clean"
	stored := clean + readBackNudge(clean) // what spawn actually stores

	// Sub-agent follows the handshake perfectly: restates both criteria.
	response := readBackMarker + "\n1. Make every unit test pass - run go test\n2. Keep go vet clean - run vet\n"

	rem := readBackReminder(stored, response)
	if rem == "" {
		t.Fatal("reminder must fire: criteria exist and were restated")
	}
	if strings.Contains(rem, "MISSING") || strings.Contains(rem, "never restated") {
		t.Fatalf("rule-following sub-agent must not get phantom verdicts:\n%s", rem)
	}
	if !strings.Contains(rem, "2/2 criteria restated") {
		t.Fatalf("expected clean 2/2 coverage, got:\n%s", rem)
	}
	// The nudge's template prose must not leak into the report.
	for _, noise := range []string{"MANDATORY FIRST STEP", "<your restatement", "If any criterion is ambiguous"} {
		if strings.Contains(rem, noise) {
			t.Fatalf("nudge template line leaked into criteria: %q", noise)
		}
	}
}

func TestIssue3237_AcceptanceExtractionSeesOriginalCriteria(t *testing.T) {
	clean := "## Task\nfix the bug\n\n## Acceptance Criteria\n- all unit tests pass\n- go vet is clean"
	stored := clean + readBackNudge(clean)

	// Defensive layer: even WITHOUT the strip, "=== " now closes the
	// criteria section, so extraction yields only the two real items.
	got := extractAcceptanceCriteria(stored)
	if len(got) != 2 {
		t.Fatalf("extracted %d criteria from a nudged task, want 2 (nudge swallowed as criteria): %v", len(got), got)
	}
}

func TestIssue3237_StripReadBackNudge(t *testing.T) {
	clean := "## Task\nbody\n\n## Acceptance Criteria\n- tests pass"
	stored := clean + readBackNudge(clean)
	if s := stripReadBackNudge(stored); s != clean {
		t.Fatalf("strip must restore the original task verbatim")
	}
	if s := stripReadBackNudge(clean); s != clean {
		t.Fatalf("strip must be a no-op on a clean task")
	}
}
