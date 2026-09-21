package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/permission"
)

// TestApproval_FeedbackRowTypingAndSend covers the "reject with feedback"
// approval row: pressing e jumps to it, typed keys become corrective text
// (NOT shortcuts), Enter resolves the approval as Deny and hands the text to
// the feedback box for the agent's rejection hook.
func TestApproval_FeedbackRowTypingAndSend(t *testing.T) {
	m := newTestModel()
	ch := make(chan permission.Decision, 1)
	box := &approvalFeedbackBox{}
	m.pendingApproval = &ApprovalMsg{
		ToolName: "edit_file",
		Input:    `{"path":"main.go"}`,
		Response: ch,
	}
	m.approvalOptions = defaultApprovalOptions()
	m.approvalCursor = 0
	m.approvalFeedbackBox = box

	// Press "e" to jump to the feedback row (last option).
	updated, _ := m.Update(tea.KeyPressMsg{Text: "e"})
	m = updated.(Model)
	feedbackRow := len(m.approvalOptions) - 1
	if m.approvalOptions[feedbackRow].shortcut != "e" {
		t.Fatalf("expected last option to be the feedback row, got %q", m.approvalOptions[feedbackRow].shortcut)
	}
	if m.approvalCursor != feedbackRow {
		t.Fatalf("expected cursor on feedback row %d, got %d", feedbackRow, m.approvalCursor)
	}

	// Typed letters are content here — including "n", which would otherwise
	// be the instant-deny shortcut on other rows.
	updated, _ = m.Update(tea.KeyPressMsg{Text: "use config file instead"})
	m = updated.(Model)
	if got := string(m.approvalFeedbackBuf); got != "use config file instead" {
		t.Fatalf("expected buffered feedback, got %q", got)
	}
	if m.pendingApproval == nil {
		t.Fatal("approval must still be pending while typing")
	}

	// Enter resolves as Deny and stashes the text.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.pendingApproval != nil {
		t.Fatal("expected pendingApproval=nil after feedback rejection")
	}
	if got := <-ch; got != permission.Deny {
		t.Fatalf("expected Deny decision, got %v", got)
	}
	if got := box.take(); got != "use config file instead" {
		t.Fatalf("expected stashed feedback, got %q", got)
	}
	if len(m.approvalFeedbackBuf) != 0 {
		t.Fatalf("expected feedback buffer cleared, got %q", string(m.approvalFeedbackBuf))
	}
}

// TestApproval_FeedbackRowBackspaceAndNav pins row-local editing and that
// navigation keys keep their menu role on the feedback row.
func TestApproval_FeedbackRowBackspaceAndNav(t *testing.T) {
	m := newTestModel()
	ch := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{ToolName: "run_command", Input: `{"command":"ls"}`, Response: ch}
	m.approvalOptions = defaultApprovalOptions()
	m.approvalCursor = len(m.approvalOptions) - 1 // feedback row

	updated, _ := m.Update(tea.KeyPressMsg{Text: "ab"})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = updated.(Model)
	if got := string(m.approvalFeedbackBuf); got != "a" {
		t.Fatalf("expected backspace to trim buffer to %q, got %q", "a", got)
	}

	// Up leaves the feedback row; on other rows "n" stays the deny shortcut.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.approvalOptions[m.approvalCursor].shortcut == "e" {
		t.Fatal("expected cursor to leave feedback row after Up")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Text: "n"})
	m = updated.(Model)
	if got := <-ch; got != permission.Deny {
		t.Fatalf("expected plain Deny from n shortcut, got %v", got)
	}
}

// TestApproval_PlainDenyCarriesNoFeedback pins the regression guard: the
// existing y/n shortcuts still resolve without any feedback stash.
func TestApproval_PlainDenyCarriesNoFeedback(t *testing.T) {
	m := newTestModel()
	ch := make(chan permission.Decision, 1)
	box := &approvalFeedbackBox{}
	m.pendingApproval = &ApprovalMsg{ToolName: "run_command", Input: `{"command":"ls"}`, Response: ch}
	m.approvalOptions = defaultApprovalOptions()
	m.approvalCursor = 0
	m.approvalFeedbackBox = box

	updated, _ := m.Update(tea.KeyPressMsg{Text: "n"})
	m = updated.(Model)
	if got := <-ch; got != permission.Deny {
		t.Fatalf("expected Deny, got %v", got)
	}
	if got := box.take(); got != "" {
		t.Fatalf("expected no feedback on plain deny, got %q", got)
	}
}
