//go:build goolm

package tui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// #4490: named keys (up/delete/tab/home/...) must NOT be appended to the
// history-search query - their key NAME is all-printable, so the old
// string heuristic treated them as typed text and the query got polluted
// ("...up", "...delete"); they must exit search mode instead.
func TestIssue4490_NamedKeysExitSearchInsteadOfPollutingQuery(t *testing.T) {
	m := Model{
		input:         mkTA(40),
		history:       []string{"base entry with words"},
		historySearch: historySearchState{active: true, query: "base", matchIdx: -1},
	}

	for _, code := range []rune{tea.KeyUp, tea.KeyDown, tea.KeyDelete, tea.KeyTab, tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown} {
		m2 := Model{
			input:         mkTA(40),
			history:       []string{"base entry with words"},
			historySearch: historySearchState{active: true, query: "base", matchIdx: -1},
		}
		msg := tea.KeyPressMsg{Code: code}
		handled, out, _ := m2.handleHistorySearchKey(msg)
		// exitHistorySearch legitimately clears the query on the way out;
		// pollution would leave "base" + the key NAME (e.g. "baseup").
		if strings.Contains(out.historySearch.query, msg.String()) || out.historySearch.query == "base"+msg.String() {
			t.Errorf("key %q polluted the query: %q", code, out.historySearch.query)
		}
		if out.historySearch.query == "base"+msg.String() {
			t.Errorf("key %q appended its NAME to the query", code)
		}
		if handled {
			t.Errorf("key %q should fall through (exit search mode), not be handled", code)
		}
	}

	// Real typed text still extends the query.
	handled, out, _ := m.handleHistorySearchKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if !handled || out.historySearch.query != "basea" {
		t.Fatalf("typed rune should extend query to \"basea\", handled=%v query=%q", handled, out.historySearch.query)
	}
}

// #4491 Bug 1: saveHooksConfig must persist BEFORE applying to the live
// agent - on Save failure the agent must NOT be running the new hooks
// (in-memory/disk split). Structural pin: the Save call precedes the
// SetHookConfig call in the source.
func TestIssue4491_SaveBeforeSetOrderPinned(t *testing.T) {
	b, err := os.ReadFile("hooks_panel.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	fnStart := strings.Index(s, "func (m *Model) saveHooksConfig(")
	if fnStart < 0 {
		t.Fatal("saveHooksConfig not found")
	}
	fnEnd := strings.Index(s[fnStart:], "\nfunc ")
	if fnEnd < 0 {
		fnEnd = len(s) - fnStart
	}
	fn := s[fnStart : fnStart+fnEnd]
	saveIdx := strings.Index(fn, ".Save()")
	setIdx := strings.Index(fn, ".SetHookConfig(")
	if saveIdx < 0 || setIdx < 0 || setIdx < saveIdx {
		t.Fatal("#4491 regression: SetHookConfig must run AFTER a successful Save")
	}
}
