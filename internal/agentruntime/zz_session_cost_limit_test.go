package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

// sa-56 companion: ApplySessionCostLimit rate resolution. The default
// pricing table ships subscription vendors (kimi, github-copilot, ...)
// and no universally-guaranteed metered entry, so the stable assertions
// here cover the disabled path, the subscription path (limit set but
// zero rates -> accumulates $0, no stop), and the always-call reset
// semantics (a reload removing the key must clear a previously applied cap).
func TestApplySessionCostLimitSubscriptionAndReset(t *testing.T) {
	cfg := &config.Config{Vendor: "kimi", Endpoint: "kimi", Model: "kimi-k2"}
	a := agent.NewAgent(nil, tool.NewRegistry(), "", 10)
	defer a.Close()

	ApplySessionCostLimit(a, cfg) // no panic; disabled -> limit stays 0
	if got := a.SessionCostLimit(); got != 0 {
		t.Fatalf("limit with cfg.SessionCostLimitUSD=0 should stay disabled, got %v", got)
	}

	cfg.SessionCostLimitUSD = 5.0
	ApplySessionCostLimit(a, cfg)
	if got := a.SessionCostLimit(); got != 5.0 {
		t.Fatalf("SessionCostLimit = %v, want 5.0", got)
	}
	// Subscription plan: zero metered rates -> huge usage accumulates $0.
	if msg, stop := a.RecordSessionCostUsage(10_000_000, 10_000_000, 0, 0); msg != "" || stop {
		t.Fatalf("subscription model must accumulate $0, got msg=%q stop=%v", msg, stop)
	}
	if got := a.SessionCostSpent(); got != 0 {
		t.Fatalf("subscription spend = %v, want 0", got)
	}

	// Reload removes the key: always-call semantics must reset the cap.
	cfg.SessionCostLimitUSD = 0
	ApplySessionCostLimit(a, cfg)
	if got := a.SessionCostLimit(); got != 0 {
		t.Fatalf("reset semantics broken: SessionCostLimit = %v, want 0", got)
	}
}
