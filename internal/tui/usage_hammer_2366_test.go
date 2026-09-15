package tui

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/usage"
)

// countingProbe lets the test script probe results and count real Fetch
// calls through the Service (#2366-① hammer regression).
type countingProbe struct {
	vendor string
	calls  atomic.Int64
	rl     atomic.Bool // true -> 429-shaped error
}

// MatchesURL lets the counting fake participate in URL-based probe
// resolution (owner ruling: adapters are chosen by endpoint URL).
func (c *countingProbe) MatchesURL(host string) bool {
	return host == "open.bigmodel.cn"
}

func (p *countingProbe) Vendor() string { return p.vendor }

func (p *countingProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*usage.UsageInfo, error) {
	p.calls.Add(1)
	if p.rl.Load() {
		return nil, &usage.RateLimitedError{RetryAfter: 10 * time.Minute}
	}
	return &usage.UsageInfo{Vendor: p.vendor}, nil
}

// TestRefreshUsagePanelKeepsNegativeCache verifies #2366-①: after a vendor
// gets 429'd (negative cache row, Retry-After-sized), pressing "r" must NOT
// re-probe it - the old implementation dropped the whole Service per
// refresh, so N rapid r-presses meant N unbackoffed full re-probes. The
// refresh invalidates only SUCCESS rows.
func TestRefreshUsagePanelKeepsNegativeCache(t *testing.T) {
	svc := usage.NewService()
	probe := &countingProbe{vendor: "zai"}
	svc.Register(probe)

	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["zai"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {BaseURL: "https://open.bigmodel.cn/api/paas/v4", APIKey: "k"},
	}}
	m.startupVendor = "zai"
	m.activeEndpoint = "e"
	m.SetConfig(cfg)
	m.usageService = svc
	m.usagePanel = &usagePanelState{
		infos: map[string]*usage.UsageInfo{},
		errs:  map[string]string{},
	}

	// Vendor is rate-limited: first Get seeds the Retry-After-sized
	// negative cache row (1 probe call).
	probe.rl.Store(true)
	if _, err := svc.Get(context.Background(), "zai", "", "k"); err == nil {
		t.Fatal("expected 429 error")
	}
	if n := probe.calls.Load(); n != 1 {
		t.Fatalf("setup: probe calls = %d, want 1", n)
	}

	// r-refresh: the rate-limited vendor must be served from the negative
	// cache, not re-hit.
	_ = m.refreshUsagePanel()()
	if n := probe.calls.Load(); n != 1 {
		t.Fatalf("r-refresh re-probed a Retry-After negative-cached vendor: %d calls", n)
	}

	// A SUCCESS row IS invalidated on refresh: clear the negative row
	// (full Invalidate simulates the window elapsing), flip the probe to
	// ok, prime the success cache, refresh -> probe count must grow.
	svc.Invalidate("zai")
	probe.rl.Store(false)
	if _, err := svc.Get(context.Background(), "zai", "", "k"); err != nil {
		t.Fatalf("ok probe: %v", err)
	}
	_ = m.refreshUsagePanel()()
	if n := probe.calls.Load(); n <= 2 {
		t.Fatalf("r-refresh did not re-probe after success invalidation: %d calls", n)
	}
}
