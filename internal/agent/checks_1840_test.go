package agent

import (
	"strings"
	"testing"
)

// #1840 case 1 pin: a budget-rejected hint must not occupy its dedup slot.
func Test1840DedupSlotOnlyOnDelivery(t *testing.T) {
	g := &guidanceBudget{injected: guidanceBudgetPerTurn} // count cap reached
	long := "[loop-guard] " + strings.Repeat("x", 100)
	if g.allowDeduped(long) {
		t.Fatal("precondition: count cap must reject")
	}
	// The rejected hint must NOT have taken its dedup slot.
	if g.seenHintTags["loop-guard"] {
		t.Fatal("rejected hint must not occupy the dedup slot")
	}
	// Positive control: a delivered hint DOES take it.
	if !g.allowDeduped("[hardcoded-secret] critical passes the count cap") {
		t.Fatal("critical must bypass the count cap")
	}
	if !g.seenHintTags["hardcoded-secret"] {
		t.Fatal("delivered hint must occupy the dedup slot")
	}
	if g.allowDeduped("[hardcoded-secret] duplicate copy") {
		t.Fatal("delivered tag must dedup later copies")
	}
}

// #1840 case 2 pin: critical hints have a dedicated byte pool - a full
// advisory pool cannot starve them.
func Test1840CriticalDedicatedPool(t *testing.T) {
	g := &guidanceBudget{}
	g.appendedBytes = guidanceBudgetBytesPerTurn // advisory pool FULL
	if !g.allowDeduped("[hardcoded-secret] critical notice") {
		t.Fatal("critical must not starve behind a full advisory pool")
	}
	// Flood of critical is still bounded by the dedicated pool.
	g2 := &guidanceBudget{}
	big := "[hardcoded-secret] " + strings.Repeat("y", guidanceBudgetCriticalBytesPerTurn-100)
	if !g2.allowDeduped(big) {
		t.Fatal("first big critical fits the dedicated pool")
	}
	if g2.allowDeduped("[SAFETY] " + strings.Repeat("z", 100)) {
		t.Fatal("dedicated critical pool must still bound floods")
	}
}

// #1840 case 3 pin: same-tag dedup loss is visible.
func Test1840DedupLossVisible(t *testing.T) {
	hints := []string{
		"[loop-guard] first content",
		"[loop-guard] different content same tag",
		"[other] fine",
	}
	out, dropped := dedupByTag(hints)
	if dropped != 1 || len(out) != 2 {
		t.Fatalf("dedup wrong: out=%d dropped=%d", len(out), dropped)
	}
	notated := noteDedupDropped(out, dropped)
	joined := strings.Join(notated, "\n")
	if !strings.Contains(joined, "duplicate tags") {
		t.Fatalf("loss must be visible, got %q", joined)
	}
}

// #1840 case 4 pin: iteration-level guidance joins conflict arbitration.
func Test1840ConflictAcrossPaths(t *testing.T) {
	g := &guidanceBudget{}
	g.delivered = []string{"Error rush: ACT NOW and commit to a fix."}
	ch := detectGuidanceConflict(append(append([]string{}, g.delivered...), "Recommendation: EXPLORE alternatives before editing."))
	if ch == "" {
		t.Fatal("ACT NOW vs EXPLORE across paths must be arbitrated")
	}
}
