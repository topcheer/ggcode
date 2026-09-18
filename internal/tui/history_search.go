package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// historySearchState implements Claude Code v2.0-style reverse input history
// search: the user presses a key to enter search mode, types any substring,
// and the composer populates with the most recent history entry containing
// it. Enter accepts the populated query, Esc cancels and restores the
// pre-search input, and pressing the search key again cycles to older
// matches (bash reverse-i-search semantics). Matching is plain
// case-insensitive substring matching, like Ctrl+F text search.
//
// Keybinding note: Claude Code binds this to Ctrl+R, but in ggcode Ctrl+R is
// the long-standing sidebar toggle (persisted per session, covered by
// tests and i18n hints), so the search lands on Alt+R instead.
//
// All helpers below are package-level functions that read the state out of
// the Model, mutate it, and write it back - a deliberate choice to avoid
// value-receiver copy traps: state must round-trip through m.historySearch
// on every keystroke.
type historySearchState struct {
	active        bool
	query         string
	matchIdx      int    // index into m.history of the current match; -1 = none
	originalInput string // composer content when search started; restored on Esc
}

// beginHistorySearch enters reverse history search mode (Alt+R). The current
// composer content is stashed and shown again if the user cancels with Esc.
// Like bash/Claude Code reverse search, the most recent entry is offered
// immediately on an empty query.
func (m *Model) beginHistorySearch() {
	m.historySearch = historySearchState{
		active:        true,
		matchIdx:      -1,
		originalInput: m.input.Value(),
	}
	m.input.Reset()
	refreshHistorySearch(m)
}

// refreshHistorySearch recomputes the most recent history entry matching the
// current query and updates the composer content plus the hint line, then
// writes the state back to m.historySearch.
func refreshHistorySearch(m *Model) {
	s := m.historySearch
	q := strings.ToLower(s.query)
	s.matchIdx = -1
	if q == "" && len(m.history) > 0 {
		// Empty query matches the most recent entry, like the first press of
		// Ctrl+R in bash / reverse search in Claude Code.
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
		// No match yet: keep the raw query visible so the user can keep
		// refining; Enter then submits it like a normal prompt.
		m.input.SetValue(s.query)
		m.input.CursorEnd()
	}
	m.inputHint = m.t("history.search.hint", s.query)
	m.historySearch = s
}

// exitHistorySearch leaves search mode, optionally restoring the input that
// was present before the search started.
func exitHistorySearch(m *Model, restore bool) {
	s := m.historySearch
	if restore {
		m.input.SetValue(s.originalInput)
		composerCursorEnd(&m.input)
	}
	m.historySearch = historySearchState{}
	m.inputHint = ""
}

// handleHistorySearchKey processes a keypress while reverse history search
// is active. handled=true means the key was consumed by the search mode.
// handled=false means search mode exits first (keeping the current match in
// the composer) and the key should then flow through the normal composer
// path - Enter in particular falls through to the regular submit logic,
// matching Claude Code's "press Enter to use the populated query".
func (m Model) handleHistorySearchKey(msg tea.KeyPressMsg) (handled bool, out Model, cmd tea.Cmd) {
	switch msg.String() {
	case "enter":
		// Accept the populated query; fall through to normal submission.
		exitHistorySearch(&m, false)
		return false, m, nil
	case "esc":
		exitHistorySearch(&m, true)
		return true, m, nil
	case "alt+r":
		// Cycle to the next older match, like pressing Ctrl+R again in bash.
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
		// Backspace on an empty query cancels the search.
		exitHistorySearch(&m, true)
		return true, m, nil
	}
	// Printable text (ASCII or CJK; single rune from a terminal, possibly
	// several from a paste-like key event): extend the query and refine the
	// match. Modifier combos always render as "mod+key" and never qualify.
	key := msg.String()
	if !strings.Contains(key, "+") && utf8.RuneCountInString(key) > 0 {
		printable := true
		for _, r := range key {
			if r < 32 {
				printable = false
				break
			}
		}
		if printable {
			m.historySearch.query += key
			refreshHistorySearch(&m)
			return true, m, nil
		}
	}
	// Any other key (arrows, readline shortcuts, ...) exits search mode and
	// is then handled by the composer as usual; the matched text stays in
	// the input so editing continues seamlessly.
	exitHistorySearch(&m, false)
	return false, m, nil
}

// pushHistory records a submitted input in the input history with
// promote-to-front dedup (#2527): when the text exactly matches an existing
// entry, that entry is moved to the most-recent slot instead of appending a
// duplicate. This keeps Alt+R reverse search and arrow-up paging free of
// repeat entries when the same input is resubmitted (e.g. accepting a
// reverse-search match with Enter, or resending a queued command).
func (m *Model) pushHistory(text string) {
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i] == text {
			m.history = append(m.history[:i], m.history[i+1:]...)
			break
		}
	}
	m.history = append(m.history, text)
	m.historyIdx = len(m.history)
}
