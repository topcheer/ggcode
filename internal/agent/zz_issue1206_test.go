package agent

import (
	"strings"
	"testing"
)

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

func TestIssue1206_ToolHintBytesStarveDetectorGuidance(t *testing.T) {
	a, _ := issue677Agent(t)

	toolHint := "[tool-fallback] " + strings.Repeat("z", 1900)
	if !a.guidanceBudget.allowDeduped(toolHint) {
		t.Fatal("tool hint should pass within the byte pool")
	}

	detectorMsg := "[error-rush] " + strings.Repeat("w", 300)
	if a.injectGuidance(detectorMsg) {
		t.Fatal("detector guidance must be gated by tool-hint bytes in the shared pool (1900+300 > 2048)")
	}

	if a.guidanceBudget.injected != 1 {
		t.Fatalf("injected = %d, want 1 (tool hint's slot; byte suppression must not consume the count budget)", a.guidanceBudget.injected)
	}
}

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
