package agentruntime

// zz_issue543_544_test.go — feature test for issue #543 (agentruntime half).
//
// ApplyToolCallBudget must ALWAYS propagate the configured budget, including
// 0. Previously `if cfg.ToolCallBudget > 0` skipped zero values, so removing
// tool_call_budget from the config in a hot reload left the old explicit
// budget applied to the agent until restart. With the fix, 0 clears the
// explicit budget and auto-derivation from maxIter applies — the same
// always-call semantics as ApplySessionTimeout.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue543ApplyToolCallBudgetZeroResets(t *testing.T) {
	ag := agent.NewAgent(nil, nil, "sys", 5)
	defer ag.Close()

	// Apply an explicit budget.
	cfg := &config.Config{ToolCallBudget: 77}
	ApplyToolCallBudget(ag, cfg)
	if got := ag.ToolCallBudget(); got != 77 {
		t.Fatalf("after ApplyToolCallBudget(77): want 77, got %d", got)
	}

	// Config reload that REMOVES the budget (0): must reset, not keep 77.
	cfgNoBudget := &config.Config{ToolCallBudget: 0}
	ApplyToolCallBudget(ag, cfgNoBudget)
	if got := ag.ToolCallBudget(); got != 0 {
		t.Fatalf("after ApplyToolCallBudget(0): want 0 (reset for auto-derive), got %d", got)
	}
}

func TestIssue543ApplyToolCallBudgetNilSafety(t *testing.T) {
	// Nil agent/config must be no-ops, not panics.
	ApplyToolCallBudget(nil, &config.Config{ToolCallBudget: 5})
	ag := agent.NewAgent(nil, nil, "sys", 5)
	defer ag.Close()
	ApplyToolCallBudget(ag, nil)
	if got := ag.ToolCallBudget(); got != 0 {
		t.Fatalf("nil config must leave budget untouched, got %d", got)
	}
}
