package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/tool"
)

// TestPasteDroppedWhileSingleChoiceQuestionnaireActive pins #1775 case 1:
// key presses are intercepted whenever a questionnaire is active, but
// PasteMsg fell through to the (fully covered) main input - pasted text
// haunted the NEXT message. A single-choice (non-freeform) questionnaire
// must drop pastes the same way it swallows keys.
func TestPasteDroppedWhileSingleChoiceQuestionnaireActive(t *testing.T) {
	m := newTestModel()
	respCh := make(chan tool.AskUserResponse, 1)
	req := tool.AskUserRequest{
		Title: "Pick",
		Questions: []tool.AskUserQuestion{{
			ID: "q1", Title: "Pick one", Kind: tool.AskUserKindSingle,
			Choices: []tool.AskUserChoice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
		}},
	}
	m.pendingQuestionnaire = newQuestionnaireState(req, respCh, m.currentLanguage())
	m.inputReady = true
	inputBefore := m.input.Value()

	next, _ := m.handlePaste(tea.PasteMsg{Content: "injected"}, nil)
	m2 := next.(Model)

	if got := m2.input.Value(); got != inputBefore {
		t.Fatalf("paste leaked into main input under non-freeform questionnaire: %q", got)
	}
}

// TestPasteDroppedWhileTmuxMenuOpen pins #1775 case 1's tmux sibling: the
// menu intercepts keys; pastes must be dropped too, not forwarded beneath.
func TestPasteDroppedWhileTmuxMenuOpen(t *testing.T) {
	m := newTestModel()
	m.tmuxMenuOpen = true
	m.inputReady = true
	inputBefore := m.input.Value()

	next, _ := m.handlePaste(tea.PasteMsg{Content: "x"}, nil)
	m2 := next.(Model)
	if got := m2.input.Value(); got != inputBefore {
		t.Fatalf("paste leaked into main input under tmux menu: %q", got)
	}
}
