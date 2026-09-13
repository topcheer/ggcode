package tui

import (
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// #2202: the #1737 case 2 fix regressed the apply path twice -
// (A) same-preset Enter clobbered the user's typed version and
// re-persisted the old value; (B) switching presets never loaded the
// target preset's default version (stale custom version leaked into the
// new preset).

func new2202Panel(cursor int, currentPreset, versionValue string) *impersonatePanelState {
	p := &impersonatePanelState{
		section:       impSectionPresets,
		editingHeader: -1,
		cursor:        cursor,
		currentPreset: currentPreset,
		presets: []provider.ImpersonationPreset{
			{ID: "chrome", DefaultVersion: "v120"},
			{ID: "firefox", DefaultVersion: "v133"},
		},
	}
	vi := textinput.New()
	vi.SetValue(versionValue)
	p.versionInput = vi
	return p
}

func TestIssue2202SamePresetEnterKeepsTypedVersion(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.impersonatePanel = new2202Panel(0, "chrome", "v130-new")

	m2, _ := m.handleImpersonatePanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	p := m2.impersonatePanel
	if p.versionInput.Value() != "v130-new" {
		t.Fatalf("symptom A: same-preset Enter must keep the typed version, got %q", p.versionInput.Value())
	}
	if p.currentPreset != "chrome" {
		t.Fatalf("currentPreset must stay chrome, got %q", p.currentPreset)
	}
}

func TestIssue2202PresetSwitchLoadsTargetDefault(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	// persisted chrome custom version still in the input; cursor on firefox
	m.impersonatePanel = new2202Panel(1, "chrome", "v120-custom")

	m2, _ := m.handleImpersonatePanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	p := m2.impersonatePanel
	if p.versionInput.Value() != "v133" {
		t.Fatalf("symptom B: switching to firefox must load its default v133, got %q", p.versionInput.Value())
	}
	if p.currentPreset != "firefox" {
		t.Fatalf("currentPreset must become firefox, got %q", p.currentPreset)
	}
}
