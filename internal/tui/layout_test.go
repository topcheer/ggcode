package tui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/session"
)

func newTestModel() Model {
	m := NewModel(nil, permission.NewConfigPolicy(nil, nil))
	return m
}

// --- Viewport / Resize tests ---

func TestResizeUpdatesViewport(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	if m.viewport.height != conversationInnerHeight(m.conversationPanelHeight()) {
		t.Errorf("expected synced viewport height %d, got %d", conversationInnerHeight(m.conversationPanelHeight()), m.viewport.height)
	}
	if m.viewport.width != m.conversationInnerWidth() {
		t.Errorf("expected synced viewport width %d, got %d", m.conversationInnerWidth(), m.viewport.width)
	}
	if m.input.Width != m.mainColumnWidth()-6 {
		t.Errorf("expected input width %d, got %d", m.mainColumnWidth()-6, m.input.Width)
	}
}

func TestResizeSmallWindow(t *testing.T) {
	m := newTestModel()
	m.handleResize(40, 5)
	if m.viewport.height != conversationInnerHeight(m.conversationPanelHeight()) {
		t.Errorf("expected synced viewport height %d, got %d", conversationInnerHeight(m.conversationPanelHeight()), m.viewport.height)
	}
}

func TestResizeTinyWindow(t *testing.T) {
	m := newTestModel()
	m.handleResize(10, 2)
	if m.viewport.height != conversationInnerHeight(m.conversationPanelHeight()) {
		t.Errorf("expected synced viewport height %d, got %d", conversationInnerHeight(m.conversationPanelHeight()), m.viewport.height)
	}
}

// --- Viewport scroll / auto-follow tests ---

func TestViewportAutoFollow(t *testing.T) {
	vp := NewViewportModel(80, 20)
	if !vp.AutoFollow() {
		t.Error("expected autoFollow to be true initially")
	}
}

func TestViewportSetSize(t *testing.T) {
	vp := NewViewportModel(80, 20)
	vp.SetSize(120, 30)
	if vp.width != 120 || vp.height != 30 {
		t.Errorf("expected 120x30, got %dx%d", vp.width, vp.height)
	}
}

func TestViewportScrollUp(t *testing.T) {
	vp := NewViewportModel(80, 20)
	// Set multi-line content
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "line content here"
	}
	vp.SetContent(strings.Join(lines, "\n"))
	vp.ScrollUp(5)
	// After scrolling up, auto-follow should be disabled
	if vp.AutoFollow() {
		t.Error("expected autoFollow to be false after manual scroll up")
	}
}

func TestViewportScrollDown(t *testing.T) {
	vp := NewViewportModel(80, 20)
	vp.ScrollUp(10)
	vp.ScrollDown(5)
	// ScrollDown with remaining offset may or may not re-enable auto-follow
	// depending on whether it reached the bottom; just verify no panic
}

func TestViewportGotoBottom(t *testing.T) {
	vp := NewViewportModel(80, 20)
	vp.ScrollUp(10)
	if vp.AutoFollow() {
		t.Error("expected autoFollow false after scroll up")
	}
	vp.GotoBottom()
	if !vp.AutoFollow() {
		t.Error("expected autoFollow true after GotoBottom")
	}
}

func TestViewportLongContentScroll(t *testing.T) {
	vp := NewViewportModel(80, 5)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "content line"
	}
	vp.SetContent(strings.Join(lines, "\n"))
	// Should auto-follow to bottom
	if !vp.AutoFollow() {
		t.Error("expected autoFollow after SetContent with long content")
	}
}

// --- Empty content layout ---

func TestEmptyContentInputAtBottom(t *testing.T) {
	m := newTestModel()
	m.handleResize(80, 24)
	view := m.View()
	// The input prompt "❯ " should appear in the view even with empty content
	if !strings.Contains(view, "❯") {
		t.Error("expected input prompt in view")
	}
}

func TestViewContainsInputPlaceholder(t *testing.T) {
	m := newTestModel()
	m.handleResize(80, 24)
	view := m.View()
	if !strings.Contains(view, "Type a message") {
		t.Error("expected input placeholder in view")
	}
}

func TestLangCommandSwitchesToChinese(t *testing.T) {
	m := newTestModel()

	cmd := m.handleLangCommand([]string{"/lang", "zh-CN"})
	if cmd != nil {
		t.Fatal("expected /lang to update synchronously")
	}
	if m.currentLanguage() != LangZhCN {
		t.Fatalf("expected zh-CN, got %s", m.currentLanguage())
	}
	if m.input.Placeholder != "输入消息..." {
		t.Fatalf("expected localized placeholder, got %q", m.input.Placeholder)
	}
	if !strings.Contains(m.output.String(), "已切换语言为") {
		t.Fatal("expected language switch output")
	}
}

func TestChineseViewRendersLocalizedPanels(t *testing.T) {
	m := newTestModel()
	m.setLanguage("zh-CN")
	m.handleResize(100, 28)

	view := m.View()
	if !strings.Contains(view, "对话") {
		t.Error("expected localized conversation panel")
	}
	if !strings.Contains(view, "输入") {
		t.Error("expected localized composer panel")
	}
	if !strings.Contains(view, "输入消息") {
		t.Error("expected localized placeholder")
	}
}

func TestViewContainsPanels(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 28)
	view := m.View()
	if !strings.Contains(view, "Conversation") {
		t.Error("expected conversation panel title in view")
	}
	if !strings.Contains(view, "Composer") {
		t.Error("expected composer panel title in view")
	}
	if !strings.Contains(view, "ggcode") {
		t.Error("expected branded header in view")
	}
}

func TestWideLayoutUsesRightSidebar(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)

	if !m.sidebarEnabled() {
		t.Fatal("expected sidebar layout to be enabled")
	}
	if m.mainColumnWidth() >= m.viewWidth() {
		t.Fatal("expected sidebar to reduce main column width")
	}

	view := m.View()
	if !strings.Contains(view, "provider") || !strings.Contains(view, "session") {
		t.Error("expected sidebar metadata in wide layout")
	}
	if !strings.Contains(view, "____ ____ ____") {
		t.Error("expected ascii logo in right sidebar")
	}
}

func TestNarrowLayoutFallsBackToTopHeader(t *testing.T) {
	m := newTestModel()
	m.handleResize(90, 28)

	if m.sidebarEnabled() {
		t.Fatal("expected narrow layout to disable sidebar")
	}

	view := m.View()
	if !strings.Contains(view, "terminal-native AI coding") {
		t.Error("expected classic header in narrow layout")
	}
}

func TestStreamingViewFollowsLatestOutput(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 28)
	m.loading = true

	for i := 0; i < 80; i++ {
		next, _ := m.Update(streamMsg(fmt.Sprintf("line %03d\n", i)))
		m = next.(Model)
	}

	view := m.View()
	if !strings.Contains(view, "line 079") {
		t.Error("expected latest streamed output to remain visible")
	}
}

func TestMouseEventsDoNotReachInput(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 28)
	m.input.SetValue("hello")
	m.input.CursorEnd()

	next, cmd := m.Update(tea.MouseMsg{
		X:      5,
		Y:      5,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	m = next.(Model)

	if cmd != nil {
		t.Error("expected mouse click to be handled without command")
	}
	if m.input.Value() != "hello" {
		t.Errorf("expected mouse event not to alter input, got %q", m.input.Value())
	}
}

// --- Status bar rendering ---

func TestStatusBarWithCostInfo(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Thinking..."
	m.statusTokens = 1500
	m.statusCost = 0.0042
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "Thinking...") {
		t.Error("expected activity in status bar")
	}
}

func TestStatusBarWithToolInfo(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Executing"
	m.statusToolName = "read_file"
	m.statusToolCount = 3
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "read_file") {
		t.Error("expected tool name in status bar")
	}
	if !strings.Contains(bar, "3") {
		t.Error("expected tool count in status bar")
	}
}

func TestStatusBarEmptyWhenNotLoading(t *testing.T) {
	m := newTestModel()
	m.loading = false
	bar := m.renderStatusBar()
	// Should return empty string when not loading
	if strings.TrimSpace(bar) != "" {
		t.Errorf("expected empty status bar when not loading, got: %q", bar)
	}
}

func TestModeBadgesUseDifferentRendering(t *testing.T) {
	m := newTestModel()

	m.mode = permission.SupervisedMode
	supervised := m.renderModeBadge()
	m.mode = permission.PlanMode
	plan := m.renderModeBadge()
	m.mode = permission.AutoMode
	auto := m.renderModeBadge()
	m.mode = permission.BypassMode
	bypass := m.renderModeBadge()

	if supervised == plan || plan == auto || auto == bypass {
		t.Error("expected different modes to render with different badges")
	}
}

// --- AutoComplete rendering ---

func TestAutoCompleteSlashCommands(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteKind = "slash"
	m.autoCompleteItems = []string{"/help", "/exit", "/clear"}
	m.autoCompleteIndex = 0
	result := m.renderAutoComplete()
	if !strings.Contains(result, "/help") {
		t.Error("expected /help in autocomplete")
	}
	if !strings.Contains(result, "/exit") {
		t.Error("expected /exit in autocomplete")
	}
	if !strings.Contains(result, "Commands:") {
		t.Error("expected commands header in autocomplete")
	}
}

func TestAutoCompleteEmpty(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = false
	result := m.renderAutoComplete()
	if result != "" {
		t.Errorf("expected empty autocomplete when inactive, got: %q", result)
	}
}

func TestAutoCompleteMentionHeader(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteKind = "mention"
	m.autoCompleteItems = []string{"internal/", "README.md"}
	result := m.renderAutoComplete()
	if !strings.Contains(result, "Files:") {
		t.Error("expected files header for mention autocomplete")
	}
	if !strings.Contains(result, "📁 internal/") {
		t.Error("expected directory icon in mention autocomplete")
	}
}

func TestSlashAutocompleteEnterExecutesCommand(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteKind = "slash"
	m.autoCompleteItems = []string{"/help", "/exit"}
	m.autoCompleteIndex = 0
	m.input.SetValue("/he")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if cmd != nil {
		t.Error("expected slash autocomplete execution to complete synchronously")
	}
	if m.input.Value() != "" {
		t.Error("expected input to be cleared after executing slash command")
	}
	if m.autoCompleteActive {
		t.Error("expected autocomplete to close after executing slash command")
	}
	if len(m.history) != 1 || m.history[0] != "/help" {
		t.Error("expected selected slash command to be added to history")
	}
	if !strings.Contains(m.output.String(), "Available commands:") {
		t.Error("expected selected slash command to execute immediately")
	}
}

func TestMentionAutocompleteEnterOnlyCompletesInput(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteKind = "mention"
	m.autoCompleteItems = []string{"README.md"}
	m.autoCompleteIndex = 0
	m.input.SetValue("@REA")
	m.input.CursorEnd()

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if cmd != nil {
		t.Error("expected mention autocomplete to only update input")
	}
	if m.input.Value() != "@README.md " {
		t.Errorf("expected mention completion in input, got %q", m.input.Value())
	}
	if m.output.Len() != 0 {
		t.Error("expected mention autocomplete not to execute a command")
	}
}

func TestModeSwitchDoesNotWriteToOutput(t *testing.T) {
	m := newTestModel()

	next, cmd := m.handleModeSwitch()
	m = next.(Model)

	if cmd != nil {
		t.Error("expected no command from mode switch")
	}
	if m.output.Len() != 0 {
		t.Errorf("expected mode switch not to write output, got %q", m.output.String())
	}
}

func TestModeCommandSetDoesNotWriteToOutput(t *testing.T) {
	m := newTestModel()

	cmd := m.handleModeCommand([]string{"/mode", "auto"})
	if cmd != nil {
		t.Error("expected no command from /mode set")
	}
	if m.output.Len() != 0 {
		t.Errorf("expected /mode set not to write output, got %q", m.output.String())
	}
}

func TestApprovalRenderedInContextPanel(t *testing.T) {
	m := newTestModel()
	m.pendingApproval = &ApprovalMsg{ToolName: "bash", Input: "ls"}
	m.approvalOptions = defaultApprovalOptions()
	result := m.renderContextPanel()
	if !strings.Contains(result, "Approval required") {
		t.Error("expected approval panel title")
	}
	if !strings.Contains(result, "tool   bash") {
		t.Error("expected tool name in approval panel")
	}
}

// --- Update message handling ---

func TestUpdateWindowSizeMsg(t *testing.T) {
	m := newTestModel()
	msg := tea.WindowSizeMsg{Width: 100, Height: 30}
	model, cmd := m.Update(msg)
	m2 := model.(Model)
	if m2.width != 100 {
		t.Errorf("expected width 100, got %d", m2.width)
	}
	if m2.height != 30 {
		t.Errorf("expected height 30, got %d", m2.height)
	}
	if cmd != nil {
		t.Error("expected nil cmd for WindowSizeMsg")
	}
}

func TestUpdateKeyMsgCtrlCRequestsExitConfirmation(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("draft text")
	msg := tea.KeyMsg{Type: tea.KeyCtrlC}
	model, cmd := m.Update(msg)
	m2 := model.(Model)
	if cmd != nil {
		t.Error("expected no quit command on first Ctrl-C")
	}
	if m2.quitting {
		t.Error("expected app to stay open on first Ctrl-C")
	}
	if m2.input.Value() != "" {
		t.Errorf("expected input to be cleared, got %q", m2.input.Value())
	}
	if !m2.exitConfirmPending {
		t.Error("expected exit confirmation to be armed")
	}
	if !strings.Contains(m2.output.String(), "Press Ctrl-C again to exit.") {
		t.Error("expected exit confirmation prompt in output")
	}
}

func TestUpdateKeyMsgCtrlCQuitsOnSecondPress(t *testing.T) {
	m := newTestModel()
	m.exitConfirmPending = true
	msg := tea.KeyMsg{Type: tea.KeyCtrlC}
	model, cmd := m.Update(msg)
	m2 := model.(Model)
	if cmd == nil {
		t.Error("expected quit command on second Ctrl-C")
	}
	if !m2.quitting {
		t.Error("expected app to quit on second Ctrl-C")
	}
}

func TestUpdateKeyMsgEnterEmpty(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("")
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	model, cmd := m.Update(msg)
	m2 := model.(Model)
	if m2.quitting {
		t.Error("should not quit on empty enter")
	}
	if cmd != nil {
		t.Error("expected nil cmd for empty enter")
	}
}

func TestResizeANSISequenceDoesNotReachInput(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 30)

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[16;40R")})
	m2 := model.(Model)

	if cmd != nil {
		t.Error("expected nil cmd for ignored ANSI fragment")
	}
	if m2.input.Value() != "" {
		t.Errorf("expected ANSI fragment to be ignored, got %q", m2.input.Value())
	}
}

func TestCtrlCCancelsAutocomplete(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteKind = "slash"
	m.autoCompleteItems = []string{"/help"}
	m.input.SetValue("/he")

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m2 := model.(Model)

	if cmd != nil {
		t.Error("expected nil cmd when cancelling autocomplete")
	}
	if m2.autoCompleteActive {
		t.Error("expected autocomplete to be cancelled")
	}
	if m2.exitConfirmPending {
		t.Error("expected Ctrl-C on autocomplete not to arm exit confirmation")
	}
}

func TestCtrlCLoadingCancelsCurrentActivity(t *testing.T) {
	m := newTestModel()
	cancelled := false
	m.loading = true
	m.cancelFunc = func() { cancelled = true }

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m2 := model.(Model)

	if cmd != nil {
		t.Error("expected nil cmd when interrupting active work")
	}
	if !cancelled {
		t.Error("expected cancel func to be called")
	}
	if !m2.loading {
		t.Error("expected model to stay busy until doneMsg arrives")
	}
	if m2.exitConfirmPending {
		t.Error("expected interrupt not to arm exit confirmation")
	}
	if !m2.runCanceled {
		t.Error("expected current run to be marked as canceled")
	}
	if !strings.Contains(m2.output.String(), "[interrupted]") {
		t.Error("expected interrupted marker in output")
	}
}

func TestLoadingAllowsTypingAndQueuesSubmission(t *testing.T) {
	m := newTestModel()
	m.loading = true

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = model.(Model)

	if m.input.Value() != "hi" {
		t.Fatalf("expected input to remain editable while loading, got %q", m.input.Value())
	}

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)

	if cmd != nil {
		t.Error("expected queued submission not to start a new agent immediately")
	}
	if len(m.pendingSubmissions) != 1 || m.pendingSubmissions[0] != "hi" {
		t.Fatalf("expected one queued submission, got %#v", m.pendingSubmissions)
	}
	if m.input.Value() != "" {
		t.Errorf("expected input to clear after queuing, got %q", m.input.Value())
	}
	if !strings.Contains(m.output.String(), "[queued 1 pending]") {
		t.Error("expected queued hint in output")
	}
}

func TestDoneMsgAutoSubmitsMergedPendingInput(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.pendingSubmissions = []string{"first question", "second question"}
	m.streamBuffer = &bytes.Buffer{}

	model, cmd := m.Update(doneMsg{})
	m = model.(Model)

	if cmd == nil {
		t.Fatal("expected doneMsg to schedule next merged submission")
	}
	if !m.loading {
		t.Error("expected next merged submission to start immediately")
	}
	if len(m.pendingSubmissions) != 0 {
		t.Errorf("expected pending submissions to be consumed, got %#v", m.pendingSubmissions)
	}
	if !strings.Contains(m.output.String(), "first question\n\nsecond question") {
		t.Error("expected merged queued text to be submitted as one user message")
	}
}

func TestCtrlCRestoresPendingMessagesToInput(t *testing.T) {
	m := newTestModel()
	cancelled := false
	m.loading = true
	m.cancelFunc = func() { cancelled = true }
	m.pendingSubmissions = []string{"first question", "second question"}
	m.input.SetValue("draft")

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = model.(Model)

	if cmd != nil {
		t.Error("expected no command when cancelling active run")
	}
	if !cancelled {
		t.Error("expected cancel func to run")
	}
	if got := m.input.Value(); got != "first question  second question  draft" {
		t.Fatalf("unexpected restored input: %q", got)
	}
	if len(m.pendingSubmissions) != 0 {
		t.Errorf("expected pending submissions to be cleared after restore, got %#v", m.pendingSubmissions)
	}
}

func TestExitConfirmationClearsOnOtherKey(t *testing.T) {
	m := newTestModel()
	m.exitConfirmPending = true

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m2 := model.(Model)

	if m2.exitConfirmPending {
		t.Error("expected exit confirmation to clear on other input")
	}
}

func TestResizeStillAllowsNormalRuneInput(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 30)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m2 := model.(Model)

	if m2.input.Value() != "a" {
		t.Errorf("expected normal input after resize, got %q", m2.input.Value())
	}
}

// --- renderOutput ---

func TestRenderOutputEmpty(t *testing.T) {
	m := newTestModel()
	result := m.renderOutput()
	if !strings.Contains(result, "Ask for a refactor") {
		t.Errorf("expected starter guidance, got: %q", result)
	}
}

func TestRenderOutputWithContent(t *testing.T) {
	m := newTestModel()
	m.output.WriteString("Hello world\n")
	result := m.renderOutput()
	if !strings.Contains(result, "Hello world") {
		t.Error("expected content in renderOutput")
	}
}

func TestResetConversationViewClearsTransientState(t *testing.T) {
	m := newTestModel()
	m.output.WriteString("Hello")
	m.loading = true
	m.streamBuffer = &bytes.Buffer{}
	m.streamBuffer.WriteString("partial")
	m.autoCompleteActive = true
	m.autoCompleteItems = []string{"/help"}
	m.statusActivity = "Thinking..."
	m.statusToolName = "read_file"
	m.statusTokens = 123
	m.statusCost = 1.2
	m.statusToolCount = 2
	m.spinner.Start("read_file")

	m.resetConversationView()

	if m.output.Len() != 0 {
		t.Error("expected output to be cleared")
	}
	if m.loading {
		t.Error("expected loading to be reset")
	}
	if m.autoCompleteActive || len(m.autoCompleteItems) != 0 {
		t.Error("expected autocomplete state to be cleared")
	}
	if m.statusActivity != "" || m.statusToolName != "" || m.statusTokens != 0 || m.statusCost != 0 || m.statusToolCount != 0 {
		t.Error("expected status state to be reset")
	}
	if m.spinner.IsActive() {
		t.Error("expected spinner to stop")
	}
}

func TestCostUpdateUsesRealSessionTracker(t *testing.T) {
	m := newTestModel()
	m.costMgr = cost.NewManager(cost.DefaultPricingTable(), "")
	m.SetSession(&session.Session{ID: "sess-1"}, nil)

	tracker := m.costMgr.GetOrCreateTracker("sess-1", "anthropic", "claude-sonnet-4-20250514")
	tracker.Record(cost.TokenUsage{InputTokens: 120, OutputTokens: 80})

	next, _ := m.Update(costUpdateMsg{InputTokens: 120, OutputTokens: 80})
	m = next.(Model)

	if m.statusTokens != 200 {
		t.Errorf("expected status tokens 200, got %d", m.statusTokens)
	}
	if !strings.Contains(m.lastCost, "tokens: 120 in / 80 out") {
		t.Errorf("expected latest usage summary, got %q", m.lastCost)
	}
}

func TestCostUpdateFallsBackToIncrementalTokens(t *testing.T) {
	m := newTestModel()
	m.costMgr = cost.NewManager(cost.DefaultPricingTable(), "")

	next, _ := m.Update(costUpdateMsg{InputTokens: 25, OutputTokens: 15})
	m = next.(Model)

	if m.statusTokens != 40 {
		t.Errorf("expected fallback token count 40, got %d", m.statusTokens)
	}
}
