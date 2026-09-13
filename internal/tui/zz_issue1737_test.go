package tui

// #1737 case 2 regression: cursor moves (j/k) used to rewrite
// versionInput to the TARGET preset's default while currentPreset only
// updated at presets-area Enter - an apply from another section (e.g.
// add header + Enter) then persisted preset A with preset B's default
// version. The sync now runs only at the presets-Enter point where
// currentPreset updates, so preset+version always apply as a pair.

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

	// j moves the cursor to firefox: versionInput must NOT flip to
	// firefox's default (that used to clobber the chrome custom version).
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
