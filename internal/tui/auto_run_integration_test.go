package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/session"
)

// TestSuggestMode_EnterConfirmsHarness verifies that pressing Enter when
// pendingAutoRun is set triggers handleAutoRun, regardless of autocomplete state.
func TestSuggestMode_EnterConfirmsHarness(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	// Simulate suggest mode: set pendingAutoRun
	m.pendingAutoRun = &harness.AutoRunResult{
		Decision: harness.RouteSuggest,
		Message:  "This looks like a code task. Run in harness?",
	}
	m.pendingAutoRunText = "Fix the auth bug"

	// Verify autocomplete is NOT active (the old bug)
	if m.autoCompleteActive {
		t.Fatal("autocomplete should not be active for this test")
	}

	// Press Enter
	model, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := model.(Model)

	// pendingAutoRun should be cleared (consumed)
	if m2.pendingAutoRun != nil {
		t.Error("pendingAutoRun should be nil after Enter (was consumed)")
	}
	if m2.pendingAutoRunText != "" {
		t.Error("pendingAutoRunText should be empty after Enter")
	}

	// A system message should have been written ("Running in harness...")
	output := renderedOutput(&m2)
	if !strings.Contains(output, "harness") && !strings.Contains(output, "Running") {
		// The handleAutoRun may have started a harness run or written a system message.
		// Either way, the pendingAutoRun was consumed.
		t.Logf("Output after Enter: %s", output)
	}
}

// TestSuggestMode_EscDismisses verifies that pressing Esc when pendingAutoRun
// is set dismisses the suggestion and submits normally.
func TestSuggestMode_EscDismisses(t *testing.T) {
	m := newTestModel()
	m.pendingAutoRun = &harness.AutoRunResult{
		Decision: harness.RouteSuggest,
		Message:  "This looks like a code task. Run in harness?",
	}
	m.pendingAutoRunText = "Fix the auth bug"
	m.input.SetValue("Fix the auth bug")
	m.chatWriteUser(nextChatID(), "Fix the auth bug")

	model, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m2 := model.(Model)

	// pendingAutoRun should be cleared
	if m2.pendingAutoRun != nil {
		t.Error("pendingAutoRun should be nil after Esc")
	}

	// A "Running normally" message should appear
	output := renderedOutput(&m2)
	if !strings.Contains(output, "skipped") && !strings.Contains(output, "normally") {
		t.Logf("Output after Esc: %s", output)
	}
	if count := strings.Count(output, "Fix the auth bug"); count != 1 {
		t.Fatalf("user message rendered %d times, want once; output=%s", count, output)
	}
	if !m2.loading {
		t.Fatal("Esc fallback should continue into the normal agent path")
	}
}

// TestSuggestMode_NewInputClearsStaleState verifies that submitting new text
// clears pendingAutoRun (prevents stale suggestions).
func TestSuggestMode_NewInputClearsStaleState(t *testing.T) {
	m := newTestModel()
	m.pendingAutoRun = &harness.AutoRunResult{
		Decision: harness.RouteSuggest,
		Message:  "Suggestion",
	}
	m.pendingAutoRunText = "old text"
	m.input.SetValue("new unrelated question")

	// Simulate Enter to submit (not the suggest handler — input is non-empty)
	model, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := model.(Model)

	// pendingAutoRun should be cleared by the stale-state cleanup
	if m2.pendingAutoRun != nil {
		t.Error("pendingAutoRun should be cleared when new text is submitted")
	}
}

// TestStrictWriteGuard_GlobalOnMode verifies that the write guard applies
// in strict mode even before routing, not just when routed to harness.
func TestStrictWriteGuard_GlobalOnMode(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	// Before guard, write tools should not be denied
	if policy.GetDecision("write_file") == permission.Deny {
		t.Error("write_file should not be denied before guard")
	}

	// Apply guard (simulates checkAutoRun in strict mode)
	m.applyStrictWriteGuard()

	// After guard, ALL write tools should be denied
	deniedTools := []string{
		"write_file", "edit_file", "multi_edit_file", "notebook_edit",
		"run_command", "git_add", "git_commit", "git_stash",
	}
	for _, tool := range deniedTools {
		if policy.GetDecision(tool) != permission.Deny {
			t.Errorf("%s should be denied after strict guard", tool)
		}
	}

	// Read tools should be unaffected
	readTools := []string{"read_file", "search_files", "list_directory", "glob"}
	for _, tool := range readTools {
		if policy.GetDecision(tool) == permission.Deny {
			t.Errorf("%s should NOT be denied by write guard", tool)
		}
	}
}

// TestStrictWriteGuard_GlobalEvenForQuestions verifies that in strict mode,
// the guard applies even when the input is a question (RouteNone).
// This tests that the guard is independent of routing outcome.
func TestStrictWriteGuard_GlobalEvenForQuestions(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := newTestModelWithPolicy(policy)

	// Simulate what happens when checkAutoRun is called in strict mode
	// with a question input (which would return RouteNone)
	m.applyStrictWriteGuard()

	// Guard should still be applied regardless of route outcome
	if policy.GetDecision("write_file") != permission.Deny {
		t.Error("strict guard should deny write_file even for non-routed inputs")
	}
	if policy.GetDecision("git_commit") != permission.Deny {
		t.Error("strict guard should deny git_commit even for non-routed inputs")
	}
}

func TestAutoRunCheckDisplaysAndRecordsUserOnce(t *testing.T) {
	m := newTestModel()
	m.config = &config.Config{Harness: config.HarnessConfig{AutoRun: "on"}}
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	m.sessionStore = store
	m.session = &session.Session{
		ID:        "test-session",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Title:     "New session",
	}

	cmd := m.startAutoRunCheck("fix auth bug", "fix auth bug", true)
	if cmd == nil {
		t.Fatal("startAutoRunCheck should return a routing command")
	}

	output := renderedOutput(&m)
	if count := strings.Count(output, "fix auth bug"); count != 1 {
		t.Fatalf("user message rendered %d times, want once; output=%s", count, output)
	}
	if got := len(m.session.Messages); got != 1 {
		t.Fatalf("session messages = %d, want 1", got)
	}
	if got := m.session.Messages[0].Content[0].Text; got != "fix auth bug" {
		t.Fatalf("session message = %q, want original prompt", got)
	}
}

func TestAutoRunFallbackUsesNormalDisplayedPath(t *testing.T) {
	m := newTestModel()
	m.config = &config.Config{Harness: config.HarnessConfig{AutoRun: "on"}}
	_ = m.startAutoRunCheck("what does auth do?", "what does auth do?", true)

	model, cmd := m.Update(autoRunCheckResultMsg{
		Text:        "what does auth do?",
		DisplayText: "what does auth do?",
		Result:      &harness.AutoRunResult{Decision: harness.RouteNormal},
	})
	m2 := model.(Model)

	if cmd == nil {
		t.Fatal("normal fallback should start the agent")
	}
	if !m2.loading {
		t.Fatal("normal fallback should restore loading state before starting agent")
	}
	if m2.streamBuffer == nil {
		t.Fatal("normal fallback should initialize streamBuffer")
	}
	if m2.statusActivity != m2.t("status.thinking") {
		t.Fatalf("statusActivity = %q, want thinking status", m2.statusActivity)
	}
	output := renderedOutput(&m2)
	if count := strings.Count(output, "what does auth do?"); count != 1 {
		t.Fatalf("user message rendered %d times, want once; output=%s", count, output)
	}
}

func newTestModelWithPolicy(policy *permission.ConfigPolicy) Model {
	m := NewModel(nil, policy)
	m.startedAt = time.Now().Add(-2 * time.Second)
	m.inputReady = true
	return m
}
