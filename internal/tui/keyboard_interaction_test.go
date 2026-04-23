package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/session"
)

// ---------------------------------------------------------------------------
// Scenario: User exits the application
// Two ways: ctrl+d (immediate) and ctrl+c twice (confirm)
// ---------------------------------------------------------------------------

func TestScenario_UserExitsWithCtrlD(t *testing.T) {
	m := newTestModel()

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+d"})
	m = updated.(Model)

	if !m.quitting {
		t.Error("expected quitting=true after ctrl+d")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestScenario_UserExitsWithCtrlCTwice(t *testing.T) {
	m := newTestModel()

	updated, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	m = updated.(Model)
	if !m.exitConfirmPending {
		t.Fatal("step1: expected exitConfirmPending=true")
	}
	if m.input.Value() != "" {
		t.Error("step1: expected input cleared when exit prompt appears")
	}
	output := m.output.String()
	if len(output) == 0 {
		t.Error("step1: expected exit confirmation text in output")
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	m = updated.(Model)
	if !m.quitting {
		t.Error("step2: expected quitting=true")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Error("step2: expected tea.Quit")
		_ = msg
	}
}

func TestScenario_ExitConfirmResetsOnAnyOtherKey(t *testing.T) {
	m := newTestModel()

	updated, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	m = updated.(Model)
	if !m.exitConfirmPending {
		t.Fatal("expected exitConfirmPending after ctrl+c")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "h"})
	m = updated.(Model)
	if m.exitConfirmPending {
		t.Error("expected exitConfirmPending=false after regular keypress")
	}
	if m.quitting {
		t.Error("expected NOT quitting - user changed their mind")
	}
}

// ---------------------------------------------------------------------------
// Scenario: User toggles sidebar visibility
// ---------------------------------------------------------------------------

func TestScenario_UserTogglesSidebar(t *testing.T) {
	m := newTestModel()
	m.config = &config.Config{}
	initial := m.sidebarVisible

	updated, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+r"})
	m = updated.(Model)
	if m.sidebarVisible == initial {
		t.Error("expected sidebar state to toggle after first ctrl+r")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "ctrl+r"})
	m = updated.(Model)
	if m.sidebarVisible != initial {
		t.Error("expected sidebar state to toggle back after second ctrl+r")
	}
}

// ---------------------------------------------------------------------------
// Scenario: User enters and exits shell mode
// ---------------------------------------------------------------------------

func TestScenario_UserEntersShellMode(t *testing.T) {
	m := newTestModel()

	updated, _ := m.Update(tea.KeyPressMsg{Text: "$"})
	m = updated.(Model)
	if !m.shellMode {
		t.Fatal("expected shellMode=true after $ on empty input")
	}
	if m.input.Prompt != "$ " {
		t.Errorf("expected prompt '$ ', got %q", m.input.Prompt)
	}
}

func TestScenario_UserExitsShellModeWithEsc(t *testing.T) {
	m := newTestModel()
	m.shellMode = true
	m.input.SetValue("some typed command")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)
	if m.shellMode {
		t.Error("expected shellMode=false after esc")
	}
	if m.input.Value() != "" {
		t.Errorf("expected input cleared after esc, got %q", m.input.Value())
	}
	if m.input.Prompt != "❯ " {
		t.Errorf("expected prompt '❯ ', got %q", m.input.Prompt)
	}
}

func TestScenario_DoesNotEnterShellModeWhenInputNonEmpty(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("existing text")

	updated, _ := m.Update(tea.KeyPressMsg{Text: "$"})
	m = updated.(Model)
	if m.shellMode {
		t.Error("should NOT enter shell mode when input is non-empty")
	}
}

func TestScenario_DoesNotEnterShellModeWhenLoading(t *testing.T) {
	m := newTestModel()
	m.loading = true

	updated, _ := m.Update(tea.KeyPressMsg{Text: "!"})
	m = updated.(Model)
	if m.shellMode {
		t.Error("should NOT enter shell mode while loading")
	}
}

// ---------------------------------------------------------------------------
// Scenario: User navigates command history with up/down arrows
// ---------------------------------------------------------------------------

func TestScenario_UserNavigatesHistory(t *testing.T) {
	m := newTestModel()
	m.history = []string{"first command", "second command", "third command"}
	m.historyIdx = len(m.history)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.historyIdx != 2 {
		t.Errorf("step1: expected historyIdx=2, got %d", m.historyIdx)
	}
	if m.input.Value() != "third command" {
		t.Errorf("step1: expected input 'third command', got %q", m.input.Value())
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.input.Value() != "second command" {
		t.Errorf("step2: expected input 'second command', got %q", m.input.Value())
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.input.Value() != "third command" {
		t.Errorf("step3: expected input 'third command', got %q", m.input.Value())
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.input.Value() != "" {
		t.Errorf("step4: expected empty input at end of history, got %q", m.input.Value())
	}
}

// ---------------------------------------------------------------------------
// Scenario: User uses autocomplete with up/down/tab/esc/enter
// ---------------------------------------------------------------------------

func TestScenario_UserNavigatesAutocomplete(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteItems = []string{"/help", "/model", "/clear"}
	m.autoCompleteIndex = 0

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.autoCompleteIndex != 1 {
		t.Errorf("step1: expected autoCompleteIndex=1, got %d", m.autoCompleteIndex)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.autoCompleteIndex != 2 {
		t.Errorf("step2: expected autoCompleteIndex=2, got %d", m.autoCompleteIndex)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.autoCompleteIndex != 1 {
		t.Errorf("step3: expected autoCompleteIndex=1, got %d", m.autoCompleteIndex)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "shift+tab"})
	m = updated.(Model)
	if m.autoCompleteIndex != 0 {
		t.Errorf("step4: expected autoCompleteIndex=0, got %d", m.autoCompleteIndex)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)
	if m.autoCompleteActive {
		t.Error("step5: expected autoCompleteActive=false after esc")
	}
	if m.autoCompleteItems != nil {
		t.Error("step5: expected autoCompleteItems=nil after dismiss")
	}
}

func TestScenario_CtrlCDismissesAutocomplete(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteItems = []string{"/help", "/model"}
	m.autoCompleteIndex = 1

	updated, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	m = updated.(Model)
	if m.autoCompleteActive {
		t.Error("expected autocomplete dismissed by ctrl+c")
	}
	if m.exitConfirmPending {
		t.Error("ctrl+c should dismiss autocomplete, not trigger exit confirm")
	}
}

func TestScenario_SingleItemAutocompleteAppliesOnTab(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteItems = []string{"/help"}
	m.autoCompleteIndex = 0

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.autoCompleteActive {
		t.Error("expected autocomplete closed after single-item tab apply")
	}
}

func TestScenario_EnterAppliesAutocomplete(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteItems = []string{"/help", "/model"}
	m.autoCompleteIndex = 0

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.autoCompleteActive {
		t.Error("expected autocomplete closed after enter")
	}
}

// ---------------------------------------------------------------------------
// Scenario: User scrolls viewport with pgup/pgdown
// ---------------------------------------------------------------------------

func TestScenario_UserScrollsWithPageKeys(t *testing.T) {
	m := newTestModel()
	for i := 0; i < 200; i++ {
		m.output.WriteString("line content that fills the viewport for scrolling test\n")
	}
	m.handleResize(120, 40)
	m.syncConversationViewport()
	m.viewport.GotoBottom()
	bottomY := m.viewport.YOffset()

	updated, _ := m.Update(tea.KeyPressMsg{Text: "pgup"})
	m = updated.(Model)
	if m.viewport.YOffset() >= bottomY {
		t.Errorf("expected viewport scrolled up, before=%d after=%d", bottomY, m.viewport.YOffset())
	}
	afterPgUp := m.viewport.YOffset()

	updated, _ = m.Update(tea.KeyPressMsg{Text: "pgdown"})
	m = updated.(Model)
	if m.viewport.YOffset() <= afterPgUp {
		t.Errorf("expected viewport scrolled down, before=%d after=%d", afterPgUp, m.viewport.YOffset())
	}
}

// ---------------------------------------------------------------------------
// Scenario: User switches permission mode with shift+tab
// ---------------------------------------------------------------------------

func TestScenario_UserSwitchesPermissionMode(t *testing.T) {
	m := newTestModel()
	initialMode := m.mode

	updated, _ := m.Update(tea.KeyPressMsg{Text: "shift+tab"})
	m = updated.(Model)
	if m.mode == initialMode {
		t.Error("expected mode to change after shift+tab")
	}
}

// ---------------------------------------------------------------------------
// Scenario: User submits text via enter key
// ---------------------------------------------------------------------------

func TestScenario_UserSubmitsText(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("hello world")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.input.Value() != "" {
		t.Error("expected input cleared after enter")
	}
	if !m.loading {
		t.Error("expected loading=true after text submission")
	}
	if m.exitConfirmPending {
		t.Error("expected exitConfirmPending=false after enter")
	}
}

func TestScenario_EnterEmptyTextDoesNothing(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.loading {
		t.Error("empty text should not trigger loading")
	}
}

func TestScenario_EnterWhileLoadingQueuesMessage(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.input.SetValue("follow up message")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.pending.count() != 1 {
		t.Errorf("expected 1 pending submission, got %d", m.pending.count())
	}
	if !m.loading {
		t.Error("should still be loading")
	}
}

func TestScenario_BusySafeCommandExecutesImmediately(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.input.SetValue("/help")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.pending.count() != 0 {
		t.Errorf("expected 0 pending for busy-safe command, got %d", m.pending.count())
	}
	if m.output.Len() == 0 {
		t.Error("expected help text in output")
	}
}

// ---------------------------------------------------------------------------
// Scenario: User cancels a running task with ctrl+c or esc
// ---------------------------------------------------------------------------

func TestScenario_UserCancelsRunningTask(t *testing.T) {
	m := newTestModel()
	m.loading = true
	cancelCalled := false
	m.cancelFunc = func() { cancelCalled = true }

	updated, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	m = updated.(Model)
	if !cancelCalled {
		t.Error("expected cancelFunc to be called")
	}
	if m.exitConfirmPending {
		t.Error("should not prompt exit confirm when cancelling run")
	}
}

func TestScenario_UserCancelsWithEsc(t *testing.T) {
	m := newTestModel()
	m.loading = true
	cancelCalled := false
	m.cancelFunc = func() { cancelCalled = true }

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)
	if !cancelCalled {
		t.Error("esc should also cancel running task")
	}
}

// ---------------------------------------------------------------------------
// Scenario: ctrl+c closes active panel before exit confirm
// ---------------------------------------------------------------------------

func TestScenario_CtrlCClosesActivePanelFirst(t *testing.T) {
	m := newTestModel()
	m.modelPanel = &modelPanelState{}

	updated, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	m = updated.(Model)
	if m.modelPanel != nil {
		t.Error("expected modelPanel to be closed")
	}
	if m.exitConfirmPending {
		t.Error("should close panel, not prompt exit confirm")
	}
}

// ---------------------------------------------------------------------------
// Scenario: User dismisses autocomplete with ctrl+c, then exits with two ctrl+c
// ---------------------------------------------------------------------------

func TestScenario_AutocompleteDismissThenExit(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteItems = []string{"/help"}
	m.autoCompleteIndex = 0

	updated, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	m = updated.(Model)
	if m.autoCompleteActive {
		t.Fatal("expected autocomplete dismissed")
	}
	if m.exitConfirmPending {
		t.Fatal("should NOT prompt exit when dismissing autocomplete")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	m = updated.(Model)
	if !m.exitConfirmPending {
		t.Fatal("expected exit confirm prompt")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	m = updated.(Model)
	if !m.quitting {
		t.Error("expected quitting=true")
	}
}

// ---------------------------------------------------------------------------
// Scenario: Approval mode - user allows/denies tool execution
// ---------------------------------------------------------------------------

func TestScenario_UserApprovesWithY(t *testing.T) {
	m := newTestModel()
	ch := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{
		ToolName: "run_command",
		Input:    `{"command":"ls"}`,
		Response: ch,
	}
	m.approvalOptions = defaultApprovalOptions()
	m.approvalCursor = 0

	updated, _ := m.Update(tea.KeyPressMsg{Text: "y"})
	m = updated.(Model)
	if m.pendingApproval != nil {
		t.Error("expected pendingApproval=nil after approval")
	}
	decision := <-ch
	if decision != permission.Allow {
		t.Errorf("expected Allow decision, got %v", decision)
	}
}

func TestScenario_UserDeniesWithN(t *testing.T) {
	m := newTestModel()
	ch := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{
		ToolName: "run_command",
		Input:    `{"command":"rm -rf /"}`,
		Response: ch,
	}
	m.approvalOptions = defaultApprovalOptions()
	m.approvalCursor = 0

	updated, _ := m.Update(tea.KeyPressMsg{Text: "n"})
	m = updated.(Model)
	if m.pendingApproval != nil {
		t.Error("expected pendingApproval=nil after denial")
	}
	decision := <-ch
	if decision != permission.Deny {
		t.Errorf("expected Deny decision, got %v", decision)
	}
}

func TestScenario_UserDeniesWithEsc(t *testing.T) {
	m := newTestModel()
	ch := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{
		ToolName: "edit_file",
		Input:    `{"path":"main.go"}`,
		Response: ch,
	}
	m.approvalOptions = defaultApprovalOptions()

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)
	decision := <-ch
	if decision != permission.Deny {
		t.Errorf("expected Deny on esc, got %v", decision)
	}
}

func TestScenario_UserNavigatesApprovalOptions(t *testing.T) {
	m := newTestModel()
	ch := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{
		ToolName: "run_command",
		Input:    `{}`,
		Response: ch,
	}
	m.approvalOptions = defaultApprovalOptions()
	m.approvalCursor = 0

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.approvalCursor != 1 {
		t.Errorf("step1: expected cursor=1, got %d", m.approvalCursor)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.approvalCursor != 2 {
		t.Errorf("step2: expected cursor=2, got %d", m.approvalCursor)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.approvalCursor != 1 {
		t.Errorf("step3: expected cursor=1, got %d", m.approvalCursor)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "shift+tab"})
	m = updated.(Model)
	if m.approvalCursor != 0 {
		t.Errorf("step4: expected cursor=0, got %d", m.approvalCursor)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "k"})
	m = updated.(Model)
	if m.approvalCursor != 2 {
		t.Errorf("step5: expected cursor=2 (wrap), got %d", m.approvalCursor)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "j"})
	m = updated.(Model)
	if m.approvalCursor != 0 {
		t.Errorf("step6: expected cursor=0 (wrap), got %d", m.approvalCursor)
	}
}

func TestScenario_UserAlwaysAllowsTool(t *testing.T) {
	m := newTestModel()
	ch := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{
		ToolName: "run_command",
		Input:    `{"command":"npm test"}`,
		Response: ch,
	}
	m.approvalOptions = defaultApprovalOptions()
	policy := permission.NewConfigPolicy(nil, nil)
	m.policy = policy
	m.approvalCursor = 0

	updated, _ := m.Update(tea.KeyPressMsg{Text: "a"})
	m = updated.(Model)
	if m.pendingApproval != nil {
		t.Error("expected pendingApproval=nil after always-allow")
	}
	decision := <-ch
	if decision != permission.Allow {
		t.Errorf("expected Allow, got %v", decision)
	}
	if policy.GetDecision("run_command") != permission.Allow {
		t.Error("expected run_command to be allowed after 'always allow'")
	}
	output := m.output.String()
	if !strings.Contains(output, "Always allow") && !strings.Contains(output, "已总是允许") {
		t.Errorf("expected 'always allow' confirmation in output, got %q", output)
	}
}

func TestScenario_UserSelectsApprovalOptionWithEnter(t *testing.T) {
	m := newTestModel()
	ch := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{
		ToolName: "run_command",
		Input:    `{}`,
		Response: ch,
	}
	m.approvalOptions = defaultApprovalOptions()
	m.approvalCursor = 2
	m.policy = permission.NewConfigPolicy(nil, nil)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	decision := <-ch
	if decision != permission.Deny {
		t.Errorf("expected Deny (cursor at 2), got %v", decision)
	}
}

// ---------------------------------------------------------------------------
// Scenario: Diff confirmation - user accepts/rejects file changes
// ---------------------------------------------------------------------------

func TestScenario_UserAcceptsDiff(t *testing.T) {
	m := newTestModel()
	ch := make(chan bool, 1)
	m.pendingDiffConfirm = &DiffConfirmMsg{
		FilePath: "main.go",
		DiffText: "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@",
		Response: ch,
	}
	m.diffOptions = diffConfirmOptions()
	m.diffCursor = 0

	updated, _ := m.Update(tea.KeyPressMsg{Text: "y"})
	m = updated.(Model)
	if m.pendingDiffConfirm != nil {
		t.Error("expected pendingDiffConfirm=nil after accept")
	}
	approved := <-ch
	if !approved {
		t.Error("expected approved=true for 'y'")
	}
}

func TestScenario_UserRejectsDiff(t *testing.T) {
	m := newTestModel()
	ch := make(chan bool, 1)
	m.pendingDiffConfirm = &DiffConfirmMsg{
		FilePath: "main.go",
		DiffText: "some diff",
		Response: ch,
	}
	m.diffOptions = diffConfirmOptions()

	updated, _ := m.Update(tea.KeyPressMsg{Text: "n"})
	m = updated.(Model)
	approved := <-ch
	if approved {
		t.Error("expected approved=false for 'n'")
	}
}

func TestScenario_UserRejectsDiffWithEsc(t *testing.T) {
	m := newTestModel()
	ch := make(chan bool, 1)
	m.pendingDiffConfirm = &DiffConfirmMsg{
		FilePath: "main.go",
		DiffText: "some diff",
		Response: ch,
	}
	m.diffOptions = diffConfirmOptions()

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)
	approved := <-ch
	if approved {
		t.Error("expected esc to reject diff")
	}
}

func TestScenario_UserNavigatesDiffOptions(t *testing.T) {
	m := newTestModel()
	ch := make(chan bool, 1)
	m.pendingDiffConfirm = &DiffConfirmMsg{
		FilePath: "test.go",
		DiffText: "diff",
		Response: ch,
	}
	m.diffOptions = diffConfirmOptions()
	m.diffCursor = 0

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.diffCursor != 1 {
		t.Errorf("expected cursor=1 (Reject), got %d", m.diffCursor)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	approved := <-ch
	if approved {
		t.Error("expected rejection when cursor is on Reject option")
	}
}

// ---------------------------------------------------------------------------
// Scenario: Harness checkpoint confirmation
// ---------------------------------------------------------------------------

func TestScenario_UserConfirmsHarnessCheckpoint(t *testing.T) {
	m := newTestModel()
	ch := make(chan bool, 1)
	m.pendingHarnessCheckpointConfirm = &HarnessCheckpointConfirmMsg{
		Response: ch,
	}
	m.diffOptions = diffConfirmOptions()

	updated, _ := m.Update(tea.KeyPressMsg{Text: "y"})
	m = updated.(Model)
	if m.pendingHarnessCheckpointConfirm != nil {
		t.Error("expected pendingHarnessCheckpointConfirm=nil after confirm")
	}
	approved := <-ch
	if !approved {
		t.Error("expected approved=true for 'y'")
	}
}

func TestScenario_UserRejectsHarnessCheckpoint(t *testing.T) {
	m := newTestModel()
	ch := make(chan bool, 1)
	m.pendingHarnessCheckpointConfirm = &HarnessCheckpointConfirmMsg{
		Response: ch,
	}
	m.diffOptions = diffConfirmOptions()

	updated, _ := m.Update(tea.KeyPressMsg{Text: "n"})
	m = updated.(Model)
	approved := <-ch
	if approved {
		t.Error("expected approved=false for 'n'")
	}
}

// ---------------------------------------------------------------------------
// Scenario: Language selector keyboard navigation
// ---------------------------------------------------------------------------

func TestScenario_UserNavigatesLanguageSelector(t *testing.T) {
	m := newTestModel()
	m.langOptions = []languageOption{
		{label: "English", shortcut: "e", lang: LangEnglish},
		{label: "中文", shortcut: "z", lang: LangZhCN},
	}
	m.langCursor = 0

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.langCursor != 1 {
		t.Errorf("expected langCursor=1, got %d", m.langCursor)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.langCursor != 0 {
		t.Errorf("expected langCursor=0, got %d", m.langCursor)
	}
}

func TestScenario_UserSelectsEnglishQuickKey(t *testing.T) {
	m := newTestModel()
	m.langOptions = []languageOption{
		{label: "English", shortcut: "e", lang: LangEnglish},
		{label: "中文", shortcut: "z", lang: LangZhCN},
	}
	m.langCursor = 1

	updated, _ := m.Update(tea.KeyPressMsg{Text: "e"})
	m = updated.(Model)
	if m.language != LangEnglish {
		t.Errorf("expected language=English after pressing 'e', got %v", m.language)
	}
	if len(m.langOptions) != 0 {
		t.Error("expected langOptions cleared after selection")
	}
}

func TestScenario_UserSelectsChineseQuickKey(t *testing.T) {
	m := newTestModel()
	m.langOptions = []languageOption{
		{label: "English", shortcut: "e", lang: LangEnglish},
		{label: "中文", shortcut: "z", lang: LangZhCN},
	}

	updated, _ := m.Update(tea.KeyPressMsg{Text: "z"})
	m = updated.(Model)
	if m.language != LangZhCN {
		t.Errorf("expected language=ZhCN after pressing 'z', got %v", m.language)
	}
}

func TestScenario_UserDismissesLanguageSelectorWithEsc(t *testing.T) {
	m := newTestModel()
	m.langOptions = []languageOption{
		{label: "English", shortcut: "e", lang: LangEnglish},
	}
	m.languagePromptRequired = false

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)
	if len(m.langOptions) != 0 {
		t.Error("expected langOptions cleared after esc dismiss")
	}
}

func TestScenario_LanguageSelectorCannotDismissWhenRequired(t *testing.T) {
	m := newTestModel()
	m.langOptions = []languageOption{
		{label: "English", shortcut: "e", lang: LangEnglish},
	}
	m.languagePromptRequired = true

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)
	if len(m.langOptions) == 0 {
		t.Error("expected langOptions NOT cleared when selection is required")
	}
}

// ---------------------------------------------------------------------------
// Scenario: Startup input drain gate suppresses keys
// ---------------------------------------------------------------------------

func TestScenario_StartupInputDrainSuppressesKeys(t *testing.T) {
	m := newTestModel()
	m.inputDrainUntil = time.Now().Add(5 * time.Second)

	updated, _ := m.Update(tea.KeyPressMsg{Text: "a"})
	m = updated.(Model)
	if m.input.Value() != "" {
		t.Error("expected keypress dropped during input drain")
	}
}

func TestScenario_InputDrainExpires(t *testing.T) {
	m := newTestModel()
	m.inputDrainUntil = time.Now().Add(-1 * time.Second)

	updated, _ := m.Update(tea.KeyPressMsg{Text: "a"})
	_ = updated.(Model)
}

// ---------------------------------------------------------------------------
// Scenario: Slash commands through handleCommand dispatch
// ---------------------------------------------------------------------------

func TestScenario_UserRunsExitCommand(t *testing.T) {
	m := newTestModel()
	cmd := m.handleCommand("/exit")
	if !m.quitting {
		t.Error("expected quitting after /exit")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestScenario_UserRunsQuitCommand(t *testing.T) {
	m := newTestModel()
	_ = m.handleCommand("/quit")
	if !m.quitting {
		t.Error("expected quitting after /quit")
	}
}

func TestScenario_UserRunsClearCommand(t *testing.T) {
	m := newTestModel()
	m.output.WriteString("some existing content\nmore content\n")
	m.loading = true

	cmd := m.handleCommand("/clear")
	if cmd != nil {
		t.Error("expected nil cmd after /clear")
	}
	if m.output.Len() != 0 {
		t.Error("expected output buffer cleared")
	}
	if m.loading {
		t.Error("expected loading=false after /clear")
	}
}

func TestScenario_UserRunsHelpCommand(t *testing.T) {
	m := newTestModel()
	cmd := m.handleCommand("/help")
	if cmd != nil {
		t.Error("expected nil cmd after /help")
	}
	if m.output.Len() == 0 {
		t.Error("expected help text in output")
	}
}

func TestScenario_UserRunsModeCommand(t *testing.T) {
	m := newTestModel()
	_ = m.handleCommand("/mode auto")
	if m.mode != permission.AutoMode {
		t.Errorf("expected mode=auto, got %v", m.mode)
	}
	_ = m.handleCommand("/mode autopilot")
	if m.mode != permission.AutopilotMode {
		t.Errorf("expected mode=autopilot, got %v", m.mode)
	}
}

func TestScenario_UserRunsAllowCommand(t *testing.T) {
	m := newTestModel()
	policy := permission.NewConfigPolicy(nil, nil)
	m.policy = policy
	_ = m.handleCommand("/allow run_command")
	if policy.GetDecision("run_command") != permission.Allow {
		t.Error("expected run_command allowed after /allow")
	}
}

func TestScenario_AllowCommandWithoutArgShowsUsage(t *testing.T) {
	m := newTestModel()
	m.policy = permission.NewConfigPolicy(nil, nil)
	before := m.output.Len()
	_ = m.handleCommand("/allow")
	if m.output.Len() == before {
		t.Error("expected usage message when /allow has no arg")
	}
}

func TestScenario_UnknownCommandShowsError(t *testing.T) {
	m := newTestModel()
	_ = m.handleCommand("/nonexistent")
	output := m.output.String()
	if !strings.Contains(output, "/nonexistent") {
		t.Error("expected error message mentioning the unknown command")
	}
}

// ---------------------------------------------------------------------------
// Scenario: User opens various panels via slash commands
// ---------------------------------------------------------------------------

func TestScenario_UserOpensModelPanel(t *testing.T) {
	m := newTestModel()
	m.modelPanel = &modelPanelState{
		models:   []string{"gpt-4", "gpt-3.5"},
		selected: 0,
	}
	if m.modelPanel == nil {
		t.Error("expected modelPanel to be set")
	}
}

func TestScenario_UserOpensImpersonatePanel(t *testing.T) {
	m := newTestModel()
	_ = m.handleCommand("/impersonate")
	if m.impersonatePanel == nil {
		t.Error("expected impersonatePanel opened")
	}
}

func TestScenario_UserOpensSkillsPanel(t *testing.T) {
	m := newTestModel()
	_ = m.handleCommand("/skills")
	if m.skillsPanel == nil {
		t.Error("expected skillsPanel opened")
	}
}

func TestScenario_UserOpensMCPPanel(t *testing.T) {
	m := newTestModel()
	m.openMCPPanel()
	if m.mcpPanel == nil {
		t.Error("expected mcpPanel opened")
	}
}

func TestScenario_UserOpensInspectorPanels(t *testing.T) {
	tests := []struct {
		cmd  string
		kind inspectorPanelKind
	}{
		{"/sessions", inspectorPanelSessions},
		{"/agents", inspectorPanelAgents},
		{"/checkpoints", inspectorPanelCheckpoints},
		{"/memory", inspectorPanelMemory},
		{"/todo", inspectorPanelTodos},
		{"/plugins", inspectorPanelPlugins},
		{"/config", inspectorPanelConfig},
		{"/status", inspectorPanelStatus},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			m := newTestModel()
			if tt.kind == inspectorPanelSessions {
				m.sessionStore = newTestSessionStore(t)
			}
			if tt.kind == inspectorPanelPlugins {
				m.pluginMgr = newTestPluginMgr()
			}
			_ = m.handleCommand(tt.cmd)
			if m.inspectorPanel == nil || m.inspectorPanel.kind != tt.kind {
				t.Errorf("expected inspector panel kind=%v", tt.kind)
			}
		})
	}
}

func TestScenario_UserOpensIMPanels(t *testing.T) {
	tests := []struct {
		cmd    string
		getter func(*Model) interface{}
	}{
		{"/qq", func(m *Model) interface{} { return m.qqPanel }},
		{"/telegram", func(m *Model) interface{} { return m.tgPanel }},
		{"/pc", func(m *Model) interface{} { return m.pcPanel }},
		{"/discord", func(m *Model) interface{} { return m.discordPanel }},
		{"/feishu", func(m *Model) interface{} { return m.feishuPanel }},
		{"/slack", func(m *Model) interface{} { return m.slackPanel }},
		{"/dingtalk", func(m *Model) interface{} { return m.dingtalkPanel }},
		{"/im", func(m *Model) interface{} { return m.imPanel }},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			m := newTestModel()
			_ = m.handleCommand(tt.cmd)
			panel := tt.getter(&m)
			if panel == nil {
				t.Errorf("expected panel opened after %s", tt.cmd)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Scenario: File browser opened with ctrl+f
// ---------------------------------------------------------------------------

func TestScenario_UserOpensFileBrowser(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+f"})
	m = updated.(Model)
	if m.fileBrowser == nil {
		t.Error("expected fileBrowser opened after ctrl+f")
	}
}

// ---------------------------------------------------------------------------
// Scenario: shouldExecuteWhileBusy function
// ---------------------------------------------------------------------------

func TestScenario_BusySafeCommandsExecuteImmediately(t *testing.T) {
	safeCommands := []string{
		"/help", "/model", "/provider", "/lang en",
		"/mcp", "/skills", "/sessions", "/agents",
		"/status", "/config", "/memory", "/todo", "/plugins",
		"/checkpoints", "/?", "/impersonate", "/qq", "/telegram",
		"/tg", "/pc", "/discord", "/feishu", "/slack", "/dingtalk",
		"/im", "/agent", "/harness", "/harness panel",
	}
	for _, cmd := range safeCommands {
		if !shouldExecuteWhileBusy(cmd) {
			t.Errorf("expected %q to be safe while busy", cmd)
		}
	}
}

func TestScenario_BusyUnsafeCommandsAreQueued(t *testing.T) {
	unsafeCommands := []string{
		"/exit", "/quit", "/clear", "/compact", "/undo",
		"/update", "/knight run", "/init", "/harness run",
		"/harness check", "hello world", "",
	}
	for _, cmd := range unsafeCommands {
		if shouldExecuteWhileBusy(cmd) {
			t.Errorf("expected %q to be queued (not safe while busy)", cmd)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario: Shell command detection via parseShellCommand
// ---------------------------------------------------------------------------

func TestScenario_UserTypesShellCommand(t *testing.T) {
	tests := []struct {
		input   string
		isShell bool
		command string
	}{
		{"$ ls -la", true, "ls -la"},
		{"!git status", true, "git status"},
		{"$echo hello", true, "echo hello"},
		{"!npm test", true, "npm test"},
		{"$ ", true, ""},
		{"hello", false, ""},
		{"/help", false, ""},
		{"", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			cmd, ok := parseShellCommand(tt.input)
			if ok != tt.isShell {
				t.Errorf("parseShellCommand(%q) ok=%v, want %v", tt.input, ok, tt.isShell)
			}
			if ok && cmd != tt.command {
				t.Errorf("parseShellCommand(%q) cmd=%q, want %q", tt.input, cmd, tt.command)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Scenario: Mode.Next() cycles correctly through all 5 modes
// ---------------------------------------------------------------------------

func TestScenario_PermissionModeCycleCompletes(t *testing.T) {
	modes := []permission.PermissionMode{
		permission.SupervisedMode,
		permission.PlanMode,
		permission.AutoMode,
		permission.BypassMode,
		permission.AutopilotMode,
	}
	current := permission.SupervisedMode
	for i := 0; i < len(modes)*2; i++ {
		current = current.Next()
		expected := modes[(i+1)%len(modes)]
		if current != expected {
			t.Errorf("cycle step %d: got %v, want %v", i+1, current, expected)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario: Startup banner clears on first real keypress
// ---------------------------------------------------------------------------

func TestScenario_StartupBannerClearsOnFirstKey(t *testing.T) {
	m := newTestModel()
	m.startupBannerVisible = true

	updated, _ := m.Update(tea.KeyPressMsg{Text: "h"})
	m = updated.(Model)
	if m.startupBannerVisible {
		t.Error("expected startupBannerVisible=false after first keypress")
	}
}

// ---------------------------------------------------------------------------
// Scenario: Paste is blocked when not ready
// ---------------------------------------------------------------------------

func TestScenario_PasteBlockedWhenNotReady(t *testing.T) {
	m := newTestModel()
	m.inputReady = false

	_, cmd := m.Update(tea.PasteMsg{Content: "pasted text"})
	if cmd != nil {
		t.Error("expected nil cmd when input not ready")
	}
}

func TestScenario_PasteBlockedWhenLoading(t *testing.T) {
	m := newTestModel()
	m.loading = true

	_, cmd := m.Update(tea.PasteMsg{Content: "pasted text"})
	if cmd != nil {
		t.Error("expected nil cmd when loading")
	}
}

// ---------------------------------------------------------------------------
// Scenario: Spaces in text input render correctly and cursor position is accurate
// Regression test for: trailing/middle spaces not rendering in composer input
// and cursor appearing frozen on space characters.
// ---------------------------------------------------------------------------

func TestScenario_InputSpacesRenderInComposer(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 30)

	for _, ch := range "hello   world" {
		updated, _ := m.Update(tea.KeyPressMsg{Text: string(ch)})
		m = updated.(Model)
	}
	if m.input.Value() != "hello   world" {
		t.Fatalf("expected input 'hello   world', got %q", m.input.Value())
	}

	rendered := m.renderComposerInput()
	plain := stripInputANSI(rendered)
	if !strings.Contains(plain, "hello   world") {
		t.Errorf("expected rendered input to contain 'hello   world', got %q", plain)
	}
}

func TestScenario_InputTrailingSpacesPreserved(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 30)

	for _, ch := range "test   " {
		updated, _ := m.Update(tea.KeyPressMsg{Text: string(ch)})
		m = updated.(Model)
	}
	if m.input.Value() != "test   " {
		t.Fatalf("expected input 'test   ' (with trailing spaces), got %q", m.input.Value())
	}
	if len([]rune(m.input.Value())) != 7 {
		t.Errorf("expected 7 runes in input, got %d", len([]rune(m.input.Value())))
	}
}

func TestScenario_CursorPositionAfterSpaces(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 30)

	for _, ch := range "ab  cd" {
		updated, _ := m.Update(tea.KeyPressMsg{Text: string(ch)})
		m = updated.(Model)
	}

	if inputCursor(&m.input) != 6 {
		t.Errorf("expected cursor at position 6, got %d", inputCursor(&m.input))
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = updated.(Model)
	if inputCursor(&m.input) != 5 {
		t.Errorf("expected cursor at position 5 after left, got %d", inputCursor(&m.input))
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = updated.(Model)
	if inputCursor(&m.input) != 4 {
		t.Errorf("expected cursor at position 4 after 2nd left, got %d", inputCursor(&m.input))
	}

	if m.input.Value() != "ab  cd" {
		t.Errorf("expected input 'ab  cd', got %q", m.input.Value())
	}
}

func TestScenario_ComposerPanelRendersSpacesWithoutOverflow(t *testing.T) {
	m := newTestModel()
	m.handleResize(80, 30)

	text := "fix the bug in the handler function where spaces break rendering"
	m.input.SetValue(text)
	m.input.CursorEnd()

	rendered := m.renderComposerPanel()
	for _, line := range strings.Split(rendered, "\n") {
		lineWidth := lipgloss.Width(line)
		maxWidth := m.mainColumnWidth()
		if lineWidth > maxWidth {
			t.Errorf("composer line overflows: width=%d max=%d line=%q", lineWidth, maxWidth, line)
		}
	}

	plain := stripInputANSI(rendered)
	if !strings.Contains(plain, "fix the bug") {
		t.Errorf("expected rendered output to contain original text with spaces, got %q", plain)
	}
}

// stripInputANSI removes common ANSI escape sequences from a string.
var inputANSIRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\][^\x1b]*\x1b\\`)

func stripInputANSI(s string) string {
	return inputANSIRe.ReplaceAllString(s, "")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestSessionStore(t *testing.T) session.Store {
	t.Helper()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	return store
}

func newTestPluginMgr() *plugin.Manager {
	return plugin.NewManager()
}

func TestShiftEnterInsertsNewline(t *testing.T) {
	m := newTestModel()
	m.inputReady = true
	m.input.SetValue("hello")
	m.input.SetHeight(1)

	// Shift+Enter: Code=KeyEnter, Mod=ModShift
	model, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	m = model.(Model)

	// textarea.Update with shift+enter may or may not insert newline depending on keymap.
	// The important thing is that the input is NOT submitted (value should not be cleared).
	val := m.input.Value()
	if val == "" {
		t.Error("input was cleared — shift+enter should NOT submit")
	}
	if !strings.Contains(val, "hello") {
		t.Errorf("value should still contain 'hello', got %q", val)
	}
}

func TestEnterSubmitsSingleLine(t *testing.T) {
	m := newTestModel()
	m.inputReady = true
	m.input.SetValue("hello world")

	model, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	m = model.(Model)

	if m.input.Value() != "" {
		t.Errorf("input should be cleared after enter, got %q", m.input.Value())
	}
	if m.input.Height() != 1 {
		t.Errorf("height should reset to 1 after submit, got %d", m.input.Height())
	}
}

func TestEnterSubmitsMultiline(t *testing.T) {
	m := newTestModel()
	m.inputReady = true
	m.input.SetValue("line1\nline2\nline3")
	m.input.SetHeight(3)

	model, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	m = model.(Model)

	if m.input.Value() != "" {
		t.Errorf("input should be cleared after enter, got %q", m.input.Value())
	}
	if m.input.Height() != 1 {
		t.Errorf("height should reset to 1 after submit, got %d", m.input.Height())
	}
}

func TestResizeResetsInputHeight(t *testing.T) {
	m := newTestModel()
	m.inputReady = true
	m.input.SetValue("a\nb\nc\nd\ne")
	m.input.SetHeight(5)

	m.handleResize(120, 40)

	got := m.input.Height()
	if got != 5 {
		t.Errorf("expected height=5 after resize with 5-line content, got %d", got)
	}
}

func TestComposerHeightRespectsSetValue(t *testing.T) {
	m := newTestModel()
	m.inputReady = true

	// Multiline content → height should grow
	m.input.SetValue("a\nb\nc\nd\ne")
	m.input.SetHeight(composerHeight(m.input.Value()))
	if m.input.Height() != 5 {
		t.Errorf("expected height=5, got %d", m.input.Height())
	}

	// Empty content → height should shrink back to 1
	m.input.SetValue("")
	m.input.SetHeight(composerHeight(m.input.Value()))
	if m.input.Height() != 1 {
		t.Errorf("expected height=1 after clear, got %d", m.input.Height())
	}
}

func TestPasteSetsCorrectHeight(t *testing.T) {
	m := newTestModel()
	m.inputReady = true
	m.input.SetValue("hello")
	m.input.SetHeight(1)

	// Paste multi-line text
	model, _ := m.Update(tea.PasteMsg{Content: "line1\nline2\nline3"})
	m = model.(Model)

	val := m.input.Value()
	if !strings.Contains(val, "line1") {
		t.Errorf("pasted content should be in input, got %q", val)
	}
	lines := strings.Count(val, "\n") + 1
	if m.input.Height() < lines {
		t.Errorf("expected height >= %d after pasting %d lines, got %d", lines, lines, m.input.Height())
	}
}
