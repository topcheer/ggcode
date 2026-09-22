package agent

import (
	"strings"
	"testing"
)

// sa-37 consumption-loop tests: the budget's elastic byte pools adapt from
// the starvation the detector ledger measures at the choke point, and the
// ledger surfaces tags that fired but never delivered.

func TestGuidanceBudget_ElasticBytePoolGrowsAfterStarvation(t *testing.T) {
	var g guidanceBudget
	g.reset()

	// Two ~1109B hints total 2218B > base 2048B: the second is rejected for
	// bytes (the count cap allows 2/turn, so it does not fire first here) -
	// exactly the starvation signal adaptCaps consumes.
	big := strings.Repeat("x", 1100)
	if !g.allowDeduped("[hint-a] " + big) {
		t.Fatal("first hint should fit the base pool")
	}
	if g.allowDeduped("[hint-b] " + big) {
		t.Fatal("second hint should be byte-suppressed at base cap")
	}
	if g.suppressedBytesTurn == 0 {
		t.Fatal("expected byte-pool starvation")
	}

	g.reset() // turn boundary: adaptCaps consumes the measured starvation

	if want := guidanceBudgetBytesPerTurn + guidanceBudgetBytesPerTurn/guidanceByteCapGrowthDen; g.byteCap != want {
		t.Fatalf("after starved turn cap = %d, want %d", g.byteCap, want)
	}
	if g.effectiveByteCap() != g.byteCap {
		t.Fatalf("effectiveByteCap = %d, want %d", g.effectiveByteCap(), g.byteCap)
	}

	// The same load now fits entirely (count cap allows both).
	for i, tag := range []string{"a", "b"} {
		if !g.allowDeduped("[hint-" + tag + "] " + big) {
			t.Fatalf("hint %d should fit the grown pool", i)
		}
	}
}

func TestGuidanceBudget_ElasticBytePoolBoundedAndDecays(t *testing.T) {
	var g guidanceBudget
	g.reset()

	// Saturate turns repeatedly with 2x ~2508B hints (5016B/turn demand):
	// growth must stop at 2x base since even the grown pool cannot fit both.
	big := strings.Repeat("x", 2500)
	for turn := 0; turn < 6; turn++ {
		g.allowDeduped("[flood-a] " + big)
		g.allowDeduped("[flood-b] " + big)
		g.reset()
	}
	if max := guidanceBudgetBytesPerTurnMax(); g.byteCap != max {
		t.Fatalf("after repeated starvation cap = %d, want bounded %d", g.byteCap, max)
	}

	// Clean turns decay the pool back down to base.
	for i := 0; i < 10 && g.byteCap > guidanceBudgetBytesPerTurn; i++ {
		g.allowDeduped("[small] ok")
		g.reset()
	}
	if g.byteCap != guidanceBudgetBytesPerTurn {
		t.Fatalf("after clean turns cap = %d, want base %d", g.byteCap, guidanceBudgetBytesPerTurn)
	}
}

func TestGuidanceBudget_CriticalPoolElasticGrowth(t *testing.T) {
	var g guidanceBudget
	g.reset()

	// Distinct critical tags stream past the dedicated 1024B pool.
	big := strings.Repeat("y", 400)
	tags := []string{"CRITICAL", "SECURITY", "SAFETY", "PERMISSION"}
	for _, tag := range tags {
		g.allowDeduped("[" + tag + "] " + big)
	}
	if g.suppressedCriticalTurn == 0 {
		t.Fatal("expected critical-pool starvation")
	}

	g.reset()
	if want := guidanceBudgetCriticalBytesPerTurn + guidanceBudgetCriticalBytesPerTurn/guidanceByteCapGrowthDen; g.criticalCap != want {
		t.Fatalf("after starved turn critical cap = %d, want %d", g.criticalCap, want)
	}
}

func TestDetectorLedgerStarvationReport(t *testing.T) {
	var l detectorLedger
	l.reset()

	l.setTurn(1)
	l.noteDelivered("healthy", 100)
	l.noteSuppressed("healthy", rejectDedup) // delivered at least once: NOT starved
	l.noteSuppressed("starved-bytes", rejectBudgetBytes)
	l.noteSuppressed("starved-bytes", rejectBudgetBytes)
	l.noteSuppressed("starved-count", rejectBudgetCount)

	l.mu.Lock()
	got := l.starvationReportLocked()
	l.mu.Unlock()

	if got == "" {
		t.Fatal("expected non-empty starvation report")
	}
	for _, want := range []string{"2 tag(s)", "[starved-bytes]x2", "[starved-count]x1"} {
		if !strings.Contains(got, want) {
			t.Errorf("report %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "healthy") {
		t.Errorf("report %q must exclude delivered tags (healthy)", got)
	}

	// All-delivered ledger renders "".
	var clean detectorLedger
	clean.reset()
	clean.noteDelivered("ok", 10)
	clean.mu.Lock()
	if s := clean.starvationReportLocked(); s != "" {
		t.Errorf("clean ledger starvation report = %q, want \"\"", s)
	}
	clean.mu.Unlock()
}

func TestAgentGuidanceStarvationReportNilSafe(t *testing.T) {
	var a Agent
	// zero-value Agent: ledger is a value struct with nil map; report is "".
	if s := a.GuidanceStarvationReport(); s != "" {
		t.Errorf("zero-value agent starvation report = %q, want \"\"", s)
	}
}
