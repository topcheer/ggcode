package tui

// #4589: the impersonate panel's Version text input had NO commit path of
// its own - enter/esc only blurred, and closing the panel never applied, so
// a freshly typed version was silently dropped. Enter in the version input
// now runs the same applyImpersonateSettings the preset/header paths use
// (SetActiveImpersonation runs before the config save, so the commit is
// observable via provider.GetActiveImpersonation even when the config save
// fails in a test environment with no config file).

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

func issue4589Model(version string) (*Model, *impersonatePanelState) {
	vi := textinput.New()
	vi.SetValue(version)
	vi.Focus()
	panel := &impersonatePanelState{
		section:       impSectionVersion,
		versionInput:  vi,
		currentPreset: "none",
		editingHeader: -1, // 0 would route keys to the header-edit branch
	}
	m := &Model{config: &config.Config{}, impersonatePanel: panel}
	provider.SetActiveImpersonation(nil, "", nil) // isolate globals
	return m, panel
}

func TestIssue4589_VersionEnterCommits(t *testing.T) {
	m, panel := issue4589Model("v131.0")

	m.handleImpersonatePanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if panel.versionInput.Focused() {
		t.Fatal("#4589: enter must blur the version input")
	}
	_, v, _ := provider.GetActiveImpersonation()
	if v != "v131.0" {
		t.Fatalf("#4589: enter on the version input must commit it - active version = %q (typed value silently dropped)", v)
	}
}

func TestIssue4589_VersionEscDoesNotCommit(t *testing.T) {
	m, panel := issue4589Model("v131.0")

	m.handleImpersonatePanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})

	if panel.versionInput.Focused() {
		t.Fatal("esc must blur the version input")
	}
	_, v, _ := provider.GetActiveImpersonation()
	if v != "" {
		t.Fatalf("#4589 regression: esc now commits - active version = %q; esc must stay a pure blur", v)
	}
}
