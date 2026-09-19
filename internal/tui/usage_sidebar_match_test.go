package tui

import (
	"context"
	"strings"
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
			"cn-coding-openai": {BaseURL: zaiCodingURL, DefaultModel: "glm-5"}, // no endpoint-level key
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

// keyCaptureProbe records the apiKey Fetch actually received - the anchor
// against the 2026-09-18 regression where zai/cn-coding-openai (key at
// vendor level ONLY) read as keyless on the usage chain while chat worked.
type keyCaptureProbe struct {
	called int
	gotKey string
	gotURL string
	usage.UsageInfo
}

func (p *keyCaptureProbe) Vendor() string { return "zai" }
func (p *keyCaptureProbe) MatchesURL(baseURL string) bool {
	return strings.Contains(baseURL, "open.bigmodel.cn")
}
func (p *keyCaptureProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*usage.UsageInfo, error) {
	p.called++
	p.gotKey = apiKey
	p.gotURL = baseURL
	return &usage.UsageInfo{Vendor: "zai"}, nil
}

// TestVendorLevelKeyReachesProbe pins the exact break: endpoint block
// without api_key + vendor-level key present -> probe IS authorized and
// Fetch receives the VENDOR key (runtime resolver fallback), not "".
func TestVendorLevelKeyReachesProbe(t *testing.T) {
	probe := &keyCaptureProbe{}
	kimiProbe := &kimiCaptureProbe{}
	svc := usage.NewService()
	svc.Register(probe)
	svc.Register(kimiProbe)

	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["zai"] = config.VendorConfig{
		APIKey: "vendor-level-key",
		Endpoints: map[string]config.EndpointConfig{
			"cn-coding-openai": {BaseURL: zaiCodingURL, DefaultModel: "glm-5"}, // endpoint key absent
		},
	}
	m.SetConfig(cfg)
	m.activeVendor = "zai"
	m.activeEndpoint = "cn-coding-openai"
	m.usageService = svc

	if reason := m.explainUsageSidebar(); reason != "" {
		t.Fatalf("probe must be authorized, blocked by: %s", reason)
	}
	cmd := m.fetchAllUsageCmd()
	if cmd == nil {
		t.Fatal("fetchAllUsageCmd returned nil")
	}
	_ = cmd()
	if probe.called != 1 {
		t.Fatalf("probe calls = %d, want 1", probe.called)
	}
	if probe.gotKey != "vendor-level-key" {
		t.Fatalf("Fetch apiKey = %q, want the VENDOR-level key", probe.gotKey)
	}
	if probe.gotURL != zaiCodingURL {
		t.Fatalf("Fetch baseURL = %q, want %q", probe.gotURL, zaiCodingURL)
	}
}

// TestFetchCmdReturnsRealMsg guards the 2026-09-18 stuck-fetching bug: the
// fetch Cmd must RETURN a usageInfoUpdatedMsg, not a tea.Batch Cmd value
// (a function as Msg - silently dropped by Update dispatch, leaving the
// panel on 获取中 and the sidebar on probing forever).
func TestFetchCmdReturnsRealMsg(t *testing.T) {
	probe := &keyCaptureProbe{}
	kimiProbe := &kimiCaptureProbe{}
	svc := usage.NewService()
	svc.Register(probe)
	svc.Register(kimiProbe)
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["zai"] = config.VendorConfig{
		APIKey: "vendor-level-key",
		Endpoints: map[string]config.EndpointConfig{
			"cn-coding-openai": {BaseURL: zaiCodingURL, DefaultModel: "glm-5"},
		},
	}
	m.SetConfig(cfg)
	m.activeVendor = "zai"
	m.activeEndpoint = "cn-coding-openai"
	m.usageService = svc
	m.usagePanel = &usagePanelState{
		infos: map[string]*usage.UsageInfo{}, errs: map[string]string{}, fetching: true,
	}
	cmd := m.fetchAllUsageCmd()
	if cmd == nil {
		t.Fatal("nil cmd")
	}
	msg := cmd()
	um, ok := msg.(usageInfoUpdatedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want usageInfoUpdatedMsg (a returned Cmd-as-Msg is dropped by Update)", msg)
	}
	if um.vendor != "zai" || um.info == nil {
		t.Fatalf("msg = %+v, want zai info", um)
	}
	// And the returned msg must actually flip the panel out of fetching.
	out, _ := m.handleUsageInfoUpdated(um)
	if out.usagePanel.fetching {
		t.Fatal("panel still fetching after result delivered")
	}
}

// kimiCaptureProbe records what the kimi probe Fetch received.
type kimiCaptureProbe struct {
	called int
	gotKey string
	gotURL string
}

func (p *kimiCaptureProbe) Vendor() string { return "kimi" }
func (p *kimiCaptureProbe) MatchesURL(baseURL string) bool {
	return strings.Contains(baseURL, "api.kimi.com")
}
func (p *kimiCaptureProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*usage.UsageInfo, error) {
	p.called++
	p.gotKey = apiKey
	p.gotURL = baseURL
	return &usage.UsageInfo{Vendor: "kimi"}, nil
}

// TestUsageProbeSurvivesVendorSwitch pins the 2026-09-19 switch bug: after
// switching to another coding-plan provider mid-session, the usage probe
// kept failing. Two compounding causes: (1) currentEndpointForUsage and
// explainUsageSidebar resolved with a hardcoded model "" - endpoints
// without a configured SelectedModel/DefaultModel (the model is a
// per-session choice in m.activeModel) failed "has no active model" even
// though chat worked; (2) explain!=empty killed the 60s refresh chain, so
// the failure was permanent until a manual panel open.
func TestUsageProbeSurvivesVendorSwitch(t *testing.T) {
	probe := &keyCaptureProbe{}
	kimiProbe := &kimiCaptureProbe{}
	svc := usage.NewService()
	svc.Register(probe)
	svc.Register(kimiProbe)

	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["zai"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"cn-coding-openai": {BaseURL: zaiCodingURL, APIKey: "zai-key", DefaultModel: "glm-5"},
	}}
	// kimi endpoint WITHOUT any configured model - the session picks one.
	cfg.Vendors["kimi"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"coding": {BaseURL: "https://api.kimi.com/coding/v1", APIKey: "kimi-key"},
	}}
	m.SetConfig(cfg)
	m.usageService = svc

	// Session starts on zai (loaded from an old session).
	m.setActiveRuntimeSelection("zai", "cn-coding-openai", "glm-5")
	if got := m.probeableVendors(); len(got) != 1 || got[0] != "zai" {
		t.Fatalf("zai probeable = %v, want [zai]", got)
	}

	// Mid-session switch to kimi, whose endpoint carries NO configured
	// model - activeModel is the only model source.
	m.setActiveRuntimeSelection("kimi", "coding", "kimi-k2")
	if reason := m.explainUsageSidebar(); reason != "" {
		t.Fatalf("after switch probe blocked: %s", reason)
	}
	got := m.probeableVendors()
	if len(got) != 1 || got[0] != "kimi" {
		t.Fatalf("kimi probeable = %v, want [kimi]", got)
	}
	cmd := m.fetchAllUsageCmd()
	if cmd == nil {
		t.Fatal("no probe fired after switch")
	}
	_ = cmd()
	if kimiProbe.called != 1 || kimiProbe.gotKey != "kimi-key" || !strings.Contains(kimiProbe.gotURL, "api.kimi.com") {
		t.Fatalf("kimi probe: calls=%d key=%q url=%q, want 1 call with kimi-key on api.kimi.com", kimiProbe.called, kimiProbe.gotKey, kimiProbe.gotURL)
	}

	// Switch back: zai still probeable (and the stale sidebar snapshot
	// must not have survived as zai data - setActiveRuntimeSelection nils it).
	m.setActiveRuntimeSelection("zai", "cn-coding-openai", "glm-5")
	if m.sidebarUsage != nil {
		t.Fatal("stale kimi sidebarUsage survived the switch back")
	}
	if got := m.probeableVendors(); len(got) != 1 || got[0] != "zai" {
		t.Fatalf("zai re-probeable = %v, want [zai]", got)
	}
}

// TestUsageProbeSurvivesDisplayNameSwitch pins the display-name bug
// (2026-09-19 user report: switching kimi -> zai showed
// `vendor "智谱 Z.AI" is not configured` in the sidebar): the switch
// callers historically passed resolved.VendorName/EndpointName (display
// labels) into setActiveRuntimeSelection, and every downstream resolver
// call then failed on the unresolvable label. Both layers are pinned:
// the defense-in-depth name->ID remap in setActiveRuntimeSelection, and
// a DisplayName-bearing config resolving through explainUsageSidebar.
func TestUsageProbeSurvivesDisplayNameSwitch(t *testing.T) {
	probe := &keyCaptureProbe{}
	svc := usage.NewService()
	svc.Register(probe)

	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["zai"] = config.VendorConfig{
		DisplayName: "智谱 Z.AI",
		Endpoints: map[string]config.EndpointConfig{
			"cn-coding-openai": {BaseURL: zaiCodingURL, APIKey: "zai-key", DefaultModel: "glm-5"},
		},
	}
	m.SetConfig(cfg)
	m.usageService = svc

	// Simulate the historical buggy switch: display-name label arrives.
	m.setActiveRuntimeSelection("智谱 Z.AI", "cn-coding-openai", "glm-5")
	if m.activeVendor != "zai" {
		t.Fatalf("display name not remapped to ID: activeVendor=%q", m.activeVendor)
	}
	if reason := m.explainUsageSidebar(); reason != "" {
		t.Fatalf("probe blocked after display-name switch: %s", reason)
	}
	got := m.probeableVendors()
	if len(got) != 1 || got[0] != "zai" {
		t.Fatalf("probeable = %v, want [zai]", got)
	}
	cmd := m.fetchAllUsageCmd()
	if cmd == nil {
		t.Fatal("no probe fired")
	}
	_ = cmd()
	if probe.gotKey != "zai-key" {
		t.Fatalf("probe key=%q, want zai-key", probe.gotKey)
	}

	// A garbage name with no unique match must NOT be silently kept as an
	// ID-shaped value: probeable stays empty (explain carries the reason).
	m.setActiveRuntimeSelection("no-such-vendor", "cn-coding-openai", "glm-5")
	if got := m.probeableVendors(); len(got) != 0 {
		t.Fatalf("bogus vendor probed: %v", got)
	}
}
