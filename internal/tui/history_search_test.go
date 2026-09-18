package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/provider"
)

// Alt+R opens reverse input history search, matching Claude Code v2.0
// behavior (Ctrl+R is taken by the sidebar toggle in ggcode).
func TestHistorySearch_EnterAndMatch(t *testing.T) {
	m := newTestModel()
	m.history = []string{"run go tests", "fix the go bug", "RUN LINT"}

	updated, _ := m.Update(tea.KeyPressMsg{Text: "alt+r"})
	m = updated.(Model)
	if !m.historySearch.active {
		t.Fatal("expected history search active after alt+r")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "go"})
	m = updated.(Model)
	if got := m.input.Value(); got != "fix the go bug" {
		t.Errorf("input = %q, want most recent match %q", got, "fix the go bug")
	}
	if !strings.Contains(m.inputHint, "go") {
		t.Errorf("hint = %q, want it to echo the query", m.inputHint)
	}

	// alt+r again cycles to an older match containing the same query.
	updated, _ = m.Update(tea.KeyPressMsg{Text: "alt+r"})
	m = updated.(Model)
	if got := m.input.Value(); got != "run go tests" {
		t.Errorf("after cycle input = %q, want older match", got)
	}

	// Enter accepts the match and falls through to normal submission: the
	// search-mode flag clears and the composer is emptied because the query
	// went through the regular submit path (m.session is nil in the test
	// model, so submitText clears the input as part of the send attempt).
	updated, _ = m.Update(tea.KeyPressMsg{Text: "enter"})
	m = updated.(Model)
	if m.historySearch.active {
		t.Error("expected search mode inactive after enter")
	}
	if got := m.input.Value(); got != "" {
		t.Errorf("input = %q, want emptied by normal submit path", got)
	}
}

func TestHistorySearch_EscRestoresOriginalInput(t *testing.T) {
	m := newTestModel()
	m.history = []string{"old prompt"}
	m.input.SetValue("draft text")

	updated, _ := m.Update(tea.KeyPressMsg{Text: "alt+r"})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Text: "old"})
	m = updated.(Model)
	if m.input.Value() != "old prompt" {
		t.Fatalf("input = %q, want matched history entry", m.input.Value())
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)
	if m.historySearch.active {
		t.Error("expected search inactive after esc")
	}
	if m.input.Value() != "draft text" {
		t.Errorf("input = %q, want original draft restored", m.input.Value())
	}
	if m.inputHint != "" {
		t.Errorf("hint = %q, want cleared", m.inputHint)
	}
}

func TestHistorySearch_NoMatchAndBackspaceRefine(t *testing.T) {
	m := newTestModel()
	m.history = []string{"deploy staging"}

	updated, _ := m.Update(tea.KeyPressMsg{Text: "alt+r"})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Text: "z"})
	m = updated.(Model)
	if got := m.input.Value(); got != "z" {
		t.Errorf("input = %q, want raw query kept when no match", got)
	}
	// Backspace removes the last query char and the match reappears.
	updated, _ = m.Update(tea.KeyPressMsg{Text: "backspace"})
	m = updated.(Model)
	if m.input.Value() != "deploy staging" {
		t.Errorf("after backspace input = %q, want match for empty query", m.input.Value())
	}
	if !m.historySearch.active {
		t.Error("expected search to stay active while refining")
	}
	// Backspace again on empty query cancels and restores original input.
	updated, _ = m.Update(tea.KeyPressMsg{Text: "backspace"})
	m = updated.(Model)
	if m.historySearch.active {
		t.Error("expected search to cancel after backspace on empty query")
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want original (empty) input restored", m.input.Value())
	}
}

func TestHistorySearch_CaseInsensitiveAndAutoInjectedFiltered(t *testing.T) {
	m := newTestModel()
	// restoreHistoryFromMessages filters auto-injected user messages.
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Fix the PARSER bug"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "ok"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "[Context Anchor] injected advisory text"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "second prompt"}}},
	}
	m.restoreHistoryFromMessages(msgs)

	updated, _ := m.Update(tea.KeyPressMsg{Text: "alt+r"})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Text: "PARSER"})
	m = updated.(Model)
	if got := m.input.Value(); got != "Fix the PARSER bug" {
		t.Errorf("input = %q, want case-insensitive match", got)
	}
	for _, h := range m.history {
		if strings.HasPrefix(h, "[") {
			t.Errorf("auto-injected message leaked into history: %q", h)
		}
	}
}

func TestHistorySearch_AnyPanelOpenDoesNotStealKeys(t *testing.T) {
	m := newTestModel()
	m.tmuxMenuOpen = true
	m.history = []string{"run tests"}

	updated, _ := m.Update(tea.KeyPressMsg{Text: "alt+r"})
	m = updated.(Model)
	if m.historySearch.active {
		t.Error("alt+r must not open history search while tmux menu is open")
	}
}
