package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/usage"
)

const zaiCodingURL = "https://open.bigmodel.cn/api/coding/paas/v4"

// TestSidebarUsageAttributionByExactURL pins the final attribution rule:
// the msg carries the URL the probe actually hit, and the sidebar accepts
// it ONLY when it equals THIS session's endpoint URL exactly. No probe-id
// translation, no vendor-name fallback. The endpoint key resolves through
// the SAME runtime resolver as chat (ResolveEndpointSelection), so a
// vendor-level key applies even when the endpoint block omits api_key
// (zai/cn-coding-openai shape - screenshot evidence 2026-09-18).
func TestSidebarUsageAttributionByExactURL(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["zai"] = config.VendorConfig{
		APIKey: "vendor-level-key",
		Endpoints: map[string]config.EndpointConfig{
			"cn-coding-openai": {BaseURL: zaiCodingURL}, // no endpoint-level key
		},
	}
	m.SetConfig(cfg)
	m.activeVendor = "zai"
	m.activeEndpoint = "cn-coding-openai"

	out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{
		vendor: "zai",
		info:   &usage.UsageInfo{Vendor: "zai"},
	})
	if out.sidebarUsage == nil {
		t.Fatal("probe result must render directly (no attribution gate)")
	}
}

// TestSidebarUsageErrorClearsStaleSidebar: an error result for the ACTIVE
// endpoint clears the sidebar and surfaces the reason.
func TestSidebarUsageErrorClearsStaleSidebar(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["v"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {BaseURL: zaiCodingURL, APIKey: "k", DefaultModel: "m"},
	}}
	m.SetConfig(cfg)
	m.activeVendor = "v"
	m.activeEndpoint = "e"

	out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{
		vendor: "zai",
		err:    &usage.RateLimitedError{},
	})
	if out.sidebarUsage != nil {
		t.Fatal("active-endpoint probe error must clear the sidebar snapshot")
	}
	if out.usageSidebarStatus == "" {
		t.Fatal("error must surface a status line")
	}
}

// TestSidebarUsageNoNameFallback: without a matching URL there is NO
// attribution - not via probe id, not via vendor name.
func TestSidebarUsageNoNameFallback(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.activeVendor = "kimi"
	m.startupVendor = "kimi"

	out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{
		vendor: "kimi",
		info:   &usage.UsageInfo{Vendor: "kimi"},
	})
	if out.sidebarUsage == nil {
		t.Fatal("any probe result renders directly - no attribution gate")
	}
}

// TestExplainUsageSidebarBranches: every no-probe branch produces a
// distinct human-readable reason, rendered verbatim in the sidebar.
func TestExplainUsageSidebarBranches(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.activeVendor = "ghost"
	m.activeEndpoint = "e"
	if got := m.explainUsageSidebar(); got != `vendor "ghost" is not configured` {
		t.Fatalf("branch vendor-missing: got %q", got)
	}

	// Unclaimed host: endpoint resolves but no probe supports it.
	cfg := config.DefaultConfig()
	cfg.Vendors["v"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {BaseURL: "https://coding.dashscope.aliyuncs.com/v1", APIKey: "k", DefaultModel: "m"},
	}}
	m2 := newTestModel()
	m2.SetConfig(cfg)
	m2.activeVendor = "v"
	m2.activeEndpoint = "e"
	if got := m2.explainUsageSidebar(); got != "no usage probe for https://coding.dashscope.aliyuncs.com" {
		t.Fatalf("branch unclaimed-host: got %q", got)
	}

	// Keyless everywhere (endpoint AND vendor): real "no api key".
	cfg2 := config.DefaultConfig()
	cfg2.Vendors["v"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {BaseURL: zaiCodingURL, DefaultModel: "m"},
	}}
	m3 := newTestModel()
	m3.SetConfig(cfg2)
	m3.activeVendor = "v"
	m3.activeEndpoint = "e"
	if got := m3.explainUsageSidebar(); got != "endpoint v/e has no api key" {
		t.Fatalf("branch keyless: got %q", got)
	}

	// Vendor-level key only (zai/cn-coding-openai shape): probeable.
	m4 := newTestModel()
	m4.SetConfig(cfg)
	m4.activeVendor = "v"
	m4.activeEndpoint = "e"
	cfg.Vendors["v"] = config.VendorConfig{
		APIKey: "vendor-level-key",
		Endpoints: map[string]config.EndpointConfig{
			"e": {BaseURL: zaiCodingURL, DefaultModel: "m"},
		},
	}
	m4.SetConfig(cfg)
	if got := m4.explainUsageSidebar(); got != "" {
		t.Fatalf("branch vendor-key-fallback: expected probeable, got %q", got)
	}
}
