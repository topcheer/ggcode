package tool

import (
	"strings"
	"testing"
)

const rbTask = `Fix the build in internal/widget.

## Acceptance Criteria
- go build ./... passes with no errors
- widget test coverage stays above 80 percent
- no new files outside internal/widget`

// Task without a criteria section: nudge must be a no-op.
func TestReadBackNudge_NoCriteria(t *testing.T) {
	if got := readBackNudge("just do the thing"); got != "" {
		t.Errorf("nudge on criteria-less task = %q, want empty", got)
	}
}

// Criteria task: nudge appended, once only (idempotent on re-send).
func TestReadBackNudge_AppendsAndIsIdempotent(t *testing.T) {
	n1 := readBackNudge(rbTask)
	if !strings.Contains(n1, readBackMarker) {
		t.Fatalf("nudge missing marker: %q", n1)
	}
	task := rbTask + n1
	if n2 := readBackNudge(task); n2 != "" {
		t.Errorf("second nudge on already-marked task = %q, want empty", n2)
	}
}

// Full restatement of all criteria: all pass, no per-criterion lines.
func TestCompareReadBack_FullCoverage(t *testing.T) {
	resp := "=== DoD READ-BACK ===\n" +
		"1. run go build ./... and confirm it passes with zero errors\n" +
		"2. widget tests keep coverage above 80 percent\n" +
		"3. no new files outside internal/widget\n"
	rep := compareReadBack(rbTask, resp)
	if !rep.HadBlock {
		t.Fatal("read-back block not detected")
	}
	for i, v := range rep.Verdicts {
		if v != coveragePass {
			t.Errorf("criterion %d verdict = %v, want pass (criteria: %s)", i+1, v, rep.Criteria[i])
		}
	}
}

// One criterion never mentioned: reported MISSING by the reminder.
func TestCompareReadBack_PartialCoverage(t *testing.T) {
	resp := "=== DoD READ-BACK ===\n" +
		"1. go build ./... passes with no errors\n" +
		"2. coverage above 80 percent\n"
	// criterion 3 (files outside internal/widget) never restated
	rem := readBackReminder(rbTask, resp)
	if !strings.Contains(rem, "MISSING") {
		t.Errorf("missing criterion not flagged:\n%s", rem)
	}
}

// Child ignored the handshake: no block at all - explicit warning.
func TestReadBackReminder_NoBlock(t *testing.T) {
	rem := readBackReminder(rbTask, "done, build fixed")
	if !strings.Contains(rem, "never restated") {
		t.Errorf("no-block warning missing:\n%s", rem)
	}
}

// Empty/short result or criteria-less task: reminder empty, no nag.
func TestReadBackReminder_EmptyCases(t *testing.T) {
	if got := readBackReminder(rbTask, ""); got != "" {
		t.Errorf("empty result produced reminder: %q", got)
	}
	if got := readBackReminder("plain task", "result text"); got != "" {
		t.Errorf("criteria-less task produced reminder: %q", got)
	}
}

// Re-poll (report marker already present): no duplicate report.
func TestReadBackReminder_Idempotent(t *testing.T) {
	first := readBackReminder(rbTask, "=== DoD READ-BACK REPORT (front-end handshake) ===\nsomething")
	if first != "" {
		t.Errorf("re-poll duplicated the report: %q", first)
	}
}

// Block extraction stops at the next markdown heading.
func TestExtractReadBackBlock_StopsAtHeading(t *testing.T) {
	resp := "=== DoD READ-BACK ===\n1. build passes\n## Work Log\nunrelated text with build words"
	block := extractReadBackBlock(resp)
	if strings.Contains(block, "unrelated") {
		t.Errorf("block bled past the next heading: %q", block)
	}
}

// Content word set: stop words and short tokens dropped.
func TestContentWordSet_Filters(t *testing.T) {
	ws := contentWordSet("The build must pass, and no errors will remain!")
	for _, stop := range []string{"the", "and", "will", "must"} {
		if ws[stop] {
			t.Errorf("stop word %q kept", stop)
		}
	}
	for _, keep := range []string{"build", "pass", "errors", "remain"} {
		if !ws[keep] {
			t.Errorf("content word %q dropped", keep)
		}
	}
}
