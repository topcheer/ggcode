package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/tool"
)

// #2609: a typed freeform note must be CLEARABLE locally. The old
// `if inputVal != ""` guard in saveActiveQuestionInput could not tell
// "empty because the remote value is not loaded" from "empty because the
// user just deleted their note" - so deleting then submitting still sent
// the stale text, and a tab round-trip resurrected it.
func newIssue2609State(t *testing.T) *questionnaireState {
	t.Helper()
	req := tool.AskUserRequest{
		Title: "t",
		Questions: []tool.AskUserQuestion{{
			ID: "q1", Title: "q", Kind: "single", Prompt: "p",
			Choices:       []tool.AskUserChoice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
			AllowFreeform: true,
		}},
	}
	return newQuestionnaireState(req, nil, "en")
}

func TestIssue2609_TypedNoteClearable(t *testing.T) {
	qs := newIssue2609State(t)

	// Type a note, save (as moveTab does), load it back, then clear.
	qs.input.SetValue("note A")
	qs.saveActiveQuestionInput()
	qs.loadActiveQuestion("en")
	if got := qs.answers[0].freeform; got != "note A" {
		t.Fatalf("precondition: saved note = %q", got)
	}
	qs.input.SetValue("")
	qs.saveActiveQuestionInput()
	if got := qs.answers[0].freeform; got != "" {
		t.Fatalf("cleared note must stay empty, got %q (agent would receive deleted text)", got)
	}

	// Tab round-trip after clearing must NOT resurrect the note.
	qs.loadActiveQuestion("en")
	if got := qs.input.Value(); strings.TrimSpace(got) != "" {
		t.Fatalf("load after clear resurrected %q", got)
	}

	// Same-tab char-by-char deletion: save at each step, last one empty.
	qs.input.SetValue("abc")
	qs.saveActiveQuestionInput()
	for _, rm := range []string{"c", "b", "a"} { // delete c, then b, then a
		cur := strings.TrimSuffix(qs.input.Value(), rm)
		qs.input.SetValue(cur)
		qs.saveActiveQuestionInput()
	}
	if got := qs.answers[0].freeform; got != "" {
		t.Fatalf("char-by-char deletion left residue %q", got)
	}
}

// The remote protection (2fdb3f87) must keep working: a remote write that
// has not been loaded into the input yet is never clobbered by a stale
// (empty) local input; once loaded, local edits win.
func TestIssue2609_RemoteWriteStillProtected(t *testing.T) {
	qs := newIssue2609State(t)

	// Simulate a remote IM answer arriving for the questionnaire.
	raw := make([]im.ParsedQuestionAnswer, 1)
	raw[0] = im.ParsedQuestionAnswer{QuestionIndex: 0, Selected: map[string]struct{}{"a": {}}, Freeform: "remote note"}
	qs.applyParsedAnswers(raw)

	// Local input is still empty/stale; save must NOT overwrite.
	qs.input.SetValue("")
	qs.saveActiveQuestionInput()
	if got := qs.answers[0].freeform; got != "remote note" {
		t.Fatalf("untouched remote answer must survive local empty input, got %q", got)
	}

	// After the tab loads the remote value, local clearing wins.
	qs.loadActiveQuestion("en")
	if got := qs.input.Value(); got != "remote note" {
		t.Fatalf("load must show the remote note, got %q", got)
	}
	qs.input.SetValue("")
	qs.saveActiveQuestionInput()
	if got := qs.answers[0].freeform; got != "" {
		t.Fatalf("post-load local clear must win over the remote value, got %q", got)
	}
}
