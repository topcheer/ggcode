package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

func issue4589Model(t *testing.T, version string) (*Model, *impersonatePanelState) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	if err := os.WriteFile(path, []byte("vendor: va\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	vi := textinput.New()
	vi.SetValue(version)
	vi.Focus()
	panel := &impersonatePanelState{
		section:       impSectionVersion,
		versionInput:  vi,
		currentPreset: "none",
		editingHeader: -1,
	}
	m := &Model{config: &config.Config{FilePath: path}, impersonatePanel: panel}
	provider.SetActiveImpersonation(nil, "", nil)
	return m, panel
}

func TestIssue4589_VersionEnterCommits(t *testing.T) {
	m, panel := issue4589Model(t, "v131.0")

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
	m, panel := issue4589Model(t, "v131.0")

	m.handleImpersonatePanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})

	if panel.versionInput.Focused() {
		t.Fatal("esc must blur the version input")
	}
	_, v, _ := provider.GetActiveImpersonation()
	if v != "" {
		t.Fatalf("#4589 regression: esc now commits - active version = %q; esc must stay a pure blur", v)
	}
}
