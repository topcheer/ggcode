package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type historySearchState struct {
	active        bool
	query         string
	matchIdx      int
	originalInput string
}

func (m *Model) beginHistorySearch() {
	m.historySearch = historySearchState{
		active:        true,
		matchIdx:      -1,
		originalInput: m.input.Value(),
	}
	m.input.Reset()
	refreshHistorySearch(m)
}

func refreshHistorySearch(m *Model) {
	s := m.historySearch
	q := strings.ToLower(s.query)
	s.matchIdx = -1
	if q == "" && len(m.history) > 0 {

		s.matchIdx = len(m.history) - 1
	}
	if q != "" {
		for i := len(m.history) - 1; i >= 0; i-- {
			if strings.Contains(strings.ToLower(m.history[i]), q) {
				s.matchIdx = i
				break
			}
		}
	}
	if s.matchIdx >= 0 {
		m.input.SetValue(m.history[s.matchIdx])
		composerCursorEnd(&m.input)
	} else {

		m.input.SetValue(s.query)
		m.input.CursorEnd()
	}
	m.inputHint = m.t("history.search.hint", s.query)
	m.historySearch = s
}

func exitHistorySearch(m *Model, restore bool) {
	s := m.historySearch
	if restore {
		m.input.SetValue(s.originalInput)
		composerCursorEnd(&m.input)
	}
	m.historySearch = historySearchState{}
	m.inputHint = ""
}

func (m Model) handleHistorySearchKey(msg tea.KeyPressMsg) (handled bool, out Model, cmd tea.Cmd) {
	switch msg.String() {
	case "enter":

		exitHistorySearch(&m, false)
		return false, m, nil
	case "esc":
		exitHistorySearch(&m, true)
		return true, m, nil
	case "alt+r":

		s := m.historySearch
		q := strings.ToLower(s.query)
		if q != "" {
			for i := s.matchIdx - 1; i >= 0; i-- {
				if strings.Contains(strings.ToLower(m.history[i]), q) {
					s.matchIdx = i
					break
				}
			}
		}
		if s.matchIdx >= 0 {
			m.input.SetValue(m.history[s.matchIdx])
			composerCursorEnd(&m.input)
		}
		m.historySearch = s
		return true, m, nil
	case "backspace", "ctrl+h":
		if runes := []rune(m.historySearch.query); len(runes) > 0 {
			m.historySearch.query = string(runes[:len(runes)-1])
			refreshHistorySearch(&m)
			return true, m, nil
		}

		exitHistorySearch(&m, true)
		return true, m, nil
	}

	if msg.Text != "" || msg.Code == tea.KeySpace {

		if msg.Code == tea.KeySpace {
			m.historySearch.query += " "
		} else {
			m.historySearch.query += msg.Text
		}
		refreshHistorySearch(&m)
		return true, m, nil
	}

	exitHistorySearch(&m, false)
	return false, m, nil
}
