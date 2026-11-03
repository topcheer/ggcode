package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCloseActivePanelClosesUsagePanel(t *testing.T) {
	m := newTestModel()
	m.usagePanel = &usagePanelState{}
	if !m.hasActivePanel() {
		t.Fatal("usagePanel should register in hasActivePanel")
	}

	if !m.closeActivePanel() {
		t.Fatal("closeActivePanel returned false with usagePanel open (no case hit)")
	}
	if m.usagePanel != nil {
		t.Fatal("closeActivePanel did not nil usagePanel")
	}

	m2 := newTestModel()
	m2.usagePanel = &usagePanelState{}
	out, _ := m2.handleUsagePanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if out.usagePanel != nil {
		t.Fatal("key-handler fallback also failed to close")
	}
}
