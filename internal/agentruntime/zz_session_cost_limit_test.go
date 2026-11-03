package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestApplySessionCostLimitSubscriptionAndReset(t *testing.T) {
	cfg := &config.Config{Vendor: "kimi", Endpoint: "kimi", Model: "kimi-k2"}
	a := agent.NewAgent(nil, tool.NewRegistry(), "", 10)
	defer a.Close()

	ApplySessionCostLimit(a, cfg)
	if got := a.SessionCostLimit(); got != 0 {
		t.Fatalf("limit with cfg.SessionCostLimitUSD=0 should stay disabled, got %v", got)
	}

	cfg.SessionCostLimitUSD = 5.0
	ApplySessionCostLimit(a, cfg)
	if got := a.SessionCostLimit(); got != 5.0 {
		t.Fatalf("SessionCostLimit = %v, want 5.0", got)
	}

	if msg, stop := a.RecordSessionCostUsage(10_000_000, 10_000_000, 0, 0); msg != "" || stop {
		t.Fatalf("subscription model must accumulate $0, got msg=%q stop=%v", msg, stop)
	}
	if got := a.SessionCostSpent(); got != 0 {
		t.Fatalf("subscription spend = %v, want 0", got)
	}

	cfg.SessionCostLimitUSD = 0
	ApplySessionCostLimit(a, cfg)
	if got := a.SessionCostLimit(); got != 0 {
		t.Fatalf("reset semantics broken: SessionCostLimit = %v, want 0", got)
	}
}
