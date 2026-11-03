package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/tool"
)

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

	qs.loadActiveQuestion("en")
	if got := qs.input.Value(); strings.TrimSpace(got) != "" {
		t.Fatalf("load after clear resurrected %q", got)
	}

	qs.input.SetValue("abc")
	qs.saveActiveQuestionInput()
	for _, rm := range []string{"c", "b", "a"} {
		cur := strings.TrimSuffix(qs.input.Value(), rm)
		qs.input.SetValue(cur)
		qs.saveActiveQuestionInput()
	}
	if got := qs.answers[0].freeform; got != "" {
		t.Fatalf("char-by-char deletion left residue %q", got)
	}
}

func TestIssue2609_RemoteWriteStillProtected(t *testing.T) {
	qs := newIssue2609State(t)

	raw := make([]im.ParsedQuestionAnswer, 1)
	raw[0] = im.ParsedQuestionAnswer{QuestionIndex: 0, Selected: map[string]struct{}{"a": {}}, Freeform: "remote note"}
	qs.applyParsedAnswers(raw)

	qs.input.SetValue("")
	qs.saveActiveQuestionInput()
	if got := qs.answers[0].freeform; got != "remote note" {
		t.Fatalf("untouched remote answer must survive local empty input, got %q", got)
	}

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
