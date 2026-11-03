package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func newMention2149Model() Model {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteKind = "mention"
	m.autoCompleteItems = []string{"internal/"}
	return m
}

func TestApplyAutoCompleteNoAtLeftOfCursorIsNoop(t *testing.T) {
	m := newMention2149Model()
	m.input.SetValue("plain text no mention")
	m.input.CursorStart()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("applyAutoComplete panicked on cursor without '@' left: %v", r)
		}
	}()
	_ = m.applyAutoComplete()
}

func TestCtrlAThenEnterWithMentionCompletionNoPanic(t *testing.T) {
	m := newMention2149Model()
	m.input.SetValue("@int")
	m.input.CursorEnd()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ctrl+a then Enter panicked: %v", r)
		}
	}()

	updated, _ := m.handleKeyPress(tea.KeyPressMsg{Text: "ctrl+a"}, nil)
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", updated)
	}
	if m2.autoCompleteActive {
		t.Fatal("ctrl+a must refresh the completion state (was: stale-active, the panic carrier)")
	}

	updated3, _ := m2.handleKeyPress(tea.KeyPressMsg{Text: "enter"}, nil)
	m3, _ := updated3.(Model)
	if strings.Contains(m3.input.Value(), "@internal") {
		t.Fatalf("completion applied off-token: %q", m3.input.Value())
	}
}
