package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue543ApplyToolCallBudgetZeroResets(t *testing.T) {
	ag := agent.NewAgent(nil, nil, "sys", 5)
	defer ag.Close()

	cfg := &config.Config{ToolCallBudget: 77}
	ApplyToolCallBudget(ag, cfg)
	if got := ag.ToolCallBudget(); got != 77 {
		t.Fatalf("after ApplyToolCallBudget(77): want 77, got %d", got)
	}

	cfgNoBudget := &config.Config{ToolCallBudget: 0}
	ApplyToolCallBudget(ag, cfgNoBudget)
	if got := ag.ToolCallBudget(); got != 0 {
		t.Fatalf("after ApplyToolCallBudget(0): want 0 (reset for auto-derive), got %d", got)
	}
}

func TestIssue543ApplyToolCallBudgetNilSafety(t *testing.T) {

	ApplyToolCallBudget(nil, &config.Config{ToolCallBudget: 5})
	ag := agent.NewAgent(nil, nil, "sys", 5)
	defer ag.Close()
	ApplyToolCallBudget(ag, nil)
	if got := ag.ToolCallBudget(); got != 0 {
		t.Fatalf("nil config must leave budget untouched, got %d", got)
	}
}
