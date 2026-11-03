package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestHistorySearch_SpaceInQuery(t *testing.T) {
	m := newTestModel()
	m.history = []string{"git status --short", "single", "another git status long"}
	updated, _ := m.Update(tea.KeyPressMsg{Text: "alt+r"})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Text: "git"})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Text: "status"})
	m = updated.(Model)

	if got := m.historySearch.query; got != " git status" {
		t.Fatalf("query = %q, want %q (literal spaces, never 'space') (#4731)", got, " git status")
	}
	if got := m.input.Value(); got != "another git status long" {
		t.Errorf("input = %q, want most recent multi-word match", got)
	}
}
