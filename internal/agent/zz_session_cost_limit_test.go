package agent

import (
	"strings"
	"testing"
)

// sa-56 companion test: session USD cost limit ladder mirrors the token
// budget thresholds (80% guidance / 95% urgent / 100% hard stop), prices
// turns from per-million rates, and no-ops when disabled or unpriced.
func TestSessionCostLimitThresholds(t *testing.T) {
	a := &Agent{}
	defer agentSessionCostLimits.Delete(a)

	// $1.00 limit; rate $10/M input tokens -> 100k tokens = $1.00.
	a.SetSessionCostLimit(1.00, 10, 0, 0, 0)
	if got := a.SessionCostLimit(); got != 1.00 {
		t.Fatalf("SessionCostLimit() = %v, want 1.00", got)
	}

	// 0%->70%: no message.
	if msg, stop := a.RecordSessionCostUsage(70_000, 0, 0, 0); msg != "" || stop {
		t.Fatalf("70%%: got msg=%q stop=%v, want none", msg, stop)
	}
	// 85%: first crossing fires the 80% guidance (values chosen to clear
	// the threshold decisively - 0.70+0.10 float-adds to 0.7999...).
	msg, stop := a.RecordSessionCostUsage(15_000, 0, 0, 0)
	if msg == "" || stop || !strings.Contains(msg, "80%") {
		t.Fatalf("80%%: got msg=%q stop=%v, want 80%% guidance no-stop", msg, stop)
	}
	// 95% boundary.
	msg, stop = a.RecordSessionCostUsage(10_000, 0, 0, 0)
	if msg == "" || stop || !strings.Contains(msg, "95%") {
		t.Fatalf("95%%: got msg=%q stop=%v, want 95%% guidance no-stop", msg, stop)
	}
	// Jump past 100%: hard stop.
	msg, stop = a.RecordSessionCostUsage(20_000, 0, 0, 0)
	if msg == "" || !stop || !strings.Contains(msg, "exhausted") {
		t.Fatalf("100%%: got msg=%q stop=%v, want exhausted stop", msg, stop)
	}
	if got := a.SessionCostSpent(); got < 1.14 || got > 1.16 {
		t.Fatalf("SessionCostSpent() = %v, want ~1.15", got)
	}
}

func TestSessionCostLimitDisabledAndUnpriced(t *testing.T) {
	a := &Agent{}
	defer agentSessionCostLimits.Delete(a)

	// No limit configured: spend accumulates silently for getters but never stops.
	a.SetSessionCostLimit(0, 10, 10, 0, 0)
	if msg, stop := a.RecordSessionCostUsage(1_000_000, 0, 0, 0); msg != "" || stop {
		t.Fatalf("disabled: got msg=%q stop=%v, want none", msg, stop)
	}
	if got := a.SessionCostSpent(); got < 9.9 || got > 10.1 {
		t.Fatalf("disabled spend = %v, want ~10", got)
	}

	// Limit set but zero rates (subscription/unknown pricing): no-op.
	a.SetSessionCostLimit(0.01, 0, 0, 0, 0)
	if msg, stop := a.RecordSessionCostUsage(1_000_000, 1_000_000, 0, 0); msg != "" || stop {
		t.Fatalf("unpriced: got msg=%q stop=%v, want none", msg, stop)
	}

	// Reset clears accumulation and threshold flags.
	a.SetSessionCostLimit(1.00, 10, 0, 0, 0)
	a.RecordSessionCostUsage(95_000, 0, 0, 0) // 95%
	a.resetSessionCostUsage()
	if got := a.SessionCostSpent(); got != 0 {
		t.Fatalf("after reset spend = %v, want 0", got)
	}
	if msg, stop := a.RecordSessionCostUsage(85_000, 0, 0, 0); !strings.Contains(msg, "80%") || stop {
		t.Fatalf("post-reset 85%%: got msg=%q stop=%v, want 80%% guidance again", msg, stop)
	}
}

func TestSessionCostLimitCachePricing(t *testing.T) {
	a := &Agent{}
	defer agentSessionCostLimits.Delete(a)

	// $1.00 limit; cache read $1/M, cache write $2/M, output $5/M.
	a.SetSessionCostLimit(1.00, 0, 5, 1, 2)
	// 100k cache-read ($0.10) + 50k cache-write ($0.10) + 20k output ($0.10) = 30%.
	if msg, stop := a.RecordSessionCostUsage(0, 20_000, 100_000, 50_000); msg != "" || stop {
		t.Fatalf("30%%: got msg=%q stop=%v, want none", msg, stop)
	}
	if got := a.SessionCostSpent(); got < 0.29 || got > 0.31 {
		t.Fatalf("cache priced spend = %v, want ~0.30", got)
	}
}
