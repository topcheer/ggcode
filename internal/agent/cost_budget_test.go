package agent

import (
	"strings"
	"testing"
)

func TestSessionCostBudget_NoBudget(t *testing.T) {
	c := newSessionCostBudget()
	c.SetBudget(0) // unlimited

	// Record a lot of tokens
	for i := 0; i < 100; i++ {
		c.recordStep(10000, 5000)
	}
	msg, stop := c.check()
	if msg != "" {
		t.Errorf("expected no message with unlimited budget, got %q", msg)
	}
	if stop {
		t.Error("expected no stop with unlimited budget")
	}
}

func TestSessionCostBudget_ProgressiveWarnings(t *testing.T) {
	c := newSessionCostBudget()
	c.SetBudget(1_000_000) // 1M tokens

	// Step 1: below 75% threshold — no message
	c.recordStep(100000, 50000) // 150K total = 15%
	msg, stop := c.check()
	if msg != "" {
		t.Errorf("expected no message at 15%%, got %q", msg)
	}
	if stop {
		t.Error("expected no stop at 15%")
	}

	// Step 2: cross both 75% and 90% — the 90% fires first (higher priority)
	c.recordStep(400000, 400000) // 150K + 800K = 950K = 95%
	msg, _ = c.check()
	if msg == "" {
		t.Error("expected 90% warning message, got empty")
	}
	if !strings.Contains(msg, "Approaching") {
		t.Errorf("expected 90%% threshold (Approaching) in message, got %q", msg)
	}

	// Check again — 75% fires next
	msg, stop = c.check()
	if msg == "" {
		t.Error("expected 75% warning message, got empty")
	}
	if !strings.Contains(msg, "75%") {
		t.Errorf("expected 75%% in message, got %q", msg)
	}
	if stop {
		t.Error("expected no stop at 95% (only at 100%)")
	}

	// Step 3: cross 100% — should stop
	c.recordStep(60000, 10000) // 950K + 70K = 1.02M > 1M budget
	msg, stop = c.check()
	if msg == "" {
		t.Error("expected stop message, got empty")
	}
	if !strings.Contains(msg, "exhausted") {
		t.Errorf("expected 'exhausted' in message, got %q", msg)
	}
	if !stop {
		t.Error("expected stop=true at 100%+")
	}
}

func TestSessionCostBudget_ThresholdsFireOnceEach(t *testing.T) {
	c := newSessionCostBudget()
	c.SetBudget(1_000_000)

	// Cross 75%
	c.recordStep(800000, 0) // 80%
	msg, _ := c.check()
	if msg == "" {
		t.Error("expected 75% warning")
	}

	// Check again — should not fire again
	msg, _ = c.check()
	if msg != "" {
		t.Errorf("expected no duplicate 75%% warning, got %q", msg)
	}

	// Cross 90%
	c.recordStep(150000, 0) // 95%
	msg, _ = c.check()
	if msg == "" {
		t.Error("expected 90% warning")
	}

	// Check again — should not fire again
	msg, _ = c.check()
	if msg != "" {
		t.Errorf("expected no duplicate 90%% warning, got %q", msg)
	}

	// Cross 100%
	c.recordStep(100000, 0) // 105%
	msg, stop := c.check()
	if msg == "" || !stop {
		t.Error("expected stop message with stop=true")
	}

	// Check again — should not fire again
	msg, stop = c.check()
	if msg != "" || stop {
		t.Errorf("expected no duplicate stop, got msg=%q stop=%v", msg, stop)
	}
}

func TestSessionCostBudget_Reset(t *testing.T) {
	c := newSessionCostBudget()
	c.SetBudget(1_000_000)

	// Consume past 75%
	c.recordStep(800000, 0)
	c.check() // fires 75% warning

	c.reset()

	// After reset, state should be clean
	c.recordStep(800000, 0)
	msg, _ := c.check()
	if msg == "" {
		t.Error("expected warning to fire again after reset")
	}
	if c.totalTokens != 800000 {
		t.Errorf("expected totalTokens=800000 after reset+record, got %d", c.totalTokens)
	}
}

func TestSessionCostBudget_Exactly100Percent(t *testing.T) {
	c := newSessionCostBudget()
	c.SetBudget(1_000_000)

	// Reach exactly 100%
	c.recordStep(600000, 400000) // 1M = 100%

	// Stop fires first (highest priority)
	msg, stop := c.check()
	if !stop {
		t.Error("expected stop at exactly 100%")
	}
	if !strings.Contains(msg, "exhausted") {
		t.Errorf("expected exhausted in message, got %q", msg)
	}

	// 90% fires next
	msg, _ = c.check()
	if !strings.Contains(msg, "Approaching") {
		t.Errorf("expected 90%% threshold (Approaching) next, got %q", msg)
	}

	// 75% fires last
	msg, _ = c.check()
	if !strings.Contains(msg, "75%") {
		t.Errorf("expected 75%% last, got %q", msg)
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{500, "500"},
		{1500, "1.5K"},
		{50000, "50.0K"},
		{1_500_000, "1.5M"},
		{50_000_000, "50.0M"},
	}
	for _, tc := range tests {
		got := formatTokenCount(tc.input)
		if got != tc.expected {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
