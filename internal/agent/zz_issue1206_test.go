package agent

// Issue #1206 tests: guidance byte-cap accounting symmetry.
//
// injectGuidance was gated by guidanceBudgetBytesPerTurn but never charged
// it: tool-result hints (allowDeduped) filled the pool while iteration-level
// detector guidance only read it. A tool-error-heavy turn could silently
// starve every subsequent detector advisory, and unlimited iteration-level
// injections never filled the pool for tool hints either.
//
// Fixed by charging the shared pool on every successful injectGuidance
// delivery, exactly like the tool-hint path.

import (
	"strings"
	"testing"
)

// injectGuidance must CHARGE the shared byte pool on delivery: a first large
// injection consumes the budget that a later (count-legal) injection is then
// gated against.
func TestIssue1206_InjectGuidanceChargesBytePool(t *testing.T) {
	a, _ := issue677Agent(t)

	big := "[advisory-1206] " + strings.Repeat("x", 1500)
	if !a.injectGuidance(big) {
		t.Fatal("first large injection should be within budget")
	}
	if got := a.guidanceBudget.appendedBytes; got != len(big) {
		t.Fatalf("appendedBytes = %d after injection, want %d (injectGuidance did not charge the pool)", got, len(big))
	}

	second := "[advisory-1206b] " + strings.Repeat("y", 700)
	if a.injectGuidance(second) {
		t.Fatal("second injection must be suppressed by the byte cap: prior injection consumed the shared pool")
	}
	if got := a.guidanceBudget.appendedBytes; got != len(big) {
		t.Fatalf("suppressed injection must not charge the pool; appendedBytes = %d, want %d", got, len(big))
	}
}

// Cross-path symmetry: bytes charged by the tool-result hint path
// (allowDeduped) must gate injectGuidance, proving one shared pool.
func TestIssue1206_ToolHintBytesStarveDetectorGuidance(t *testing.T) {
	a, _ := issue677Agent(t)

	toolHint := "[tool-fallback] " + strings.Repeat("z", 1900)
	if !a.guidanceBudget.allowDeduped(toolHint) {
		t.Fatal("tool hint should pass within the byte pool")
	}

	// Count budget untouched, but the shared byte pool is nearly full.
	detectorMsg := "[error-rush] " + strings.Repeat("w", 300)
	if a.injectGuidance(detectorMsg) {
		t.Fatal("detector guidance must be gated by tool-hint bytes in the shared pool (1900+300 > 2048)")
	}
	// The count budget is shared too (#1197 semantics: tool hints consume the
	// same allow() count) - the tool hint took count slot 1, and the byte-
	// suppressed detector message must not consume a slot.
	if a.guidanceBudget.injected != 1 {
		t.Fatalf("injected = %d, want 1 (tool hint's slot; byte suppression must not consume the count budget)", a.guidanceBudget.injected)
	}
}

// Reverse direction: iteration-level injections must fill the pool so a later
// tool hint is bounded by them.
func TestIssue1206_IterationBytesBoundLaterToolHints(t *testing.T) {
	a, _ := issue677Agent(t)

	first := "[advisory-a] " + strings.Repeat("a", 1500)
	if !a.injectGuidance(first) {
		t.Fatal("first advisory should deliver")
	}
	toolHint := "[tool-fallback] " + strings.Repeat("b", 700)
	if a.guidanceBudget.allowDeduped(toolHint) {
		t.Fatal("tool hint must be bounded by iteration-level injection bytes (1500+700 > 2048)")
	}
}

// Small injections within both caps must not interfere. Note the count
// budget is shared across both paths (guidanceBudgetPerTurn = 2): advisory
// + tool hint fill it, so a third injection is count-suppressed by design.
func TestIssue1206_SmallInjectionsCoexist(t *testing.T) {
	a, _ := issue677Agent(t)

	first := a.injectGuidance("[advisory-small-one] ok")
	tool := a.guidanceBudget.allowDeduped("[tool-hint-small] ok")
	if !first || !tool {
		t.Fatalf("small advisory (%v) and small tool hint (%v) should both deliver within byte+count budgets", first, tool)
	}
	want := len("[advisory-small-one] ok") + len("[tool-hint-small] ok")
	if got := a.guidanceBudget.appendedBytes; got != want {
		t.Fatalf("appendedBytes = %d, want %d (both paths charge the shared pool)", got, want)
	}
}
