package tui

import (
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestImpersonateCursorMoveDoesNotTouchVersionInput(t *testing.T) {
	m := newTestModel()
	p := &impersonatePanelState{
		section:       impSectionPresets,
		editingHeader: -1,
		cursor:        0,
		presets: []provider.ImpersonationPreset{
			{ID: "chrome", DefaultVersion: "v120"},
			{ID: "firefox", DefaultVersion: "v133"},
		},
	}
	vi := textinput.New()
	vi.SetValue("v120-custom")
	p.versionInput = vi
	m.impersonatePanel = p

	m2, _ := m.handleImpersonatePanelKey(tea.KeyPressMsg{Code: tea.KeyDown})
	p2 := m2.impersonatePanel
	if p2.cursor != 1 {
		t.Fatalf("cursor must move to 1, got %d", p2.cursor)
	}
	if p2.versionInput.Value() != "v120-custom" {
		t.Fatalf("versionInput must not change on cursor move, got %q", p2.versionInput.Value())
	}
	if p2.currentPreset != "" {
		t.Fatalf("currentPreset must not change on cursor move, got %q", p2.currentPreset)
	}
}
