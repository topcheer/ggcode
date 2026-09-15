package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/usage"
)

// TestUsageFetchDenominatorPinnedToSnapshot verifies #2371-②: the fetch
// completion denominator is the snapshot size pinned at fetch start, not a
// live probeableVendors() recount. A vendor added to the config mid-fetch
// must NOT wedge the spinner (its msg never comes; the old recount made the
// count unreachable and fetching stuck true until a manual r/Esc).
func TestUsageFetchDenominatorPinnedToSnapshot(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["zai"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {APIKey: "k"},
	}}
	m.SetConfig(cfg)
	m.usagePanel = &usagePanelState{
		infos:    map[string]*usage.UsageInfo{},
		errs:     map[string]string{},
		fetching: true,
		expected: 1, // pinned by fetchAllUsageCmd's snapshot (zai only)
	}

	// Mid-flight: a second keyed vendor appears in the config.
	m.startupVendor = "zai"
	cfg.Vendors["deepseek"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {APIKey: "k2"},
	}}
	m.SetConfig(cfg)

	// The in-flight round only ever delivers zai's msg - and that must
	// complete the fetch. The old denominator (live recount = 2) left
	// fetching=true forever.
	out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{vendor: "zai", info: &usage.UsageInfo{Vendor: "zai"}})
	if out.usagePanel.fetching {
		t.Fatal("fetch stuck true after all snapshot vendors answered (denominator recounted live)")
	}
}

// TestUsageFetchSnapshotExcludesKeyless verifies #2371-①: the closure works
// off a {vendor, baseURL, apiKey} snapshot taken on the Update goroutine -
// vendors without a key drop out at snapshot time (expected matches what
// will produce messages), and only snapshot vendors are probed.
func TestUsageFetchSnapshotExcludesKeyless(t *testing.T) {
	svc := usage.NewService()
	probe := &countingProbe{vendor: "zai"}
	svc.Register(probe)

	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendors["zai"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {APIKey: "k"},
	}}
	// Keyed config entry but NO probe registered: excluded by probeable.
	cfg.Vendors["orphan"] = config.VendorConfig{Endpoints: map[string]config.EndpointConfig{
		"e": {APIKey: "k"},
	}}
	m.SetConfig(cfg)
	m.startupVendor = "zai" // #2150 rework: panel probes the ACTIVE vendor only
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
	_ = cmd() // executes the closure: must touch nothing on m, probe zai once
	if n := probe.calls.Load(); n != 1 {
		t.Fatalf("probe calls = %d, want 1", n)
	}
}
