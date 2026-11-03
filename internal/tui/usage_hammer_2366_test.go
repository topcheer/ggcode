package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/usage"
)

type countingProbe struct {
	vendor string
	calls  atomic.Int64
	rl     atomic.Bool
}

func (c *countingProbe) MatchesURL(baseURL string) bool {
	return strings.Contains(baseURL, "open.bigmodel.cn")
}

func (p *countingProbe) Vendor() string { return p.vendor }

func (p *countingProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*usage.UsageInfo, error) {
	p.calls.Add(1)
	if p.rl.Load() {
		return nil, &usage.RateLimitedError{RetryAfter: 10 * time.Minute}
	}
	return &usage.UsageInfo{Vendor: p.vendor}, nil
}

func TestRefreshUsagePanelKeepsNegativeCache(t *testing.T) {
	svc := usage.NewService()
	probe := &countingProbe{vendor: "zai"}
	svc.Register(probe)

	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["zai"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", APIKey: "k", DefaultModel: "glm-5"},
	}}
	m.startupVendor = "zai"
	m.activeEndpoint = "e"
	m.SetConfig(cfg)
	m.usageService = svc
	m.usagePanel = &usagePanelState{
		infos: map[string]*usage.UsageInfo{},
		errs:  map[string]string{},
	}

	probe.rl.Store(true)
	if _, err := svc.Get(context.Background(), "zai", "", "k"); err == nil {
		t.Fatal("expected 429 error")
	}
	if n := probe.calls.Load(); n != 1 {
		t.Fatalf("setup: probe calls = %d, want 1", n)
	}

	_ = m.refreshUsagePanel()()
	if n := probe.calls.Load(); n != 1 {
		t.Fatalf("r-refresh re-probed a Retry-After negative-cached vendor: %d calls", n)
	}

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
