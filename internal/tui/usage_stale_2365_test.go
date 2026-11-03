package tui

import (
	"errors"
	"testing"

	"github.com/topcheer/ggcode/internal/usage"
)

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

	if m.usageService != oldSvc {
		t.Fatal("refresh dropped the Service instance (429 pacing lost)")
	}
	if oldSvc == nil {
		t.Fatal("ensureUsageService returned nil")
	}
}

func TestUsageUpdateFlipEvictsOpposite(t *testing.T) {
	m := newTestModel()
	m.activeVendor = "zai"
	m.usagePanel = &usagePanelState{infos: map[string]*usage.UsageInfo{}, errs: map[string]string{}, fetching: true}

	info := &usage.UsageInfo{Vendor: "zai"}
	if out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{vendor: "zai", info: info}); out.usagePanel.infos["zai"] != info {
		t.Fatal("ok result not recorded")
	}

	if out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{vendor: "zai", err: errors.New("down")}); out.usagePanel.infos["zai"] != nil {
		t.Fatal("flip to error kept the stale info entry")
	} else if out.usagePanel.errs["zai"] == "" {
		t.Fatal("flip to error did not record the error")
	}

	if out, _ := m.handleUsageInfoUpdated(usageInfoUpdatedMsg{vendor: "zai", info: info}); out.usagePanel.errs["zai"] != "" {
		t.Fatal("flip to ok kept the stale error entry")
	} else if out.usagePanel.infos["zai"] != info {
		t.Fatal("flip to ok did not record the info")
	}
}

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
