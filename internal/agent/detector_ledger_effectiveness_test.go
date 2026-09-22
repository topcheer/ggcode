package agent

import (
	"strings"
	"testing"
)

// sa-38: delivery-effectiveness (post-guidance recurrence) tests.
//
// The ledger's delivered/suppressed counters answer "did guidance get
// through"; the Recurrences/FirstDeliveryTurn/LastRecurrenceTurn fields
// answer "did it WORK" - a firing at a later turn than the first delivery
// means the same problem recurred after the guidance was injected
// (performative compliance, cf. arXiv:2509.25370).

func TestLedgerEffectivenessRecurrenceAcrossTurns(t *testing.T) {
	l := &detectorLedger{}
	l.setTurn(3)
	l.noteDelivered("edit-oscillation", 100)
	l.setTurn(6)
	l.noteDelivered("edit-oscillation", 80) // fired again -> recurrence
	l.setTurn(8)
	l.noteSuppressed("edit-oscillation", rejectBudgetBytes) // fired again -> recurrence
	// heeded tag: delivered once, never fires again
	l.setTurn(2)
	l.noteDelivered("spec-gaming", 50)

	rows := l.snapshot()
	var osc, spec detectorLedgerRow
	for _, r := range rows {
		switch r.Tag {
		case "edit-oscillation":
			osc = r
		case "spec-gaming":
			spec = r
		}
	}
	if osc.Recurrences != 2 || osc.LastRecurrenceTurn != 8 {
		t.Fatalf("edit-oscillation: got Recurrences=%d LastRecurrenceTurn=%d, want 2/8", osc.Recurrences, osc.LastRecurrenceTurn)
	}
	if osc.FirstDeliveryTurn != 3 {
		t.Fatalf("FirstDeliveryTurn=%d, want 3", osc.FirstDeliveryTurn)
	}
	if spec.Recurrences != 0 || spec.FirstDeliveryTurn != 2 {
		t.Fatalf("spec-gaming: got Recurrences=%d FirstDeliveryTurn=%d, want 0/2", spec.Recurrences, spec.FirstDeliveryTurn)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	rep := l.effectivenessReportLocked()
	if !strings.Contains(rep, "delivery effectiveness: 1/2 delivered tag(s) recurred") {
		t.Fatalf("report missing recurrence ratio:\n%s", rep)
	}
	if !strings.Contains(rep, "[edit-oscillation] recurred x2 (delivered turn 3, last turn 8)") {
		t.Fatalf("report missing recurrence detail:\n%s", rep)
	}
	if strings.Contains(rep, "all heeded") {
		t.Fatalf("report must not claim all-heeded when a tag recurred:\n%s", rep)
	}
}

func TestLedgerEffectivenessSameTurnRejectionsNotRecurrence(t *testing.T) {
	l := &detectorLedger{}
	l.setTurn(4)
	l.noteDelivered("dedup-tag", 60)
	l.noteSuppressed("dedup-tag", rejectDedup)       // same turn: budget artifact
	l.noteSuppressed("dedup-tag", rejectBudgetBytes) // same turn: budget artifact
	l.setTurn(5)
	l.noteDelivered("dedup-tag", 40) // later turn: real recurrence

	rows := l.snapshot()
	if len(rows) != 1 || rows[0].Recurrences != 1 {
		t.Fatalf("want 1 recurrence (same-turn rejections excluded, later delivery counted), got %+v", rows)
	}
	if rows[0].LastRecurrenceTurn != 5 {
		t.Fatalf("LastRecurrenceTurn=%d, want 5", rows[0].LastRecurrenceTurn)
	}
}

func TestLedgerEffectivenessAllHeeded(t *testing.T) {
	l := &detectorLedger{}
	l.setTurn(1)
	l.noteDelivered("scope-drift", 30)
	l.setTurn(2)
	l.noteDelivered("spec-gaming", 40)
	l.setTurn(9) // run goes on, nobody fires again

	l.mu.Lock()
	defer l.mu.Unlock()
	rep := l.effectivenessReportLocked()
	if !strings.Contains(rep, "delivery effectiveness: 0/2 delivered tag(s) recurred post-guidance (all heeded)") {
		t.Fatalf("want all-heeded line, got:\n%s", rep)
	}
}

func TestLedgerEffectivenessEmptyWhenNothingDelivered(t *testing.T) {
	l := &detectorLedger{}
	l.setTurn(3)
	l.noteSuppressed("starved-tag", rejectBudgetCount) // starvation population: nothing to evaluate

	l.mu.Lock()
	defer l.mu.Unlock()
	if rep := l.effectivenessReportLocked(); rep != "" {
		t.Fatalf("want empty report when nothing delivered, got:\n%s", rep)
	}
}

// Pre-loop deliveries (turn 0, e.g. context build) still anchor recurrence:
// any firing at turn >= 1 counts as post-guidance.
func TestLedgerEffectivenessPreLoopDelivery(t *testing.T) {
	l := &detectorLedger{} // turn stays 0
	l.noteDelivered("pre-loop-tag", 20)
	l.setTurn(1)
	l.noteDelivered("pre-loop-tag", 20)

	rows := l.snapshot()
	if rows[0].Recurrences != 1 || rows[0].FirstDeliveryTurn != 0 {
		t.Fatalf("pre-loop delivery: got Recurrences=%d FirstDeliveryTurn=%d, want 1/0", rows[0].Recurrences, rows[0].FirstDeliveryTurn)
	}
}

// The public accessor used by /runreport must agree with the internal report
// and stay nil-safe (direct guidanceBudget constructions without wiring).
func TestLedgerEffectivenessAgentAccessor(t *testing.T) {
	a := &Agent{}
	if rep := a.GuidanceEffectivenessReport(); rep != "" {
		t.Fatalf("nil-safe accessor must return \"\", got %q", rep)
	}

	a2 := &Agent{}
	a2.detectorLedger.setTurn(2)
	a2.detectorLedger.noteDelivered("tag-a", 10)
	l := &a2.detectorLedger
	l.mu.Lock()
	l.mu.Unlock()
	if rep := a2.GuidanceEffectivenessReport(); !strings.Contains(rep, "all heeded") {
		t.Fatalf("accessor should render all-heeded for a delivered tag:\n%s", rep)
	}
}
