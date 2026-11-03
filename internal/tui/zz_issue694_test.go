package tui

import (
	"testing"
	"time"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func newQuestionnaire694(ch chan toolpkg.AskUserResponse) *questionnaireState {
	req := toolpkg.AskUserRequest{
		Title: "pick one",
		Questions: []toolpkg.AskUserQuestion{
			{ID: "q1", Title: "Which DB?", Kind: "single_choice"},
		},
	}
	return newQuestionnaireState(req, ch, LangEnglish)
}

func TestIssue694_ClearPendingApprovalsReleasesQuestionnaireChannel(t *testing.T) {
	m := newTestModel()
	ch := make(chan toolpkg.AskUserResponse, 1)
	m.pendingQuestionnaire = newQuestionnaire694(ch)
	m.tunnelPendingAskUserID = "stale-ask"

	m.clearPendingApprovals()

	if m.pendingQuestionnaire != nil {
		t.Error("pendingQuestionnaire not cleared")
	}
	if m.tunnelPendingAskUserID != "" {
		t.Errorf("tunnelPendingAskUserID = %q, want empty", m.tunnelPendingAskUserID)
	}
	select {
	case resp := <-ch:
		if resp.Status != toolpkg.AskUserStatusCancelled {
			t.Errorf("questionnaire released with status %q, want cancelled", resp.Status)
		}
	case <-waitTimeout694(t):
		t.Fatal("#694: blocked questionnaire waiter was not released — agent goroutine would hang")
	}
}

func TestIssue694_SwitchSessionReleasesQuestionnaireChannel(t *testing.T) {
	m := newTestModel()
	ch := make(chan toolpkg.AskUserResponse, 1)
	m.pendingQuestionnaire = newQuestionnaire694(ch)

	m.switchToSession(newSes688(), true)

	if m.pendingQuestionnaire != nil {
		t.Error("pendingQuestionnaire not cleared on session switch")
	}
	select {
	case resp := <-ch:
		if resp.Status != toolpkg.AskUserStatusCancelled {
			t.Errorf("questionnaire released with status %q, want cancelled", resp.Status)
		}
	case <-waitTimeout694(t):
		t.Fatal("#694: session switch left the questionnaire waiter unreleased")
	}
}

func TestIssue694_ClearPendingApprovalsNilQuestionnaireNoop(t *testing.T) {
	m := newTestModel()
	m.pendingQuestionnaire = nil
	m.clearPendingApprovals()
	if m.pendingQuestionnaire != nil {
		t.Error("pendingQuestionnaire should stay nil")
	}
}

func waitTimeout694(t *testing.T) <-chan time.Time {
	return time.After(2 * time.Second)
}
