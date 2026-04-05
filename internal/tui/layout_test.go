package tui

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/subagent"
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
	if !strings.Contains(view, "vendor") || !strings.Contains(view, "session") {
		t.Error("expected sidebar metadata in wide layout")
	}
	if !strings.Contains(view, "____ ____ ____") {
		t.Error("expected ascii logo in right sidebar")
	}
	if !strings.Contains(view, "Mode policy") || !strings.Contains(view, "approval") {
		t.Error("expected mode policy section in sidebar")
	}
}

func TestSidebarModePolicyLocalizesInChinese(t *testing.T) {
	m := newTestModel()
	m.setLanguage("zh-CN")
	m.handleResize(128, 28)

	view := m.View()
	if !strings.Contains(view, "模式说明") || !strings.Contains(view, "审批") || !strings.Contains(view, "行为") {
		t.Error("expected localized mode policy section in sidebar")
	}
}

func TestProviderPanelRendersInContextPanel(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	m.SetConfig(cfg)
	m.openProviderPanel()

	result := m.renderContextPanel()

	if !strings.Contains(result, "/provider") {
		t.Fatal("expected provider panel title")
	}
	if !strings.Contains(result, "Vendors") {
		t.Fatal("expected vendor list in provider panel")
	}
	if !strings.Contains(result, "Endpoints") {
		t.Fatal("expected endpoint list in provider panel")
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
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "Thinking...") {
		t.Error("expected activity in status bar")
	}
	if strings.Contains(bar, "tokens") || strings.Contains(bar, "$") {
		t.Error("expected token and cost info to be hidden from status bar")
	}
}

func TestStatusBarWithToolInfo(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Reading README.md"
	m.statusToolName = "Read"
	m.statusToolArg = "README.md"
	m.statusToolCount = 3
	bar := m.renderStatusBar()
	if strings.Contains(bar, "read_file") {
		t.Error("expected raw tool name to be hidden in status bar")
	}
	if !strings.Contains(bar, "Read") || !strings.Contains(bar, "README.md") {
		t.Error("expected friendly tool label in status bar")
	}
	if !strings.Contains(bar, "3") {
		t.Error("expected tool count in status bar")
	}
}

func TestRenderOutputShowsGroupedToolActivity(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true
	m.startToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: true})
	m.finishToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: false, Result: "line1\nline2"})
	m.startToolActivity(ToolStatusMsg{ToolName: "search_files", DisplayName: "Search", Detail: "ContextManager", Running: true})
	m.finishToolActivity(ToolStatusMsg{ToolName: "search_files", DisplayName: "Search", Detail: "ContextManager", Running: false, Result: "Found 4 matches"})
	m.closeToolActivityGroup()

	output := m.renderOutput()

	if !strings.Contains(output, "Exploring project context") {
		t.Fatal("expected grouped title in main content")
	}
	if !strings.Contains(output, "Read README.md — 2 lines of content") {
		t.Fatal("expected single-line read summary in grouped activity")
	}
	if !strings.Contains(output, "Search ContextManager — 4 matches") {
		t.Fatal("expected single-line search summary in grouped activity")
	}
}

func TestTodoWriteOrganizesFollowingActivityUnderActiveTodo(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true
	raw := `{"todos":[{"id":"todo-1","content":"Polish TUI activity flow","status":"in_progress"},{"id":"todo-2","content":"Refresh docs","status":"pending"}]}`

	m.startToolActivity(ToolStatusMsg{ToolName: "todo_write", DisplayName: "Update todos", Running: true, RawArgs: raw})
	m.finishToolActivity(ToolStatusMsg{ToolName: "todo_write", DisplayName: "Update todos", Running: false, RawArgs: raw})
	m.startToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "internal/tui/view.go", Running: true})
	m.finishToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "internal/tui/view.go", Running: false, Result: "line1\nline2"})

	output := m.renderOutput()

	if !strings.Contains(output, "🎯 Todo: Polish TUI activity flow") {
		t.Fatalf("expected active todo heading in grouped activity, got %q", output)
	}
	if !strings.Contains(output, "📦 Advancing tasks") {
		t.Fatalf("expected todo update group, got %q", output)
	}
	if !strings.Contains(output, "Started Polish TUI activity flow") {
		t.Fatalf("expected todo diff summary, got %q", output)
	}
	if !strings.Contains(output, "📦 Exploring project context") {
		t.Fatalf("expected following tool work to render as its own group, got %q", output)
	}
}

func TestRenderOutputCapsGroupedActivityToLatestFiveItems(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true
	for i := 1; i <= 7; i++ {
		name := fmt.Sprintf("step-%d.md", i)
		m.startToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: name, Running: true})
		m.finishToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: name, Running: false, Result: "line1\nline2"})
	}
	m.closeToolActivityGroup()

	output := m.renderOutput()

	if !strings.Contains(output, "… 2 earlier completed steps") {
		t.Fatal("expected folded count for hidden group items")
	}
	if strings.Contains(output, "step-1.md") || strings.Contains(output, "step-2.md") {
		t.Fatal("expected older group items to be hidden")
	}
	for i := 3; i <= 7; i++ {
		if !strings.Contains(output, fmt.Sprintf("step-%d.md", i)) {
			t.Fatalf("expected latest item step-%d.md to remain visible", i)
		}
	}
}

func TestRenderOutputShowsSubAgentAsIndependentState(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	id := m.subAgentMgr.Spawn("context\n\nInvestigate parser behavior", "Investigate parser behavior", nil, context.Background())
	sa, ok := m.subAgentMgr.Get(id)
	if !ok {
		t.Fatal("expected spawned subagent")
	}
	sa.Status = subagent.StatusRunning
	sa.CurrentPhase = "tool"
	sa.CurrentTool = "read_file"
	sa.CurrentArgs = `{"path":"docs/spec.md"}`
	sa.ToolCallCount = 2

	output := m.renderOutput()

	if !strings.Contains(output, "🤖") || !strings.Contains(output, "Investigate parser behavior") {
		t.Fatalf("expected independent subagent state block, got %q", output)
	}
	if !strings.Contains(output, "Reading docs/spec.md") {
		t.Fatalf("expected friendly subagent activity summary, got %q", output)
	}
	if strings.Contains(output, "spawn_agent") || strings.Contains(output, "wait_agent") || strings.Contains(output, id) {
		t.Fatalf("expected subagent lifecycle internals to stay hidden, got %q", output)
	}
}

func TestStatusBarStaysCompactWithoutGroupedActivities(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true
	m.startToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: true})
	m.finishToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: false, Result: "line1\nline2"})
	m.closeToolActivityGroup()

	bar := m.renderStatusBar()

	if strings.Contains(bar, "Exploring project context") || strings.Contains(bar, "Read README.md") {
		t.Fatal("expected grouped activities to stay out of the agent status panel")
	}
}

func TestStatusBarShowsActiveTodo(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeTodo = &todoStateItem{ID: "todo-1", Content: "Polish TUI activity flow", Status: "in_progress"}

	bar := m.renderStatusBar()

	if !strings.Contains(bar, "Working on Polish TUI activity flow") {
		t.Fatalf("expected active todo in status bar, got %q", bar)
	}
	if !strings.Contains(bar, "🎯") {
		t.Fatalf("expected todo marker in status bar, got %q", bar)
	}
}

func TestRenderOutputDoesNotDuplicateLegacyToolLog(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true

	m.startToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: true})
	m.finishToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: false, Result: "line1\nline2"})

	output := m.renderOutput()

	if strings.Count(output, "Read README.md") != 1 {
		t.Fatalf("expected grouped activity to render once, got %q", output)
	}
	if strings.Contains(output, "└") || strings.Contains(output, "● Read README.md") {
		t.Fatal("expected legacy tool log chrome to be removed")
	}
}

func TestDoneMsgPersistsGroupedActivitiesInOutput(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true
	m.startToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: true})
	m.finishToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: false, Result: "line1\nline2"})

	next, _ := m.Update(doneMsg{})
	m = next.(Model)

	output := m.renderOutput()
	if !strings.Contains(output, "Exploring project context") || !strings.Contains(output, "Read README.md — 2 lines of content") {
		t.Fatalf("expected grouped activities to persist in main output after completion, got %q", output)
	}
}

func TestTrimLeadingRenderedSpacing(t *testing.T) {
	trimmed := trimLeadingRenderedSpacing("\n\n  Hello\n")
	if trimmed != "Hello\n" {
		t.Fatalf("expected leading rendered spacing to be trimmed, got %q", trimmed)
	}
}

func TestRenderOutputDecoratesStreamingBullet(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.streamPrefixWritten = true
	m.streamStartPos = 0
	m.output.WriteString(bulletStyle.Render("● ") + "streaming")
	m.spinner.Start("Writing response")
	m.spinner.frame = 2

	output := m.renderOutput()

	if !strings.Contains(output, "○ ") {
		t.Fatalf("expected breathing bullet frame in output, got %q", output)
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
	m.mode = permission.AutopilotMode
	autopilot := m.renderModeBadge()

	if supervised == plan || plan == auto || auto == bypass || bypass == autopilot {
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

func TestAutopilotApprovalAutoContinues(t *testing.T) {
	m := newTestModel()
	m.mode = permission.AutopilotMode
	msg := ApprovalMsg{ToolName: "bash", Input: `{"command":"sudo rm -rf /"}`, Response: make(chan permission.Decision, 1)}

	next, cmd := m.Update(msg)
	m = next.(Model)
	if cmd != nil {
		t.Fatal("expected autopilot approval to resolve synchronously")
	}
	select {
	case decision := <-msg.Response:
		if decision != permission.Allow {
			t.Fatalf("expected autopilot to allow approval, got %v", decision)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected autopilot to resolve approval immediately")
	}
	if m.pendingApproval != nil {
		t.Fatal("expected pending approval to be cleared")
	}
}

func TestAutopilotDiffConfirmAutoContinues(t *testing.T) {
	m := newTestModel()
	m.mode = permission.AutopilotMode
	msg := DiffConfirmMsg{FilePath: "main.go", DiffText: "@@ -1 +1 @@\n-a\n+b", Response: make(chan bool, 1)}

	next, cmd := m.Update(msg)
	m = next.(Model)
	if cmd != nil {
		t.Fatal("expected autopilot diff confirm to resolve synchronously")
	}
	select {
	case approved := <-msg.Response:
		if !approved {
			t.Fatal("expected autopilot to approve diff confirm")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected autopilot to resolve diff confirm immediately")
	}
	if m.pendingDiffConfirm != nil {
		t.Fatal("expected pending diff confirm to be cleared")
	}
}

func TestApprovalRenderedInContextPanel(t *testing.T) {
	m := newTestModel()
	m.pendingApproval = &ApprovalMsg{ToolName: "bash", Input: `{"command":"ls -la docs"}`}
	m.approvalOptions = defaultApprovalOptions()
	result := m.renderContextPanel()
	if !strings.Contains(result, "Approval required") {
		t.Error("expected approval panel title")
	}
	if strings.Contains(result, "tool   bash") {
		t.Error("expected friendly tool name in approval panel")
	}
	if !strings.Contains(result, "Run ls -la docs") {
		t.Error("expected friendly tool label in approval panel")
	}
}

func TestApprovalInputPreviewUsesBasename(t *testing.T) {
	m := newTestModel()
	m.pendingApproval = &ApprovalMsg{ToolName: "read_file", Input: `{"path":"/tmp/project/docs/spec.md"}`}
	m.approvalOptions = defaultApprovalOptions()

	result := m.renderContextPanel()

	if strings.Contains(result, "/tmp/project/docs/spec.md") {
		t.Error("expected approval preview to hide full path")
	}
	if !strings.Contains(result, `"path":"spec.md"`) {
		t.Error("expected approval preview to show basename only")
	}
}

func TestDiffConfirmUsesBasename(t *testing.T) {
	m := newTestModel()
	m.pendingDiffConfirm = &DiffConfirmMsg{FilePath: "/tmp/project/internal/context/manager.go", DiffText: "@@ -1 +1 @@\n-a\n+b"}
	m.diffOptions = diffConfirmOptions()

	result := m.renderContextPanel()

	if strings.Contains(result, "/tmp/project/internal/context/manager.go") {
		t.Error("expected diff confirm panel to hide full path")
	}
	if !strings.Contains(result, "manager.go") {
		t.Error("expected diff confirm panel to show basename")
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
	m.statusToolCount = 2
	m.activityGroups = []toolActivityGroup{{Title: "Exploring project context", Active: true, Items: []toolActivityItem{{Summary: "Read README.md"}}}}
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
	if m.statusActivity != "" || m.statusToolName != "" || m.statusToolCount != 0 {
		t.Error("expected status state to be reset")
	}
	if len(m.activityGroups) != 0 {
		t.Error("expected grouped activity state to be reset")
	}
	if m.spinner.IsActive() {
		t.Error("expected spinner to stop")
	}
}
