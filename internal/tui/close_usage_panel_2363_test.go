package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestCloseActivePanelClosesUsagePanel verifies #2363: closeActivePanel
// must handle usagePanel directly - the #904-audited invariant that
// hasActivePanel and closeActivePanel model the same panel set broke when
// usagePanel was added to only one of them. ctrl+c must close the panel
// WITHOUT relying on the later key-handler fallback (dispatch-order luck).
func TestCloseActivePanelClosesUsagePanel(t *testing.T) {
	m := newTestModel()
	m.usagePanel = &usagePanelState{}
	if !m.hasActivePanel() {
		t.Fatal("usagePanel should register in hasActivePanel")
	}

	// Direct unit call on the fixed function (key routing differs across
	// bubbletea versions; the invariant under test is closeActivePanel
	// itself, which ctrl+c's exit-confirm guard consults first).
	if !m.closeActivePanel() {
		t.Fatal("closeActivePanel returned false with usagePanel open (no case hit)")
	}
	if m.usagePanel != nil {
		t.Fatal("closeActivePanel did not nil usagePanel")
	}

	// And the dispatch-order path still ends closed (handleUsagePanelKey
	// esc/ctrl+c fallback remains as defense in depth).
	m2 := newTestModel()
	m2.usagePanel = &usagePanelState{}
	out, _ := m2.handleUsagePanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if out.usagePanel != nil {
		t.Fatal("key-handler fallback also failed to close")
	}
}
