package agent

import (
	"strings"
	"testing"
)

func TestSessionCostLimitThresholds(t *testing.T) {
	a := &Agent{}
	defer agentSessionCostLimits.Delete(a)

	a.SetSessionCostLimit(1.00, 10, 0, 0, 0)
	if got := a.SessionCostLimit(); got != 1.00 {
		t.Fatalf("SessionCostLimit() = %v, want 1.00", got)
	}

	if msg, stop := a.RecordSessionCostUsage(70_000, 0, 0, 0); msg != "" || stop {
		t.Fatalf("70%%: got msg=%q stop=%v, want none", msg, stop)
	}

	msg, stop := a.RecordSessionCostUsage(15_000, 0, 0, 0)
	if msg == "" || stop || !strings.Contains(msg, "80%") {
		t.Fatalf("80%%: got msg=%q stop=%v, want 80%% guidance no-stop", msg, stop)
	}

	msg, stop = a.RecordSessionCostUsage(10_000, 0, 0, 0)
	if msg == "" || stop || !strings.Contains(msg, "95%") {
		t.Fatalf("95%%: got msg=%q stop=%v, want 95%% guidance no-stop", msg, stop)
	}

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

	a.SetSessionCostLimit(0, 10, 10, 0, 0)
	if msg, stop := a.RecordSessionCostUsage(1_000_000, 0, 0, 0); msg != "" || stop {
		t.Fatalf("disabled: got msg=%q stop=%v, want none", msg, stop)
	}
	if got := a.SessionCostSpent(); got < 9.9 || got > 10.1 {
		t.Fatalf("disabled spend = %v, want ~10", got)
	}

	a.SetSessionCostLimit(0.01, 0, 0, 0, 0)
	if msg, stop := a.RecordSessionCostUsage(1_000_000, 1_000_000, 0, 0); msg != "" || stop {
		t.Fatalf("unpriced: got msg=%q stop=%v, want none", msg, stop)
	}

	a.SetSessionCostLimit(1.00, 10, 0, 0, 0)
	a.RecordSessionCostUsage(95_000, 0, 0, 0)
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

	a.SetSessionCostLimit(1.00, 0, 5, 1, 2)

	if msg, stop := a.RecordSessionCostUsage(0, 20_000, 100_000, 50_000); msg != "" || stop {
		t.Fatalf("30%%: got msg=%q stop=%v, want none", msg, stop)
	}
	if got := a.SessionCostSpent(); got < 0.29 || got > 0.31 {
		t.Fatalf("cache priced spend = %v, want ~0.30", got)
	}
}
