package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/permission"
)

// TestReviewCTA_EnterApproves tests that when pendingHarnessReview is set,
// pressing Enter triggers the review approve flow and clears the pending state.
func TestReviewCTA_EnterApproves(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	// Simulate a completed task awaiting review
	task := &harness.Task{
		ID:                 "t-review-1",
		Goal:               "Fix auth bug",
		Status:             harness.TaskCompleted,
		VerificationStatus: harness.VerificationPassed,
		ReviewStatus:       harness.ReviewPending,
	}
	m.pendingHarnessReview = task

	// Press Enter
	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := model.(Model)

	// pendingHarnessReview should be cleared
	if m2.pendingHarnessReview != nil {
		t.Error("pendingHarnessReview should be nil after Enter")
	}

	// A command should have been issued (the review approve cmd)
	if cmd == nil {
		t.Error("expected a non-nil command from review approve")
	}

	// A system message should mention the task
	output := renderedOutput(&m2)
	if !strings.Contains(output, "t-review-1") {
		t.Errorf("output should mention task ID, got: %s", output)
	}
}

// TestReviewCTA_EscSkips tests that pressing Esc with pendingHarnessReview
// dismisses the review CTA without approving.
func TestReviewCTA_EscSkips(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	task := &harness.Task{
		ID:                 "t-review-2",
		Status:             harness.TaskCompleted,
		VerificationStatus: harness.VerificationPassed,
		ReviewStatus:       harness.ReviewPending,
	}
	m.pendingHarnessReview = task

	// Press Esc
	model, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m2 := model.(Model)

	// pendingHarnessReview should be cleared
	if m2.pendingHarnessReview != nil {
		t.Error("pendingHarnessReview should be nil after Esc")
	}

	// A message about skipping should appear
	output := renderedOutput(&m2)
	if !strings.Contains(output, "skipped") && !strings.Contains(output, "later") {
		t.Errorf("output should mention skip or later, got: %s", output)
	}
}

// TestPromoteCTA_EnterPromotes tests that when pendingHarnessPromote is set,
// pressing Enter triggers the promote flow.
func TestPromoteCTA_EnterPromotes(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	task := &harness.Task{
		ID:                 "t-promote-1",
		Goal:               "Fix auth bug",
		Status:             harness.TaskCompleted,
		VerificationStatus: harness.VerificationPassed,
		ReviewStatus:       harness.ReviewApproved,
		PromotionStatus:    "",
	}
	m.pendingHarnessPromote = task

	// Press Enter
	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := model.(Model)

	// pendingHarnessPromote should be cleared
	if m2.pendingHarnessPromote != nil {
		t.Error("pendingHarnessPromote should be nil after Enter")
	}

	// A command should have been issued (the promote cmd)
	if cmd == nil {
		t.Error("expected a non-nil command from promote apply")
	}

	// A system message should mention promoting
	output := renderedOutput(&m2)
	if !strings.Contains(output, "t-promote-1") {
		t.Errorf("output should mention task ID, got: %s", output)
	}
}

// TestPromoteCTA_EscSkips tests that pressing Esc with pendingHarnessPromote
// dismisses the promote CTA without promoting.
func TestPromoteCTA_EscSkips(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	task := &harness.Task{
		ID:              "t-promote-2",
		ReviewStatus:    harness.ReviewApproved,
		PromotionStatus: "",
	}
	m.pendingHarnessPromote = task

	// Press Esc
	model, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m2 := model.(Model)

	if m2.pendingHarnessPromote != nil {
		t.Error("pendingHarnessPromote should be nil after Esc")
	}

	output := renderedOutput(&m2)
	if !strings.Contains(output, "skipped") && !strings.Contains(output, "later") {
		t.Errorf("output should mention skip or later, got: %s", output)
	}
}

// TestCTA_PriorityOrder tests that review CTA takes priority over promote CTA
// if both are somehow set (shouldn't happen, but verify defense).
func TestCTA_PriorityOrder_ReviewBeforePromote(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	m.pendingHarnessReview = &harness.Task{ID: "t-review-3"}
	m.pendingHarnessPromote = &harness.Task{ID: "t-promote-3"}

	// Press Esc — review handler should fire first
	model, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m2 := model.(Model)

	// Review should be cleared, promote should remain
	if m2.pendingHarnessReview != nil {
		t.Error("pendingHarnessReview should be nil after Esc")
	}
	if m2.pendingHarnessPromote == nil {
		t.Error("pendingHarnessPromote should still be set (review cleared first)")
	}
}

// TestCTA_StaleCleanupOnNewInput tests that new text submission clears
// pendingAutoRun. Note: pendingHarnessReview/pendingHarnessPromote are NOT
// cleared on Enter because their Enter handlers run first (approve/promote).
// They are cleared only when the user types a new message while no pending
// state is active, which is the expected UX — the user must explicitly
// Esc-skip before submitting new text.
func TestCTA_StaleCleanupOnNewInput(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	// pendingAutoRun IS cleared on new text submission
	m.pendingAutoRun = &harness.AutoRunResult{Decision: harness.RouteSuggest}
	m.pendingAutoRunText = "old suggestion"
	m.input.SetValue("new unrelated question")

	// Press Enter to submit
	model, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := model.(Model)

	if m2.pendingAutoRun != nil {
		t.Error("pendingAutoRun should be cleared on new text submission")
	}
	if m2.pendingAutoRunText != "" {
		t.Error("pendingAutoRunText should be cleared on new text submission")
	}
}

// TestHarnessRunResult_SetsPendingReview verifies that a harnessRunResultMsg
// with CTAReview sets pendingHarnessReview.
func TestHarnessRunResult_SetsPendingReview(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	task := &harness.Task{
		ID:                 "t-cta-review",
		Status:             harness.TaskCompleted,
		VerificationStatus: harness.VerificationPassed,
		ReviewStatus:       harness.ReviewPending,
	}

	msg := harnessRunResultMsg{
		Summary: &harness.RunSummary{
			Task: task,
		},
		CTA:        harness.CTAReview,
		CTAMessage: "Review changes with /harness review approve t-cta-review",
	}

	model, _ := m.Update(msg)
	m2 := model.(Model)

	if m2.pendingHarnessReview == nil {
		t.Fatal("pendingHarnessReview should be set after harnessRunResultMsg with CTAReview")
	}
	if m2.pendingHarnessReview.ID != "t-cta-review" {
		t.Errorf("pendingHarnessReview.ID = %q, want %q", m2.pendingHarnessReview.ID, "t-cta-review")
	}

	output := renderedOutput(&m2)
	if !strings.Contains(output, "Press Enter to approve") {
		t.Errorf("output should contain review CTA prompt, got: %s", output)
	}
}

// TestReviewResult_SetsPendingPromote verifies that a harnessReviewResultMsg
// with ReviewApproved sets pendingHarnessPromote.
func TestReviewResult_SetsPendingPromote(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	msg := harnessReviewResultMsg{
		Task: &harness.Task{
			ID:              "t-cta-promote",
			ReviewStatus:    harness.ReviewApproved,
			PromotionStatus: "",
		},
		TaskID: "t-cta-promote",
	}

	model, _ := m.Update(msg)
	m2 := model.(Model)

	if m2.pendingHarnessPromote == nil {
		t.Fatal("pendingHarnessPromote should be set after harnessReviewResultMsg with ReviewApproved")
	}
	if m2.pendingHarnessPromote.ID != "t-cta-promote" {
		t.Errorf("pendingHarnessPromote.ID = %q, want %q", m2.pendingHarnessPromote.ID, "t-cta-promote")
	}

	output := renderedOutput(&m2)
	if !strings.Contains(output, "Press Enter to promote") {
		t.Errorf("output should contain promote CTA prompt, got: %s", output)
	}
}

// TestPromoteResult_ClearsPendingState verifies that a harnessPromoteResultMsg
// does not leave stale pending state.
func TestPromoteResult_ClearsPendingState(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	msg := harnessPromoteResultMsg{
		Task: &harness.Task{
			ID:              "t-promoted",
			PromotionStatus: harness.PromotionApplied,
		},
		TaskID: "t-promoted",
	}

	model, _ := m.Update(msg)
	m2 := model.(Model)

	// No pending state should remain
	if m2.pendingHarnessReview != nil {
		t.Error("pendingHarnessReview should be nil after promote result")
	}
	if m2.pendingHarnessPromote != nil {
		t.Error("pendingHarnessPromote should be nil after promote result")
	}

	output := renderedOutput(&m2)
	if !strings.Contains(output, "promoted") {
		t.Errorf("output should mention promoted, got: %s", output)
	}
}
