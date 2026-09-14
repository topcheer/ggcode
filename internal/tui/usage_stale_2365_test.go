package tui

import (
	"errors"
	"testing"

	"github.com/topcheer/ggcode/internal/usage"
)

// TestRefreshUsagePanelClearsStale verifies #2365-①a: the r-refresh must
// evict round-1 results. The old code kept infos/errs, so a vendor whose
// round-2 result flipped to an error kept rendering its round-1 balance
// forever (render prefers infos; the fresh error was permanently masked).
func TestRefreshUsagePanelClearsStale(t *testing.T) {
	m := newTestModel()
	m.usagePanel = &usagePanelState{
		infos: map[string]*usage.UsageInfo{
			"zai": {Vendor: "zai", Balance: floatPtr(99.99)},
		},
		errs: map[string]string{},
	}

	oldSvc := m.ensureUsageService()
	cmd := m.refreshUsagePanel()
	if cmd == nil {
		t.Fatal("refresh returned nil cmd")
	}
	if len(m.usagePanel.infos) != 0 || len(m.usagePanel.errs) != 0 {
		t.Fatalf("refresh kept stale maps: infos=%d errs=%d", len(m.usagePanel.infos), len(m.usagePanel.errs))
	}
	if !m.usagePanel.fetching {
		t.Fatal("refresh did not set fetching")
	}
	// #2366-① supersedes the original #2365 cache-bypass-by-replacement
	// design: the Service instance must SURVIVE the refresh (singleflight
	// + Retry-After-sized negative cache are instance state); only the
	// per-vendor SUCCESS rows are invalidated.
	if m.usageService != oldSvc {
		t.Fatal("refresh dropped the service instance (negative cache + singleflight gone)")
	}
}

// TestUsageUpdateFlipEvictsOpposite verifies #2365-①b: infos/errs are
// mutually exclusive per vendor - a flipped probe result must delete its
// opposite entry, or the completion count double-counts (fetching flips
// false early) and a stale info masks a fresh error.
func TestUsageUpdateFlipEvictsOpposite(t *testing.T) {
	m := newTestModel()
	m.activeVendor = "zai"
	m.usagePanel = &usagePanelState{infos: map[string]*usage.UsageInfo{}, errs: map[string]string{}, fetching: true}

	info := &usage.UsageInfo{Vendor: "zai"}
	if out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{vendor: "zai", info: info}); out.usagePanel.infos["zai"] != info {
		t.Fatal("ok result not recorded")
	}
	// Flip to error: infos entry must go.
	if out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{vendor: "zai", err: errors.New("down")}); out.usagePanel.infos["zai"] != nil {
		t.Fatal("flip to error kept the stale info entry")
	} else if out.usagePanel.errs["zai"] == "" {
		t.Fatal("flip to error did not record the error")
	}
	// Flip back to ok: errs entry must go.
	if out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{vendor: "zai", info: info}); out.usagePanel.errs["zai"] != "" {
		t.Fatal("flip to ok kept the stale error entry")
	} else if out.usagePanel.infos["zai"] != info {
		t.Fatal("flip to ok did not record the info")
	}
}

// TestVendorSwitchClearsSidebarUsage verifies #2365-②: switching the
// active vendor drops the sidebar snapshot - the section renders no
// vendor name, so the previous vendor's balance/quota must not present
// as the new vendor's numbers.
func TestVendorSwitchClearsSidebarUsage(t *testing.T) {
	m := newTestModel()
	m.activeVendor = "zai"
	m.sidebarUsage = &usage.UsageInfo{Vendor: "zai", Balance: floatPtr(50)}

	m.setActiveRuntimeSelection("deepseek", "e2", "m2")

	if m.sidebarUsage != nil {
		t.Fatalf("vendor switch kept stale sidebar snapshot: %+v", m.sidebarUsage)
	}
	if m.activeVendor != "deepseek" {
		t.Fatalf("vendor not switched: %s", m.activeVendor)
	}
}

func floatPtr(f float64) *float64 { return &f }
