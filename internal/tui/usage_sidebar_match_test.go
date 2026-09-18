package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/usage"
)

// TestSidebarUsageAttributionByURLDomain pins the 2026-09-18 sidebar fix:
// attribution compares in the RESOLVED probe-id domain (svc.Resolve of the
// current endpoint URL), never the config vendor-name domain. msg.vendor
// comes from probeableVendors() == Resolve(baseURL) ("openrouter"), while
// the user may have NAMED the vendor anything ("ai-gateway"). The old
// name-domain comparison never matched for renamed configs and the sidebar
// silently stayed empty.
func TestSidebarUsageAttributionByURLDomain(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["ai-gateway"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"openrouter": {BaseURL: "https://openrouter.ai/api/v1", APIKey: "k"},
	}}
	m.SetConfig(cfg)
	// Session-runtime selection (persisted in the session, NOT the config
	// file): activeVendor/activeEndpoint reflect what this session switched
	// to; the config file only feeds NEW sessions.
	m.activeVendor = "ai-gateway" // user-chosen name, NOT the probe id
	m.activeEndpoint = "openrouter"

	// Probe result keyed by the URL-resolved id (what probeableVendors emits).
	out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{
		vendor: "openrouter",
		info:   &usage.UsageInfo{Vendor: "openrouter"},
	})
	if out.sidebarUsage == nil {
		t.Fatal("sidebar not attributed: config vendor name 'ai-gateway' must match probe id 'openrouter' via the resolved URL domain")
	}

	// A result for a DIFFERENT probe id must not leak onto the sidebar.
	out2, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{
		vendor: "zai",
		info:   &usage.UsageInfo{Vendor: "zai"},
	})
	if out2.sidebarUsage != nil && out2.sidebarUsage.Vendor == "zai" {
		t.Fatal("sidebar attributed to a non-active probe id")
	}
}

// TestSidebarUsageErrorClearsStaleSidebar: an error result for the ACTIVE
// endpoint clears the sidebar instead of keeping a stale balance (#2365-①b
// sidebar side).
func TestSidebarUsageErrorClearsStaleSidebar(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["v"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {BaseURL: "https://open.bigmodel.cn/api/paas/v4", APIKey: "k"},
	}}
	m.SetConfig(cfg)
	m.activeVendor = "v"

	out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{
		vendor: "zai",
		err:    errUsageProbeForTest(),
	})
	if out.sidebarUsage != nil {
		t.Fatal("active-endpoint probe error must clear the sidebar snapshot")
	}
}

// TestSidebarUsageFallbackToNameDomain: with no resolvable endpoint (daemon
// /IM-attached sessions may lack one), attribution falls back to comparing
// the config vendor name directly.
func TestSidebarUsageFallbackToNameDomain(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig()) // no vendors -> no endpoint
	m.activeVendor = "kimi"
	m.startupVendor = "kimi"

	out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{
		vendor: "kimi",
		info:   &usage.UsageInfo{Vendor: "kimi"},
	})
	if out.sidebarUsage == nil {
		t.Fatal("name-domain fallback must still attribute when no endpoint URL resolves")
	}
}

func errUsageProbeForTest() error { return &usage.RateLimitedError{} }
