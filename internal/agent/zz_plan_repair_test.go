package agent

import (
	"strings"
	"testing"
)

func pdItems(n int) []planItem {
	items := make([]planItem, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, planItem{Text: strings.Repeat("x", 5), Keywords: []string{"zzabsent" + string(rune('a'+i)), "zzgone"}})
	}
	return items
}

func stalledStats() *RunStats {
	return &RunStats{} // corpus empty: nothing matches -> all items unaddressed
}

// TestPlanVerdictEscalation pins the three-value verdict state machine:
// first drift -> continue nudge, second consecutive -> repair, third ->
// abandon, then silence (cap). A clean check resets the streak.
func TestPlanVerdictEscalation(t *testing.T) {
	p := newPlanDriftState()
	p.captured = true
	p.items = pdItems(4)

	first := p.checkPlanDrift(stalledStats(), "")
	if !strings.Contains(first, "[Plan Drift Detection]") {
		t.Fatalf("first stalled check must be a continue nudge, got %q", first)
	}
	second := p.checkPlanDrift(stalledStats(), "")
	if !strings.Contains(second, "[Plan Repair]") || !strings.Contains(second, "todo_write") {
		t.Fatalf("second consecutive check must demand a revised plan, got %q", second)
	}
	third := p.checkPlanDrift(stalledStats(), "")
	if !strings.Contains(third, "[Plan Escalation]") || !strings.Contains(third, "Ask the user") {
		t.Fatalf("third consecutive check must escalate to the user, got %q", third)
	}
	if after := p.checkPlanDrift(stalledStats(), ""); after != "" {
		t.Fatalf("verdict cap must silence further checks, got %q", after)
	}
}

// TestPlanVerdictStreakReset pins that a clean check resets the escalation
// streak (and that a clean first check no longer burns the warning - the
// fire-once bug fix).
func TestPlanVerdictStreakReset(t *testing.T) {
	p := newPlanDriftState()
	p.captured = true
	p.items = pdItems(4)

	cleanStats := &RunStats{FilesEdited: []string{"/x/zzabsenta.go", "/x/zzgone.go"}}
	if got := p.checkPlanDrift(cleanStats, ""); got != "" {
		t.Fatalf("addressed plan items must not fire, got %q", got)
	}
	if p.verdictCount != 0 {
		t.Fatalf("clean check must not consume a verdict, got %d", p.verdictCount)
	}
	// One drifted check, then clean again: streak resets, next drift is a
	// fresh continue verdict, not a repair.
	_ = p.checkPlanDrift(stalledStats(), "")
	if p.stallStreak != 1 {
		t.Fatalf("streak must be 1 after first drift, got %d", p.stallStreak)
	}
	_ = p.checkPlanDrift(cleanStats, "")
	if p.stallStreak != 0 {
		t.Fatalf("clean check must reset streak, got %d", p.stallStreak)
	}
	again := p.checkPlanDrift(stalledStats(), "")
	if !strings.Contains(again, "[Plan Drift Detection]") {
		t.Fatalf("post-reset drift must be a fresh continue verdict, got %q", again)
	}
}

// TestPlanVerdictFor pins the streak->verdict mapping incl. overflow.
func TestPlanVerdictFor(t *testing.T) {
	cases := []struct {
		streak int
		want   planVerdict
	}{{1, verdictContinue}, {2, verdictRepair}, {3, verdictAbandon}, {9, verdictAbandon}}
	for _, tc := range cases {
		if got := planVerdictFor(tc.streak); got != tc.want {
			t.Errorf("streak %d: want %d, got %d", tc.streak, tc.want, got)
		}
	}
}
