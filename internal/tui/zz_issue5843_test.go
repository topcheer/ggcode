package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/permission"
)

// Battery for issue #5843 (TUI side): the approval gate must keep the input
// drain open. While an approval prompt is pending, typing reaches the
// composer, enter queues the text on the interrupt channel (approval stays
// pending), and the y/n/a letter shortcuts only act when the composer is
// empty. Uses only pre-existing symbols so it compiles at the pre-fix
// parent and pins the behavioral red.

func new5843ApprovalModel() (Model, chan permission.Decision) {
	m := newTestModel()
	ch := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{
		ToolName: "run_command",
		Input:    "{" + string('"') + "command" + string('"') + ":" + string('"') + "npm test" + string('"') + "}",
		Response: ch,
	}
	m.approvalOptions = defaultApprovalOptions()
	m.approvalCursor = 0
	return m, ch
}

func TestIssue5843_TypingDuringApprovalReachesComposer(t *testing.T) {
	m, ch := new5843ApprovalModel()
	updated, _ := m.Update(tea.KeyPressMsg{Text: "h"})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Text: "i"})
	m = updated.(Model)
	if got := m.input.Value(); got != "hi" {
		t.Fatalf("typed keys must reach the composer while the approval gate is open, got %q", got)
	}
	if m.pendingApproval == nil {
		t.Fatal("typing must not resolve the pending approval")
	}
	select {
	case d := <-ch:
		t.Fatalf("typing must not answer the approval, got decision %v", d)
	default:
	}
}

func TestIssue5843_EnterWithTextQueuesSubmissionKeepsApproval(t *testing.T) {
	m, ch := new5843ApprovalModel()
	m.input.SetValue("also lint the output")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.pendingApproval == nil {
		t.Fatal("enter with composer text must queue input on the interrupt channel, not answer the approval")
	}
	if got := m.pendingSubmissionCount(); got != 1 {
		t.Fatalf("expected 1 queued submission after enter, got %d", got)
	}
	if m.input.Value() != "" {
		t.Fatalf("composer must clear after queueing, got %q", m.input.Value())
	}
	select {
	case d := <-ch:
		t.Fatalf("approval must stay pending, got decision %v", d)
	default:
	}
}

func TestIssue5843_ShortcutsStayOnEmptyComposer(t *testing.T) {
	m, ch := new5843ApprovalModel()
	updated, _ := m.Update(tea.KeyPressMsg{Text: "y"})
	m = updated.(Model)
	if m.pendingApproval != nil {
		t.Fatal("y on an empty composer must still approve")
	}
	if d := <-ch; d != permission.Allow {
		t.Fatalf("expected Allow from empty-composer y, got %v", d)
	}
}

func TestIssue5843_LetterShortcutTypesWhenComposerBusy(t *testing.T) {
	m, _ := new5843ApprovalModel()
	m.input.SetValue("yes")
	updated, _ := m.Update(tea.KeyPressMsg{Text: "y"})
	m = updated.(Model)
	if m.pendingApproval == nil {
		t.Fatal("y while composing must not resolve the approval")
	}
	if got := m.input.Value(); got != "yesy" {
		t.Fatalf("y while composing must append to the composer, got %q", got)
	}
}

func TestIssue5843_QueuedDuringApprovalIsDrainedByInterruptHandler(t *testing.T) {
	m, _ := new5843ApprovalModel()
	m.input.SetValue("meanwhile check the logs")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if got := m.pendingSubmissionCount(); got != 1 {
		t.Fatalf("setup: expected 1 queued submission, got %d", got)
	}
	drained := m.consumePendingSubmission()
	if drained != "meanwhile check the logs" {
		t.Fatalf("queued text must be retrievable via the interrupt drain, got %q", drained)
	}
}
