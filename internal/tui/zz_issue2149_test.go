package tui

// #2149 regression: with a mention completion active, ctrl+a moved the
// cursor to the start WITHOUT recalculating the completion state; the
// next Enter applied the stale completion, the mention branch scanned
// left for '@' from cursor 0, hit atPos == -1, and value[:atPos] panicked
// the whole TUI (slice bounds out of range [:-1]). Two guards: the
// readline shortcuts now refresh the completion, and applyAutoComplete
// no-ops when no '@' precedes the cursor.

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

// Unit shape of the panic: cursor with no '@' to its left must be a safe
// no-op, never value[:-1].
func TestApplyAutoCompleteNoAtLeftOfCursorIsNoop(t *testing.T) {
	m := newMention2149Model()
	m.input.SetValue("plain text no mention")
	m.input.CursorStart() // no '@' anywhere to the left

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("applyAutoComplete panicked on cursor without '@' left: %v", r)
		}
	}()
	_ = m.applyAutoComplete()
}

// The carrier sequence from the issue: '@int' activates the completion,
// ctrl+a moves the cursor to the start, Enter must not panic and must not
// splice a replacement at position -1.
func TestCtrlAThenEnterWithMentionCompletionNoPanic(t *testing.T) {
	m := newMention2149Model()
	m.input.SetValue("@int")
	m.input.CursorEnd()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ctrl+a then Enter panicked: %v", r)
		}
	}()

	// ctrl+a through the real dispatch: the refresh must deactivate the
	// completion (cursor no longer on the mention token) instead of
	// leaving stale state.
	updated, _ := m.handleKeyPress(tea.KeyPressMsg{Text: "ctrl+a"}, nil)
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", updated)
	}
	if m2.autoCompleteActive {
		t.Fatal("ctrl+a must refresh the completion state (was: stale-active, the panic carrier)")
	}

	// Enter through the same dispatch - must not panic and must not apply
	// the completion off-token.
	updated3, _ := m2.handleKeyPress(tea.KeyPressMsg{Text: "enter"}, nil)
	m3, _ := updated3.(Model)
	if strings.Contains(m3.input.Value(), "@internal") {
		t.Fatalf("completion applied off-token: %q", m3.input.Value())
	}
}
