package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/usage"
)

func TestUsageFetchDenominatorPinnedToSnapshot(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["zai"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", APIKey: "k", DefaultModel: "glm-5"},
	}}
	m.SetConfig(cfg)
	m.usagePanel = &usagePanelState{
		infos:    map[string]*usage.UsageInfo{},
		errs:     map[string]string{},
		fetching: true,
		expected: 1,
	}

	m.startupVendor = "zai"
	cfg.Vendors["deepseek"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {BaseURL: "https://api.deepseek.com", APIKey: "k2"},
	}}
	m.SetConfig(cfg)

	out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{vendor: "zai", info: &usage.UsageInfo{Vendor: "zai"}})
	if out.usagePanel.fetching {
		t.Fatal("fetch stuck true after all snapshot vendors answered (denominator recounted live)")
	}
}

func TestUsageFetchSnapshotExcludesKeyless(t *testing.T) {
	svc := usage.NewService()
	probe := &countingProbe{vendor: "zai"}
	svc.Register(probe)

	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["zai"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", APIKey: "k", DefaultModel: "glm-5"},
	}}

	cfg.Vendors["orphan"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {APIKey: "k"},
	}}
	m.SetConfig(cfg)
	m.startupVendor = "zai"
	m.activeEndpoint = "e"
	m.usageService = svc
	m.usagePanel = &usagePanelState{
		infos: map[string]*usage.UsageInfo{},
		errs:  map[string]string{},
	}

	cmd := m.fetchAllUsageCmd()
	if cmd == nil {
		t.Fatal("fetchAllUsageCmd returned nil cmd")
	}
	if m.usagePanel.expected != 1 {
		t.Fatalf("expected = %d, want 1 (zai only; keyless/probeless excluded)", m.usagePanel.expected)
	}
	_ = cmd()
	if n := probe.calls.Load(); n != 1 {
		t.Fatalf("probe calls = %d, want 1", n)
	}
}
