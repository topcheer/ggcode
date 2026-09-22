package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// demoteHint is a generic advisory hint with a repeatable tag.
func demoteHint(tag string) string {
	return "[" + tag + "] example advisory hint text"
}

// TestGuidanceRepeatGate_DemotesAfterCap drives the full appendGuidance path
// across turns: the tag delivers guidanceTagMaxDeliveries times, then the
// next hint becomes the one-time [guidance-paused] notice, later repeats are
// dropped silently, and the demoted tag consumes no budget slots.
func TestGuidanceRepeatGate_DemotesAfterCap(t *testing.T) {
	a := &Agent{}
	const tag = "momentum-loss"
	hint := demoteHint(tag)

	delivered := 0
	for turn := 0; turn < guidanceTagMaxDeliveries; turn++ {
		a.guidanceBudget.reset() // new agent iteration
		res := &tool.Result{Content: "tool output"}
		if !a.appendGuidance(res, hint) {
			t.Fatalf("turn %d: hint should be delivered before the cap", turn)
		}
		if !strings.Contains(res.Content, "["+tag+"]") {
			t.Fatalf("turn %d: delivered content lost the hint", turn)
		}
		delivered++
	}
	if delivered != guidanceTagMaxDeliveries {
		t.Fatalf("expected %d deliveries, got %d", guidanceTagMaxDeliveries, delivered)
	}

	// Turn cap+1: the first blocked repeat is replaced by the notice.
	a.guidanceBudget.reset()
	res := &tool.Result{Content: "tool output"}
	if !a.appendGuidance(res, hint) {
		t.Fatal("first blocked repeat should deliver the [guidance-paused] notice")
	}
	if !strings.Contains(res.Content, "[guidance-paused]") ||
		!strings.Contains(res.Content, tag) {
		t.Fatalf("expected one-time pause notice, got: %q", res.Content)
	}
	if strings.Contains(res.Content, "["+tag+"]") {
		t.Fatal("the repeat itself must not be delivered, only the notice")
	}

	// Turn cap+2: silent drop, no notice repeat.
	a.guidanceBudget.reset()
	if a.appendGuidance(&tool.Result{Content: "tool output"}, hint) {
		t.Fatal("later repeats should be dropped silently")
	}
	if got := a.guidanceBudget.injected; got != 0 {
		t.Fatalf("demoted repeats must consume no budget slots, injected=%d", got)
	}
	if got := a.guidanceBudget.appendedBytes; got != 0 {
		t.Fatalf("demoted repeats must charge no byte pool, appendedBytes=%d", got)
	}
}

// TestGuidanceRepeatGate_CriticalTagsNeverDemoted: safety guidance ignores
// the cap entirely.
func TestGuidanceRepeatGate_CriticalTagsNeverDemoted(t *testing.T) {
	var g guidanceBudget
	g.reset()
	// Critical pool (1024 bytes) fits one hint per turn loop; reset turns to
	// bypass per-turn dedup while keeping the ledger across turns.
	for i := 0; i < guidanceTagMaxDeliveries+3; i++ {
		g.reset() // new turn: per-turn dedup resets, the demote ledger stays
		text := "[hardcoded-secret] critical hint"
		if g.filterDemoted(text) != text {
			t.Fatalf("delivery %d: critical hint must never be demoted", i)
		}
		if !g.allowDeduped(text) {
			t.Fatalf("delivery %d: critical hint should pass (critical pool)", i)
		}
	}
}

// TestGuidanceRepeatGate_UntaggedAndBudgetRejectedNotCounted: only real
// deliveries consume the cap.
func TestGuidanceRepeatGate_UntaggedAndBudgetRejectedNotCounted(t *testing.T) {
	var g guidanceBudget
	g.reset()
	g.initDemote()

	// Untagged hints pass through the filter unchanged.
	untagged := "plain advisory text without a tag"
	if g.filterDemoted(untagged) != untagged {
		t.Fatal("untagged hints must pass the gate unchanged")
	}

	// Budget-rejected attempts must not consume the tag's cap: exhaust the
	// per-turn advisory slots, then hammer the gate with the same tag.
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		// Distinct tags: same-tag fillers would hit the per-turn dedup
		// (#607 B3) instead of consuming the advisory slots under test.
		g.allowDeduped("[filler-" + string(rune('A'+i)) + "] filler")
	}
	rejected := "[repeated-tag] never actually delivered"
	for i := 0; i < guidanceTagMaxDeliveries*2; i++ {
		if g.allowDeduped(rejected) {
			t.Fatal("hint beyond the per-turn budget should be rejected")
		}
	}
	g.reset() // next turn: budget slots back
	hint := demoteHint("repeated-tag")
	for i := 0; i < guidanceTagMaxDeliveries; i++ {
		g.reset()
		if !g.allowDeduped(hint) {
			t.Fatalf("delivery %d: budget-rejected turns must not consume the cap", i)
		}
	}
	// The cap is only reached now, not earlier.
	if !g.demote.demoted("repeated-tag") {
		t.Fatal("tag should demote only after guidanceTagMaxDeliveries real deliveries")
	}
	if first := g.filterDemoted(hint); !strings.Contains(first, "[guidance-paused]") {
		t.Fatalf("expected pause notice after cap, got %q", first)
	}
}

// TestGuidanceRepeatGate_CompactionReset: the compaction reset clears the
// ledger so the cap restarts (the model can no longer see earlier copies).
func TestGuidanceRepeatGate_CompactionReset(t *testing.T) {
	a := &Agent{}
	hint := demoteHint("edit-oscillation")
	for i := 0; i <= guidanceTagMaxDeliveries; i++ {
		a.guidanceBudget.reset()
		a.appendGuidance(&tool.Result{Content: "x"}, hint)
	}
	if !a.guidanceBudget.demote.demoted("edit-oscillation") {
		t.Fatal("tag should be demoted after cap+1 arrivals")
	}
	// Simulate the post-compaction reset registered in guidanceCounterResets.
	a.guidanceBudget.demote.reset()
	if a.guidanceBudget.demote.demoted("edit-oscillation") {
		t.Fatal("compaction reset must clear the demotion ledger")
	}
	a.guidanceBudget.reset()
	if !a.appendGuidance(&tool.Result{Content: "x"}, hint) {
		t.Fatal("after compaction reset the tag should deliver again")
	}
}

// TestGuidanceRepeatGate_ResetRegistryEntry pins that the compaction reset
// table covers the repeat-gate ledger (same mechanical guard as #1826).
func TestGuidanceRepeatGate_ResetRegistryEntry(t *testing.T) {
	a := &Agent{}
	a.guidanceBudget.initDemote()
	a.guidanceBudget.demote.noteDelivery("some-tag")
	a.guidanceBudget.demote.noteDelivery("some-tag")
	a.guidanceBudget.demote.noteDelivery("some-tag")
	a.guidanceBudget.demote.noteDelivery("some-tag")
	if !a.guidanceBudget.demote.demoted("some-tag") {
		t.Fatal("precondition: tag should be demoted")
	}
	a.resetGuidanceCounters()
	if a.guidanceBudget.demote.demoted("some-tag") {
		t.Fatal("resetGuidanceCounters must clear the repeat-gate ledger")
	}
}

// TestGuidanceRepeatGate_PerTurnResetKeepsLedger: the per-iteration reset()
// must not clear the run-scoped ledger.
func TestGuidanceRepeatGate_PerTurnResetKeepsLedger(t *testing.T) {
	var g guidanceBudget
	g.reset()
	g.initDemote()
	for i := 0; i < guidanceTagMaxDeliveries; i++ {
		g.reset()
		if !g.allowDeduped(demoteHint("stuck-tag")) {
			t.Fatalf("delivery %d should pass", i)
		}
	}
	g.reset() // per-turn reset only
	if !g.demote.demoted("stuck-tag") {
		t.Fatal("per-turn reset must not clear the run-scoped demotion ledger")
	}
}
