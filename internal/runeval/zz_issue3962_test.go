package runeval

import "testing"

// #3962: DuplicateGroups are keyed by callKey{tool, canonical input}, so a
// single session commonly contributes MULTIPLE groups for the same tool
// (grep 'foo'x3, grep 'bar'x2 -> two groups). The old counter incremented
// Sessions once per GROUP, so one session rendered as "across 3 sessions" -
// the exact diagnostic distortion the struct doc forbids ("sessions that had
// at least one duplicate group for it").
func TestIssue3962_SessionsCountDistinctSessionsNotGroups(t *testing.T) {
	// One session, three grep duplicate groups (different inputs).
	a := Aggregate([]SessionReport{
		{ID: "s1", Report: Report{ToolCalls: 7, EfficiencyScore: 80, DuplicateGroups: []DuplicateGroup{
			{Tool: "grep", Count: 3, Repeats: 2},
			{Tool: "grep", Count: 2, Repeats: 1},
			{Tool: "grep", Count: 2, Repeats: 1},
		}}},
	})
	var top *RepeatedToolAgg
	for i := range a.TopRepeatedTools {
		if a.TopRepeatedTools[i].Tool == "grep" {
			top = &a.TopRepeatedTools[i]
		}
	}
	if top == nil {
		t.Fatalf("no grep aggregate: %+v", a.TopRepeatedTools)
	}
	if top.Sessions != 1 {
		t.Fatalf("#3962: single session with 3 dup groups must count Sessions=1, got %d", top.Sessions)
	}
	if top.Repeats != 4 {
		t.Fatalf("repeats must still sum across groups: want 4, got %d", top.Repeats)
	}
	// The rendered finding must not claim cross-session spread.
	for _, f := range a.Findings {
		if contains(f, "grep") && contains(f, "across 3 sessions") {
			t.Fatalf("finding still claims phantom cross-session spread: %s", f)
		}
	}
}

// Distinct-session semantics preserved: two sessions, one group each for the
// same tool, still count Sessions=2 (the pre-existing test's blind spot is
// that its fixtures cannot distinguish the two counting disciplines).
func TestIssue3962_DistinctSessionsStillCounted(t *testing.T) {
	a := Aggregate([]SessionReport{
		{ID: "s1", Report: Report{ToolCalls: 2, EfficiencyScore: 80, DuplicateGroups: []DuplicateGroup{
			{Tool: "read_file", Count: 2, Repeats: 1},
		}}},
		{ID: "s2", Report: Report{ToolCalls: 2, EfficiencyScore: 80, DuplicateGroups: []DuplicateGroup{
			{Tool: "read_file", Count: 2, Repeats: 1},
			{Tool: "read_file", Count: 2, Repeats: 1}, // same session, second group: must NOT bump to 3
		}}},
	})
	var top *RepeatedToolAgg
	for i := range a.TopRepeatedTools {
		if a.TopRepeatedTools[i].Tool == "read_file" {
			top = &a.TopRepeatedTools[i]
		}
	}
	if top == nil {
		t.Fatalf("no read_file aggregate: %+v", a.TopRepeatedTools)
	}
	if top.Sessions != 2 {
		t.Fatalf("two distinct sessions must count Sessions=2 (second group in s2 must not bump), got %d", top.Sessions)
	}
	if top.Repeats != 3 {
		t.Fatalf("repeats sum across all groups: want 3 (1+1+1), got %d", top.Repeats)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
