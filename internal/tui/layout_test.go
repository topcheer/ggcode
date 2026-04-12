package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
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

func TestRebuildMarkdownRendererSkipsUnchangedWrapWidth(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 24)
	m.rebuildMarkdownRenderer()
	before := uintptr(unsafe.Pointer(m.mdRenderer))

	m.rebuildMarkdownRenderer()
	afterFirst := uintptr(unsafe.Pointer(m.mdRenderer))
	m.rebuildMarkdownRenderer()
	afterSecond := uintptr(unsafe.Pointer(m.mdRenderer))

	if afterFirst != before {
		t.Fatalf("expected unchanged wrap width to avoid renderer rebuild")
	}
	if afterSecond != afterFirst {
		t.Fatalf("expected repeated rebuild with unchanged width to be a no-op")
	}
}

func TestRebuildMarkdownRendererUpdatesWhenWrapWidthChanges(t *testing.T) {
	m := newTestModel()
	before := uintptr(unsafe.Pointer(m.mdRenderer))

	m.handleResize(120, 24)
	m.rebuildMarkdownRenderer()
	after := uintptr(unsafe.Pointer(m.mdRenderer))

	if after == before {
		t.Fatalf("expected renderer rebuild after wrap width change")
	}
	if m.markdownWrapWidth != m.mainColumnWidth()-4 {
		t.Fatalf("expected markdown wrap width %d, got %d", m.mainColumnWidth()-4, m.markdownWrapWidth)
	}
}

func TestSpinnerFrameGlyphUsesWholeRune(t *testing.T) {
	if got := spinnerFrameGlyph(0); got != "⠋" {
		t.Fatalf("expected first spinner glyph ⠋, got %q", got)
	}
	if got := spinnerFrameGlyph(9); got != "⠏" {
		t.Fatalf("expected last spinner glyph ⠏, got %q", got)
	}
}

func TestActiveStatusBarDoesNotShowBrokenSpinnerByte(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Running command"
	m.spinner.Start("Run: cd")

	rendered := m.renderStatusBar()

	if strings.Contains(rendered, "â") {
		t.Fatalf("expected status bar to avoid broken spinner byte, got %q", rendered)
	}
	if !strings.Contains(rendered, "⠋") {
		t.Fatalf("expected status bar to include spinner glyph, got %q", rendered)
	}
}

func TestActiveStatusBarUsesSpinnerGlyphInsteadOfHourglass(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Thinking"

	rendered := m.renderStatusBar()

	if strings.Contains(rendered, "⏳") {
		t.Fatalf("expected status bar to avoid hourglass icon, got %q", rendered)
	}
	if !strings.Contains(rendered, "⠋") {
		t.Fatalf("expected status bar to show spinner glyph, got %q", rendered)
	}
}

func TestToolStartReturnsSpinnerTickCommand(t *testing.T) {
	m := newTestModel()
	m.loading = true

	_, cmd := m.Update(toolStatusMsg(ToolStatusMsg{
		ToolName:    "run_command",
		DisplayName: "Run",
		Detail:      "pod install",
		Activity:    "Running pod install",
		Running:     true,
	}))

	if cmd == nil {
		t.Fatal("expected starting a running tool to schedule spinner ticks")
	}
}

func TestSubmitShellCommandStartsSpinnerImmediately(t *testing.T) {
	m := newTestModel()

	cmd := m.submitShellCommand("pwd", true)

	if !m.spinner.IsActive() {
		t.Fatal("expected shell submit to start spinner immediately")
	}
	if cmd == nil {
		t.Fatal("expected shell submit to return commands")
	}
}

func TestSpinnerMsgSchedulesNextTick(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.spinner.Start("Run: pod install")
	before := m.spinner.CurrentFrame()

	model, cmd := m.Update(spinnerMsg{})
	m2 := model.(Model)

	if m2.spinner.CurrentFrame() == before {
		t.Fatal("expected spinner frame to advance on spinner tick")
	}
	if cmd == nil {
		t.Fatal("expected spinner tick to schedule the next frame")
	}
}

func TestStatusMsgRestartsStoppedLoadingSpinner(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Thinking..."

	next, cmd := m.Update(statusMsg{Activity: "Writing...", ToolName: "read_file", ToolArg: "README.md", ToolCount: 1})
	if cmd == nil {
		t.Fatal("expected status update to restart spinner while loading")
	}
	m = next.(Model)
	if !m.spinner.IsActive() {
		t.Fatal("expected spinner to restart on status update")
	}
}

func TestAgentStreamMsgRestartsStoppedLoadingSpinner(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 1
	m.statusActivity = "Writing..."

	next, cmd := m.Update(agentStreamMsg{RunID: 1, Text: "hello"})
	if cmd == nil {
		t.Fatal("expected stream update to restart spinner while loading")
	}
	m = next.(Model)
	if !m.spinner.IsActive() {
		t.Fatal("expected spinner to restart on stream update")
	}
}

func TestToolCompletionResumesLoadingSpinner(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Thinking..."
	m.spinner.Start("Read: README.md")

	next, cmd := m.Update(toolStatusMsg(ToolStatusMsg{
		ToolName:    "read_file",
		DisplayName: "Read",
		Detail:      "README.md",
		Activity:    "Reading README.md",
		Running:     false,
	}))
	if cmd == nil {
		t.Fatal("expected tool completion to resume loading spinner")
	}
	m = next.(Model)
	if !m.spinner.IsActive() {
		t.Fatal("expected spinner to resume after tool completion")
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
	if m.input.Placeholder != "输入消息...（$ / ! 进入 shell 模式）" {
		t.Fatalf("expected localized placeholder, got %q", m.input.Placeholder)
	}
	if !strings.Contains(m.output.String(), "已切换语言为") {
		t.Fatal("expected language switch output")
	}
}

func TestLangCommandWithoutArgsOpensSelector(t *testing.T) {
	m := newTestModel()

	cmd := m.handleLangCommand([]string{"/lang"})
	if cmd != nil {
		t.Fatal("expected /lang to open selector synchronously")
	}
	if len(m.langOptions) != 2 {
		t.Fatalf("expected 2 language options, got %d", len(m.langOptions))
	}
	if m.langOptions[m.langCursor].lang != LangEnglish {
		t.Fatalf("expected current language to be preselected, got %s", m.langOptions[m.langCursor].lang)
	}
	panel := m.renderContextPanel()
	if !strings.Contains(panel, "Switch interface language") {
		t.Fatal("expected language selector panel title")
	}
	if !strings.Contains(panel, "English (e)") || !strings.Contains(panel, "简体中文 (z)") {
		t.Fatal("expected both language options to be rendered")
	}
}

func TestLanguageSelectorEnterSwitchesLanguage(t *testing.T) {
	m := newTestModel()
	m.handleLangCommand([]string{"/lang"})
	m.langCursor = 1

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected language selection to update synchronously")
	}
	m2 := next.(Model)
	if m2.currentLanguage() != LangZhCN {
		t.Fatalf("expected zh-CN after selector confirm, got %s", m2.currentLanguage())
	}
	if len(m2.langOptions) != 0 {
		t.Fatal("expected language selector to close after confirmation")
	}
	if !strings.Contains(m2.output.String(), "已切换语言为") {
		t.Fatal("expected language switch output after confirmation")
	}
}

func TestLanguageSelectorEscClosesWithoutChangingLanguage(t *testing.T) {
	m := newTestModel()
	m.handleLangCommand([]string{"/lang"})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc to close selector synchronously")
	}
	m2 := next.(Model)
	if len(m2.langOptions) != 0 {
		t.Fatal("expected language selector to close")
	}
	if m2.currentLanguage() != LangEnglish {
		t.Fatalf("expected language to remain English, got %s", m2.currentLanguage())
	}
}

func TestLanguageSelectorCtrlCTriggersExitConfirm(t *testing.T) {
	m := newTestModel()
	m.handleLangCommand([]string{"/lang"})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("expected first ctrl-c to arm exit confirmation")
	}
	m2 := next.(Model)
	if !m2.exitConfirmPending {
		t.Fatal("expected ctrl-c in language selector to arm exit confirmation")
	}
	if len(m2.langOptions) == 0 {
		t.Fatal("expected language selector to stay visible until explicit close or quit")
	}
}

func TestSetConfigUsesPersistedLanguageWithoutOpeningSelector(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Language = "zh-CN"

	m.SetConfig(cfg)

	if m.currentLanguage() != LangZhCN {
		t.Fatalf("expected persisted language zh-CN, got %s", m.currentLanguage())
	}
	if len(m.langOptions) != 0 {
		t.Fatal("expected persisted language to apply without opening selector")
	}
}

func TestSetConfigFirstRunOpensLanguageSelector(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.FirstRun = true
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")

	m.SetConfig(cfg)

	if !m.languagePromptRequired {
		t.Fatal("expected first-run language prompt to be required")
	}
	if len(m.langOptions) != 2 {
		t.Fatalf("expected first-run language selector to open, got %d options", len(m.langOptions))
	}
	panel := m.renderContextPanel()
	if !strings.Contains(panel, "Choose your preferred language") {
		t.Fatal("expected first-run language onboarding title")
	}
}

func TestFirstRunLanguageSelectorPersistsChoice(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg.FilePath = path
	cfg.FirstRun = true
	m.SetConfig(cfg)
	m.langCursor = 1

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected first-run language selection to update synchronously")
	}
	m2 := next.(Model)
	if m2.currentLanguage() != LangZhCN {
		t.Fatalf("expected zh-CN after onboarding confirm, got %s", m2.currentLanguage())
	}
	if m2.languagePromptRequired {
		t.Fatal("expected onboarding flag to clear after confirmation")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "language: zh-CN\n" {
		t.Fatalf("expected persisted language file, got %q", string(data))
	}
}

func TestFirstRunLanguageSelectorEscDoesNotClose(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.FirstRun = true
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	m.SetConfig(cfg)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc to be ignored during required first-run language prompt")
	}
	m2 := next.(Model)
	if len(m2.langOptions) == 0 {
		t.Fatal("expected first-run language selector to remain open")
	}
	if !m2.languagePromptRequired {
		t.Fatal("expected first-run language prompt to remain required")
	}
}

func TestChineseViewRendersLocalizedPanels(t *testing.T) {
	m := newTestModel()
	m.setLanguage("zh-CN")
	m.handleResize(100, 28)

	view := m.View()
	if !strings.Contains(view, "输入消息") {
		t.Error("expected localized placeholder")
	}
	if strings.Contains(view, "对话") {
		t.Error("expected localized conversation heading to be removed")
	}
	if strings.Contains(view, "输入\n") || strings.Contains(view, "输入\r\n") {
		t.Error("expected localized composer heading to be removed")
	}
}

func TestViewContainsPanels(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 28)
	view := m.View()
	if !strings.Contains(view, "ggcode") {
		t.Error("expected branded header in view")
	}
	if strings.Contains(view, "Conversation") {
		t.Error("expected conversation heading to be removed")
	}
	if strings.Contains(view, "Composer") {
		t.Error("expected composer heading to be removed")
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
	if !strings.Contains(view, "ggcode") {
		t.Error("expected branded logo content in right sidebar")
	}
	if !strings.Contains(view, "Mode policy") || !strings.Contains(view, "approval") {
		t.Error("expected mode policy section in sidebar")
	}
}

func TestWideLayoutLeavesRightMarginForSidebarBorder(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)

	if !m.sidebarEnabled() {
		t.Fatal("expected sidebar layout to be enabled")
	}

	total := m.mainColumnWidth() + 1 + m.sidebarWidth()
	if total != m.viewWidth()-1 {
		t.Fatalf("expected composed width %d, got %d", m.viewWidth()-1, total)
	}

	if margin := m.terminalRightMargin(); margin != 1 {
		t.Fatalf("expected sidebar outer margin 1, got %d", margin)
	}
}

func TestNarrowLayoutLeavesRightMarginForMainPanels(t *testing.T) {
	m := newTestModel()
	m.sidebarVisible = false
	m.handleResize(128, 28)

	if m.sidebarEnabled() {
		t.Fatal("expected sidebar to be disabled")
	}

	if got := m.mainColumnWidth(); got != m.viewWidth()-1 {
		t.Fatalf("expected main column width %d, got %d", m.viewWidth()-1, got)
	}

	view := m.View()
	if got := lipgloss.Width(view); got > m.viewWidth()-1 {
		t.Fatalf("expected rendered width <= %d, got %d", m.viewWidth()-1, got)
	}
}

func TestPanelRenderWidthsStayWithinAssignedColumns(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)

	if got := lipgloss.Width(m.renderConversationPanel(12)); got > m.mainColumnWidth() {
		t.Fatalf("expected conversation width <= %d, got %d", m.mainColumnWidth(), got)
	}
	if got := lipgloss.Width(m.renderComposerPanel()); got > m.mainColumnWidth() {
		t.Fatalf("expected composer width <= %d, got %d", m.mainColumnWidth(), got)
	}
	if got := lipgloss.Width(m.renderSidebar(20)); got > m.sidebarWidth() {
		t.Fatalf("expected sidebar width <= %d, got %d", m.sidebarWidth(), got)
	}
}

func TestSidebarDetailRowClipsByDisplayWidth(t *testing.T) {
	m := newTestModel()
	row := m.renderSidebarDetailRow("目录", "~/ggai/chain-convenience-store-sales-management-system", 24)
	for _, line := range strings.Split(row, "\n") {
		if lipgloss.Width(line) > 24 {
			t.Fatalf("expected sidebar row width <= 24, got %d for %q", lipgloss.Width(line), line)
		}
	}
}

func TestTruncateDisplayWidthHandlesWideRunes(t *testing.T) {
	got := truncateDisplayWidth("国内 Coding Plan (Anthropic)", 12)
	if lipgloss.Width(got) > 12 {
		t.Fatalf("expected truncated width <= 12, got %d for %q", lipgloss.Width(got), got)
	}
}

func TestWideLayoutSidebarMatchesColumnHeight(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 40)

	header := ""
	if m.topHeaderEnabled() {
		header = m.renderHeader()
	}
	startupBanner := m.renderStartupBanner()
	actionPanel := m.renderContextPanel()
	statusBar := m.renderStatusBar()
	composer := m.renderComposerPanel()

	availableHeight := m.viewHeight() - lipgloss.Height(header) - lipgloss.Height(startupBanner) - lipgloss.Height(composer)
	if actionPanel != "" {
		availableHeight -= lipgloss.Height(actionPanel)
	}
	if statusBar != "" {
		availableHeight -= lipgloss.Height(statusBar)
	}
	if availableHeight < 8 {
		availableHeight = 8
	}

	sections := []string{}
	if header != "" {
		sections = append(sections, header)
	}
	if startupBanner != "" {
		sections = append(sections, startupBanner)
	}
	sections = append(sections, m.renderConversationPanel(availableHeight))
	if actionPanel != "" {
		sections = append(sections, actionPanel)
	}
	if statusBar != "" {
		sections = append(sections, statusBar)
	}
	sections = append(sections, composer)

	left := lipgloss.JoinVertical(lipgloss.Left, sections...)
	sidebar := m.renderSidebar(lipgloss.Height(left))

	if lipgloss.Height(sidebar) != lipgloss.Height(left) {
		t.Fatalf("expected sidebar height %d to match left column height %d", lipgloss.Height(sidebar), lipgloss.Height(left))
	}
}

func TestRenderLogoUsesStyledWordmarkByDefault(t *testing.T) {
	logo := renderLogo(16)
	if logo == asciiLogo() {
		t.Fatal("expected styled wordmark by default")
	}
	if !strings.Contains(logo, "GG") || !strings.Contains(logo, "CODE") {
		t.Fatal("expected branded wordmark output")
	}
}

func TestRenderLogoFallsBackToASCIIWhenForced(t *testing.T) {
	t.Setenv("GGCODE_ASCII_LOGO", "1")
	if got := renderLogo(16); got != asciiLogo() {
		t.Fatal("expected ascii fallback when GGCODE_ASCII_LOGO is set")
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

func TestSidebarRendersMCPSectionAndActiveTools(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	m.mcpServers = []MCPInfo{
		{Name: "web-reader", Connected: true, Transport: "http", Migrated: true},
		{Name: "zai-mcp-server", Pending: true, Transport: "stdio"},
		{Name: "server-3", Connected: true, Transport: "stdio"},
		{Name: "server-4", Connected: true, Transport: "stdio"},
		{Name: "server-5", Connected: true, Transport: "stdio"},
		{Name: "server-6", Connected: true, Transport: "stdio"},
	}
	m.activeMCPTools = map[string]ToolStatusMsg{
		"mcp__web-reader__fetch": {
			ToolName:    "mcp__web-reader__fetch",
			DisplayName: "Fetch",
			Detail:      "docs",
			Running:     true,
		},
	}

	view := m.View()

	if !strings.Contains(view, "MCP") || !strings.Contains(view, "5 up • 1 pending • 0 failed") {
		t.Fatal("expected MCP summary block in sidebar")
	}
	if !strings.Contains(view, "web-reader (http)") || !strings.Contains(view, "zai-mcp-server (stdio)") {
		t.Fatal("expected MCP server rows in sidebar")
	}
	if strings.Contains(view, "server-6 (stdio)") {
		t.Fatal("expected sidebar MCP list to cap at five servers")
	}
	if !strings.Contains(view, "/mcp") {
		t.Fatal("expected sidebar MCP overflow hint")
	}
	if !strings.Contains(view, "Active tools") || !strings.Contains(view, "web-reader") {
		t.Fatal("expected active MCP tool list in sidebar")
	}
}

func TestSidebarRendersWorkingDirectoryAndGitBranch(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, ".git", "refs", "heads"), 0o755); err != nil {
		t.Fatalf("mkdir refs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git", "HEAD"), []byte("ref: refs/heads/feature/sidebar\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	m := newTestModel()
	m.handleResize(128, 28)

	if got := m.sidebarWorkingDirectory(); !strings.HasSuffix(got, filepath.Base(repoDir)) {
		t.Fatalf("expected sidebarWorkingDirectory to end with %q, got %q", filepath.Base(repoDir), got)
	}
	if got := m.sidebarGitBranch(); got != "feature/sidebar" {
		t.Fatalf("expected sidebarGitBranch feature/sidebar, got %q", got)
	}

	view := m.View()

	if !strings.Contains(view, "cwd") {
		t.Fatalf("expected sidebar cwd row, got %q", view)
	}
	if !strings.Contains(view, "branch") || !strings.Contains(view, "feature/sidebar") {
		t.Fatalf("expected sidebar branch row, got %q", view)
	}
}

func TestLoadedSkillCountExcludesLegacyCommandsAndMCP(t *testing.T) {
	m := newTestModel()
	m.commandMgr = commands.NewManager(t.TempDir())
	base := m.loadedSkillCount()
	m.commandMgr.SetExtraProviders(func() []*commands.Command {
		return []*commands.Command{
			{Name: "extra-skill", LoadedFrom: commands.LoadedFromSkills},
			{Name: "legacy-command", LoadedFrom: commands.LoadedFromCommands},
			{Name: "mcp-prompt", LoadedFrom: commands.LoadedFromMCP},
		}
	})

	if got := m.loadedSkillCount(); got != base+1 {
		t.Fatalf("expected loaded skill count delta of 1, got base=%d current=%d", base, got)
	}
}

func TestSidebarRendersContextSection(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	m.agent = agent.NewAgent(nil, tool.NewRegistry(), "", 1)
	m.agent.ContextManager().SetMaxTokens(1000)
	m.agent.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("x", 400)}}})

	view := m.View()

	if !strings.Contains(view, "Context") || !strings.Contains(view, "compact") {
		t.Fatalf("expected context section in sidebar, got %q", view)
	}
	if !strings.Contains(view, "1k") {
		t.Fatalf("expected context window display, got %q", view)
	}
}

func TestCtrlRTogglesSidebarVisibility(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	if !m.sidebarEnabled() {
		t.Fatal("expected sidebar enabled by default on wide layout")
	}

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd != nil {
		t.Fatal("expected ctrl+r toggle to be synchronous")
	}
	m = model.(Model)
	if m.sidebarVisible {
		t.Fatal("expected ctrl+r to hide sidebar")
	}
	if m.sidebarEnabled() {
		t.Fatal("expected sidebar to be disabled after ctrl+r")
	}
	if strings.Contains(m.View(), "geek AI workspace") {
		t.Fatal("expected ctrl+r sidebar hide to suppress the top header too")
	}

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = model.(Model)
	if !m.sidebarVisible || !m.sidebarEnabled() {
		t.Fatal("expected second ctrl+r to show sidebar again")
	}
}

func TestSetConfigAppliesPersistedSidebarVisibility(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	visible := false
	cfg.UI.SidebarVisible = &visible

	m.SetConfig(cfg)

	if m.sidebarVisible {
		t.Fatal("expected sidebar visibility to follow persisted config")
	}
}

func TestCtrlRPersistsSidebarVisibility(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	m.SetConfig(cfg)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = model.(Model)
	if m.sidebarVisible {
		t.Fatal("expected ctrl+r to hide sidebar")
	}

	loaded, err := config.Load(cfg.FilePath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.SidebarVisible() {
		t.Fatal("expected persisted sidebar preference to be false after ctrl+r")
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
	if !strings.Contains(view, "ggcode") {
		t.Error("expected branded header in narrow layout")
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

	if strings.Contains(output, "Todo:") || strings.Contains(output, "🎯") {
		t.Fatalf("expected main content to omit todo heading, got %q", output)
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
	if !strings.Contains(output, "\n\n 📦 Exploring project context") {
		t.Fatalf("expected spacing between grouped sections, got %q", output)
	}
}

func TestRenderGroupedActivitiesMergesSameTodoIntoSingleBlock(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true
	m.activeTodo = &todoStateItem{ID: "todo-1", Content: "Polish TUI activity flow", Status: "in_progress"}

	m.startToolActivity(ToolStatusMsg{ToolName: "run_command", DisplayName: "Run", Detail: "build", Running: true})
	m.finishToolActivity(ToolStatusMsg{ToolName: "run_command", DisplayName: "Run", Detail: "build", Running: false, Result: "ok"})
	m.closeToolActivityGroup()
	m.startToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: true})
	m.finishToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: false, Result: "line1\nline2"})
	m.closeToolActivityGroup()

	output := m.renderGroupedActivities()

	if strings.Contains(output, "Todo:") || strings.Contains(output, "🎯") {
		t.Fatalf("expected grouped activities to omit todo heading, got %q", output)
	}
	if !strings.Contains(output, "📦 Running commands\n    • Run build") {
		t.Fatalf("expected running commands section, got %q", output)
	}
	if !strings.Contains(output, "\n\n 📦 Exploring project context\n    • Read README.md") {
		t.Fatalf("expected later same-todo work to stay in the same block with spacing, got %q", output)
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

func TestRenderGroupedActivitiesShowsCommandPreviewInsteadOfRawOutput(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true

	msg := ToolStatusMsg{
		ToolName:    "run_command",
		DisplayName: "Restart metro cleanly",
		Running:     false,
		RawArgs:     `{"command":"# Restart metro cleanly\ncd /tmp/app\nrm -rf .metro-cache\nnpm install\nnpm run start -- --reset-cache\nsleep 2\necho ready\n"}`,
		Result:      "booting\nready\n",
	}
	m.startToolActivity(ToolStatusMsg{
		ToolName:    "run_command",
		DisplayName: "Restart metro cleanly",
		Running:     true,
		RawArgs:     msg.RawArgs,
	})
	m.finishToolActivity(msg)
	m.closeToolActivityGroup()

	output := m.renderGroupedActivities()

	if !strings.Contains(output, "• Restart metro cleanly") {
		t.Fatalf("expected command title to render as the item header, got %q", output)
	}
	if !strings.Contains(output, "cd /tmp/app") || !strings.Contains(output, "npm run start -- --reset-cache") {
		t.Fatalf("expected command preview lines to render, got %q", output)
	}
	if !strings.Contains(output, "booting") || !strings.Contains(output, "ready") {
		t.Fatalf("expected command stdout preview to render, got %q", output)
	}
	if strings.Contains(output, "# Restart metro cleanly") {
		t.Fatalf("expected title comment to be extracted from command preview, got %q", output)
	}
}

func TestRenderGroupedActivitiesCommandWithoutCommentAvoidsDuplicateTitle(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true

	msg := ToolStatusMsg{
		ToolName:    "run_command",
		DisplayName: "Run",
		Running:     false,
		RawArgs:     `{"command":"git reset HEAD PR_DESCRIPTION.md .ggcode/todos.json 2>/dev/null\ngit status --short | head -5\n"}`,
		Result:      "M README.md\nA internal/tui/view.go\n",
	}
	m.startToolActivity(ToolStatusMsg{
		ToolName:    "run_command",
		DisplayName: "Run",
		Running:     true,
		RawArgs:     msg.RawArgs,
	})
	m.finishToolActivity(msg)
	m.closeToolActivityGroup()

	output := m.renderGroupedActivities()

	if strings.Contains(output, "Run git reset") || strings.Contains(output, "执行 git reset") {
		t.Fatalf("expected command preview without duplicated title, got %q", output)
	}
	if !strings.Contains(output, "git reset HEAD PR_DESCRIPTION.md .ggcode/todos.json 2>/dev/null") {
		t.Fatalf("expected first command line to render directly, got %q", output)
	}
	if !strings.Contains(output, "M README.md") {
		t.Fatalf("expected output preview lines to render, got %q", output)
	}
}

func TestRenderGroupedActivitiesCommandOutputAppendsHiddenCountToLastLine(t *testing.T) {
	m := newTestModel()
	m.setLanguage("zh-CN")
	m.handleResize(120, 40)
	m.loading = true

	msg := ToolStatusMsg{
		ToolName:    "run_command",
		DisplayName: "Run",
		Running:     false,
		RawArgs:     `{"command":"echo one\necho two\n"}`,
		Result:      "1\n2\n3\n4\n5\n6\n7\n",
	}
	m.startToolActivity(ToolStatusMsg{
		ToolName:    "run_command",
		DisplayName: "Run",
		Running:     true,
		RawArgs:     msg.RawArgs,
	})
	m.finishToolActivity(msg)
	m.closeToolActivityGroup()

	output := m.renderGroupedActivities()

	if !strings.Contains(output, "5 … 还有 2 行输出") {
		t.Fatalf("expected hidden output count to be appended to last visible line, got %q", output)
	}
	if strings.Contains(output, "\n      6") || strings.Contains(output, "\n      7") {
		t.Fatalf("expected output preview to cap at five lines, got %q", output)
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

func TestRenderOutputShowsSubAgentProgressSummary(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	id := m.subAgentMgr.Spawn("context\n\nBuild release", "Build release", nil, context.Background())
	sa, ok := m.subAgentMgr.Get(id)
	if !ok {
		t.Fatal("expected spawned subagent")
	}
	sa.Status = subagent.StatusRunning
	sa.ProgressSummary = "Job ID: cmd-1 • Status: running • Total lines: 42"
	sa.ToolCallCount = 3

	output := m.renderOutput()

	if !strings.Contains(output, "Job ID: cmd-1") || !strings.Contains(output, "Total lines: 42") {
		t.Fatalf("expected subagent progress summary, got %q", output)
	}
}

func TestRenderOutputHidesCompletedSubAgentState(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	id := m.subAgentMgr.Spawn("context\n\nInvestigate parser behavior", "Investigate parser behavior", nil, context.Background())
	sa, ok := m.subAgentMgr.Get(id)
	if !ok {
		t.Fatal("expected spawned subagent")
	}
	sa.Status = subagent.StatusCompleted
	sa.CurrentPhase = "completed"
	sa.ToolCallCount = 2

	output := m.renderOutput()

	if strings.Contains(output, "🤖") || strings.Contains(output, "Investigate parser behavior") || strings.Contains(output, "Completed") {
		t.Fatalf("expected completed subagent to be hidden from live content area, got %q", output)
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
	m.output.WriteString(assistantBulletStyle.Render("● ") + "streaming")
	m.spinner.Start("Writing response")
	m.spinner.frame = 2

	output := m.renderOutput()

	if !strings.Contains(output, "○ ") {
		t.Fatalf("expected breathing bullet frame in output, got %q", output)
	}
}

func TestAgentStreamMsgRendersMarkdownIncrementally(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 32)
	m.rebuildMarkdownRenderer()
	m.loading = true
	m.activeAgentRunID = 1
	m.streamBuffer = &bytes.Buffer{}

	next, _ := m.Update(agentStreamMsg{
		RunID: 1,
		Text:  "### Streaming title\n\nUse `foo` now.",
	})
	updated := next.(Model)

	rendered := updated.renderCurrentStreamMarkdown()
	if rendered == "" {
		t.Fatal("expected rendered markdown preview during streaming")
	}
	if !strings.Contains(updated.output.String(), rendered) {
		t.Fatalf("expected streaming output to contain rendered markdown, got %q", updated.output.String())
	}
	if strings.Contains(updated.output.String(), "`foo`") {
		t.Fatalf("expected inline code markdown to be rendered before completion, got %q", updated.output.String())
	}
	if strings.Contains(updated.output.String(), "Use `foo` now.") {
		t.Fatalf("expected rendered output to differ from raw markdown chunk, got %q", updated.output.String())
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

func TestComposerPanelUsesModeColors(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 28)

	m.mode = permission.SupervisedMode
	supervised := m.renderComposerPanel()
	m.mode = permission.AutoMode
	auto := m.renderComposerPanel()
	m.mode = permission.BypassMode
	bypass := m.renderComposerPanel()

	if supervised == auto || auto == bypass || supervised == bypass {
		t.Fatal("expected composer panel rendering to change with mode color")
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
	if strings.Contains(result, "1. /help") {
		t.Error("did not expect numbered slash autocomplete entries")
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

func TestModeSwitchPersistsDefaultModePreference(t *testing.T) {
	m := newTestModel()
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg := config.DefaultConfig()
	cfg.FilePath = path
	m.config = cfg

	next, cmd := m.handleModeSwitch()
	m = next.(Model)

	if cmd != nil {
		t.Fatal("expected no command from mode switch")
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.DefaultMode != permission.PlanMode.String() {
		t.Fatalf("expected persisted mode %q, got %q", permission.PlanMode.String(), loaded.DefaultMode)
	}
	if m.output.Len() != 0 {
		t.Errorf("expected no output on successful mode persistence, got %q", m.output.String())
	}
}

func TestModeCommandPersistsDefaultModePreference(t *testing.T) {
	m := newTestModel()
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg := config.DefaultConfig()
	cfg.FilePath = path
	m.config = cfg

	cmd := m.handleModeCommand([]string{"/mode", "auto"})
	if cmd != nil {
		t.Fatal("expected no command from /mode set")
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.DefaultMode != permission.AutoMode.String() {
		t.Fatalf("expected persisted mode %q, got %q", permission.AutoMode.String(), loaded.DefaultMode)
	}
	if m.output.Len() != 0 {
		t.Errorf("expected no output on successful /mode persistence, got %q", m.output.String())
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

func TestAskUserRenderedInContextPanel(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 32)
	m.pendingQuestionnaire = newQuestionnaireState(tool.AskUserRequest{
		Title: "Clarify release plan",
		Questions: []tool.AskUserQuestion{
			{
				ID:            "scope",
				Title:         "Scope",
				Prompt:        "Which slice should land first?",
				Kind:          tool.AskUserKindSingle,
				AllowFreeform: true,
				Choices: []tool.AskUserChoice{
					{ID: "frontend", Label: "Frontend"},
					{ID: "backend", Label: "Backend"},
				},
			},
		},
	}, make(chan tool.AskUserResponse, 1), LangEnglish)
	m.syncQuestionnaireInputWidth()

	result := m.renderContextPanel()

	if !strings.Contains(result, "Answer questions") {
		t.Fatal("expected ask_user panel title")
	}
	if !strings.Contains(result, "Clarify release plan") {
		t.Fatal("expected questionnaire title")
	}
	if !strings.Contains(result, "Which slice should land first?") {
		t.Fatal("expected active question prompt")
	}
	if !strings.Contains(result, "Submit") || !strings.Contains(result, "Cancel") {
		t.Fatal("expected action tabs")
	}
}

func TestAskUserEnterAdvancesAndSubmitReturnsStructuredResponse(t *testing.T) {
	m := newTestModel()
	response := make(chan tool.AskUserResponse, 1)
	m.pendingQuestionnaire = newQuestionnaireState(tool.AskUserRequest{
		Questions: []tool.AskUserQuestion{
			{
				ID:            "scope",
				Title:         "Scope",
				Prompt:        "Pick scope",
				Kind:          tool.AskUserKindSingle,
				AllowFreeform: true,
				Choices: []tool.AskUserChoice{
					{ID: "frontend", Label: "Frontend"},
					{ID: "backend", Label: "Backend"},
				},
			},
			{
				ID:            "notes",
				Title:         "Notes",
				Prompt:        "Anything else?",
				Kind:          tool.AskUserKindText,
				AllowFreeform: true,
			},
		},
	}, response, LangEnglish)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(Model)
	if _, ok := m.pendingQuestionnaire.answers[0].selected["frontend"]; !ok {
		t.Fatal("expected space to select the highlighted choice")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.pendingQuestionnaire.tabIndex != 1 {
		t.Fatalf("expected enter to advance to next question, got tab %d", m.pendingQuestionnaire.tabIndex)
	}

	m.pendingQuestionnaire.input.SetValue("Release safety first.")

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !m.pendingQuestionnaire.onSubmitTab() {
		t.Fatal("expected enter on last question to move to submit tab")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.pendingQuestionnaire != nil {
		t.Fatal("expected questionnaire to clear after submission")
	}

	select {
	case result := <-response:
		if result.Status != tool.AskUserStatusSubmitted {
			t.Fatalf("expected submitted result, got %q", result.Status)
		}
		if result.AnsweredCount != 2 {
			t.Fatalf("expected answered_count=2, got %d", result.AnsweredCount)
		}
		if got := result.Answers[0].SelectedChoiceIDs; len(got) != 1 || got[0] != "frontend" {
			t.Fatalf("unexpected selected ids: %#v", got)
		}
		if result.Answers[1].FreeformText != "Release safety first." {
			t.Fatalf("unexpected freeform text: %q", result.Answers[1].FreeformText)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected ask_user submission response")
	}
}

func TestAskUserEscapeReturnsCancelledResponse(t *testing.T) {
	m := newTestModel()
	response := make(chan tool.AskUserResponse, 1)
	m.pendingQuestionnaire = newQuestionnaireState(tool.AskUserRequest{
		Questions: []tool.AskUserQuestion{
			{
				ID:            "notes",
				Title:         "Notes",
				Prompt:        "Anything else?",
				Kind:          tool.AskUserKindText,
				AllowFreeform: true,
			},
		},
	}, response, LangEnglish)

	m.pendingQuestionnaire.input.SetValue("partial answer")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.pendingQuestionnaire != nil {
		t.Fatal("expected questionnaire to clear after escape")
	}

	select {
	case result := <-response:
		if result.Status != tool.AskUserStatusCancelled {
			t.Fatalf("expected cancelled result, got %q", result.Status)
		}
		if len(result.Answers) != 1 || result.Answers[0].FreeformText != "partial answer" {
			t.Fatalf("unexpected cancelled payload: %#v", result.Answers)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected ask_user cancel response")
	}
}

func TestRenderStatusBarOmitsAgentTitle(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Thinking..."

	result := m.renderStatusBar()

	if strings.Contains(result, "Agent status") {
		t.Fatal("expected agent status panel title to be hidden")
	}
	if !strings.Contains(result, "Thinking...") {
		t.Fatal("expected agent activity content to remain visible")
	}
}

func TestAgentErrMsgFormatsAnthropicSerializationFailure(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	next, _ := m.Update(agentErrMsg{
		RunID: 7,
		Err:   errors.New("chat error: json: error calling MarshalJSON for type anthropic.MessageParam: json: error calling MarshalJSON for type anthropic.ContentBlockParamUnion: json"),
	})
	m = next.(Model)

	output := m.output.String()
	if !strings.Contains(output, "Request serialization failed before sending to Anthropic") {
		t.Fatalf("expected friendly anthropic serialization message, got %q", output)
	}
	if strings.Contains(output, "anthropic.ContentBlockParamUnion") {
		t.Fatalf("expected internal SDK type names to be hidden, got %q", output)
	}
}

func TestAgentErrMsgFormatsGenericChatFailureWithoutDoublePrefix(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 9

	next, _ := m.Update(agentErrMsg{
		RunID: 9,
		Err:   errors.New("chat error: upstream timeout"),
	})
	m = next.(Model)

	output := m.output.String()
	if !strings.Contains(output, "Model request failed: upstream timeout") {
		t.Fatalf("expected cleaned chat error prefix, got %q", output)
	}
	if strings.Contains(output, "Error: chat error:") {
		t.Fatalf("expected nested chat error prefix to be removed, got %q", output)
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

func TestInitRequestsInitialWindowSize(t *testing.T) {
	m := newTestModel()

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected init command")
	}

	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected init to return a batch command, got %T", msg)
	}
	if len(batch) != 2 {
		t.Fatalf("expected blink and window size commands, got %d", len(batch))
	}
}

func TestStartupBannerHiddenByDefault(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 30)

	view := m.View()
	if strings.Contains(view, "Initializing") {
		t.Fatalf("expected startup banner to stay hidden by default, got %q", view)
	}
}

func TestStartupBannerReadyMsgRemainsDismissed(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 30)

	model, _ := m.Update(startupReadyMsg{})
	m = model.(Model)
	if m.startupBannerVisible {
		t.Fatal("expected startup banner to remain dismissed")
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

func TestResizeTerminalColorResponseDoesNotReachInput(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 30)

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("11;rgb:0000/0000/0000\\")})
	m2 := model.(Model)

	if cmd != nil {
		t.Error("expected nil cmd for ignored terminal color response")
	}
	if m2.input.Value() != "" {
		t.Errorf("expected terminal color response to be ignored, got %q", m2.input.Value())
	}
}

func TestResizeMalformedTerminalColorResponseDoesNotReachInput(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 30)

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(") 1;rgb:0000/0000/0000\\")})
	m2 := model.(Model)

	if cmd != nil {
		t.Error("expected nil cmd for ignored malformed terminal color response")
	}
	if m2.input.Value() != "" {
		t.Errorf("expected malformed terminal color response to be ignored, got %q", m2.input.Value())
	}
}

func TestResizeConcatenatedTerminalResponsesAreSanitizedFromInput(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 30)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`]11;rgb:0000/0000/0000\]11;rgb:0000/0000/0000\1;rgb:0000/0000/0000\`)})
	m2 := model.(Model)

	if m2.input.Value() != "" {
		t.Fatalf("expected concatenated terminal responses to be stripped, got %q", m2.input.Value())
	}
}

func TestBareMouseWheelSequenceDoesNotReachInput(t *testing.T) {
	m := newTestModel()

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`<64;50;42M<64;50;42M`)})
	m2 := model.(Model)

	if cmd != nil {
		t.Error("expected nil cmd for ignored bare mouse fragment")
	}
	if m2.input.Value() != "" {
		t.Fatalf("expected bare mouse fragment to be stripped, got %q", m2.input.Value())
	}
}

func TestStartupOrphanTerminalFragmentDoesNotReachInput(t *testing.T) {
	m := newTestModel()

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`)\]`)})
	m2 := model.(Model)

	if m2.input.Value() != "" {
		t.Fatalf("expected startup orphan fragment to be stripped, got %q", m2.input.Value())
	}
}

func TestResizeSanitizerPreservesNormalInput(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 30)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	m2 := model.(Model)

	if m2.input.Value() != "hi" {
		t.Errorf("expected normal input after resize, got %q", m2.input.Value())
	}
}

func TestIdleTerminalNoiseDoesNotReachInput(t *testing.T) {
	m := newTestModel()
	m.startedAt = time.Now().Add(-10 * time.Second)

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`11;rgb:0000/0000/0000[<0;34;126M`)})
	m2 := model.(Model)

	if cmd != nil {
		t.Error("expected nil cmd for ignored idle terminal noise")
	}
	if m2.input.Value() != "" {
		t.Fatalf("expected idle terminal noise to be ignored, got %q", m2.input.Value())
	}
}

func TestIdleTerminalNoiseDoesNotOverwriteExistingInput(t *testing.T) {
	m := newTestModel()
	m.startedAt = time.Now().Add(-10 * time.Second)
	m.input.SetValue("hello")
	m.input.CursorEnd()

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`11;rgb:0000/0000/0000[<0;34;126M`)})
	m2 := model.(Model)

	if m2.input.Value() != "hello" {
		t.Fatalf("expected existing input to survive idle terminal noise, got %q", m2.input.Value())
	}
}

func TestTerminalSuppressionWindowOnlyAppliesAfterResize(t *testing.T) {
	m := newTestModel()
	if terminalResponseSuppressionActive(time.Time{}) {
		t.Fatal("expected startup not to suppress terminal response fragments")
	}
	m.handleResize(100, 30)
	if !terminalResponseSuppressionActive(m.lastResizeAt) {
		t.Fatal("expected resize window to suppress terminal response fragments")
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
	if m2.loading {
		t.Error("expected normal run interrupt to release loading immediately")
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

func TestEscLoadingCancelsCurrentActivity(t *testing.T) {
	m := newTestModel()
	cancelled := false
	m.loading = true
	m.cancelFunc = func() { cancelled = true }

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m2 := model.(Model)

	if cmd != nil {
		t.Error("expected nil cmd when interrupting active work with esc")
	}
	if !cancelled {
		t.Error("expected cancel func to be called on esc")
	}
	if m2.loading {
		t.Error("expected normal run esc interrupt to release loading immediately")
	}
	if !m2.runCanceled {
		t.Error("expected current run to be marked as canceled by esc")
	}
	if !strings.Contains(m2.output.String(), "[interrupted]") {
		t.Error("expected interrupted marker in output after esc")
	}
}

func TestCtrlCWhileAlreadyCancellingDoesNotDuplicateInterruptOutput(t *testing.T) {
	m := newTestModel()
	cancelCount := 0
	m.loading = true
	m.runCanceled = true
	m.cancelFunc = func() { cancelCount++ }
	m.output.WriteString("[interrupted]\n\n")

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m2 := model.(Model)

	if cmd != nil {
		t.Error("expected no command when already cancelling active work")
	}
	if cancelCount != 0 {
		t.Errorf("expected cancel func not to be called again, got %d", cancelCount)
	}
	if strings.Count(m2.output.String(), "[interrupted]") != 1 {
		t.Fatalf("expected interrupted marker not to duplicate, got %q", m2.output.String())
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

func TestAgentInterruptMsgRendersDeliveredInput(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 3

	next, cmd := m.Update(agentInterruptMsg{RunID: 3, Text: "please switch direction"})
	if cmd != nil {
		t.Fatal("expected interrupt delivery to render inline")
	}
	m = next.(Model)

	if !strings.Contains(m.output.String(), "please switch direction") {
		t.Fatalf("expected delivered interrupt text in output, got %q", m.output.String())
	}
	if !strings.Contains(m.output.String(), "[delivered to active run; revising plan]") {
		t.Fatalf("expected delivery marker in output, got %q", m.output.String())
	}
}

func TestRenderConversationUserEntryWrapsLongText(t *testing.T) {
	m := newTestModel()
	m.handleResize(72, 30)
	text := "再次好好建议查一下这个应用 qq-bot 插件的实现是否符合本应用的规范，同时实现的方式是否和 openclaw 的 qq-bot 实现机制是一致的"

	rendered := m.renderConversationUserEntry("❯ ", text)

	if !strings.Contains(rendered, "\n") {
		t.Fatalf("expected wrapped user entry, got %q", rendered)
	}
	if !strings.Contains(rendered, "openclaw") {
		t.Fatalf("expected wrapped user entry to preserve full text, got %q", rendered)
	}
}

func TestDollarKeyEntersShellMode(t *testing.T) {
	m := newTestModel()

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("$")})
	m = model.(Model)

	if cmd != nil {
		t.Fatal("expected shell-mode toggle to complete inline")
	}
	if !m.shellMode {
		t.Fatal("expected shell mode to be enabled")
	}
	if m.input.Prompt != "$ " {
		t.Fatalf("expected shell prompt, got %q", m.input.Prompt)
	}
	if m.input.Value() != "" {
		t.Fatalf("expected trigger rune to be consumed, got %q", m.input.Value())
	}
}

func TestShellModeEnterStartsLocalCommand(t *testing.T) {
	m := newTestModel()
	m.setShellMode(true)
	m.input.SetValue("echo hi")

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)

	if cmd == nil {
		t.Fatal("expected shell command to start asynchronously")
	}
	if !m.loading {
		t.Fatal("expected shell command to enter loading state")
	}
	if !strings.Contains(m.output.String(), "$ echo hi") {
		t.Fatalf("expected shell command to be echoed in output, got %q", m.output.String())
	}
}

func TestShellModeEscExitsShellMode(t *testing.T) {
	m := newTestModel()
	m.setShellMode(true)
	m.input.SetValue("echo hi")

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)

	if cmd != nil {
		t.Fatal("expected shell-mode exit to complete inline")
	}
	if m.shellMode {
		t.Fatal("expected shell mode to be disabled")
	}
	if m.input.Prompt != "❯ " {
		t.Fatalf("expected normal prompt, got %q", m.input.Prompt)
	}
	if m.input.Value() != "" {
		t.Fatalf("expected shell draft to clear on exit, got %q", m.input.Value())
	}
}

func TestProjectMemoryLoadingQueuesSubmissionBeforeFirstRun(t *testing.T) {
	m := newTestModel()
	m.projectMemoryLoading = true

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = model.(Model)
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)

	if cmd != nil {
		t.Fatal("expected submission to queue while project memory is still loading")
	}
	if len(m.pendingSubmissions) != 1 || m.pendingSubmissions[0] != "hi" {
		t.Fatalf("expected queued submission, got %#v", m.pendingSubmissions)
	}
	if m.projectMemoryLoading != true {
		t.Fatal("expected project memory loading flag to remain set until async load completes")
	}
}

func TestProjectMemoryLoadedConsumesQueuedSubmissionAndInjectsMemory(t *testing.T) {
	ag := agent.NewAgent(nil, tool.NewRegistry(), "base", 1)
	m := NewModel(ag, permission.NewConfigPolicy(nil, nil))
	m.projectMemoryLoading = true
	m.pendingSubmissions = []string{"hello"}

	model, cmd := m.Update(projectMemoryLoadedMsg{
		Content: "repo guidance",
		Files:   []string{"/tmp/GGCODE.md"},
	})
	m = model.(Model)

	if cmd == nil {
		t.Fatal("expected queued submission to start once project memory finishes loading")
	}
	if m.projectMemoryLoading {
		t.Fatal("expected project memory loading flag to clear")
	}
	if len(m.projMemFiles) != 1 || m.projMemFiles[0] != "/tmp/GGCODE.md" {
		t.Fatalf("unexpected project memory files: %#v", m.projMemFiles)
	}
	msgs := ag.Messages()
	if len(msgs) < 2 {
		t.Fatalf("expected base and injected project memory system messages, got %d", len(msgs))
	}
	if !strings.Contains(msgs[len(msgs)-1].Content[0].Text, "repo guidance") {
		t.Fatalf("expected injected project memory message, got %#v", msgs[len(msgs)-1])
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
	got := m.output.String()
	if !strings.Contains(got, "first question") || !strings.Contains(got, "second question") {
		t.Error("expected merged queued text to be submitted as one user message")
	}
}

func TestNextUserMessageStartsOnFreshLineAfterPartialOutput(t *testing.T) {
	m := newTestModel()
	m.output.WriteString("partial tool output")

	m.handleCommand("follow-up")

	if !strings.Contains(m.output.String(), "partial tool output\n❯ follow-up\n") {
		t.Fatalf("expected follow-up message to start on a fresh line, got %q", m.output.String())
	}
}

func TestStreamReplyStartsAfterBlankLine(t *testing.T) {
	m := newTestModel()
	m.output.WriteString("previous block\n")

	model, _ := m.Update(streamMsg("next reply"))
	m = model.(Model)

	if !strings.Contains(m.output.String(), "previous block\n\n● next reply") {
		t.Fatalf("expected stream reply to start after a blank line, got %q", m.output.String())
	}
}

func TestCompactionStatusRendersOnOwnLine(t *testing.T) {
	m := newTestModel()
	m.streamBuffer = &bytes.Buffer{}

	m.appendStreamChunk("partial reply")
	m.appendStreamChunk("[compacting conversation to stay within context window]\n")

	got := m.output.String()
	if !strings.Contains(got, "partial reply") || !strings.Contains(got, "\n● [compacting conversation to stay within context window]\n") {
		t.Fatalf("expected compaction status on its own line, got %q", got)
	}
}

func TestCompactionStatusLocalizesInChinese(t *testing.T) {
	m := newTestModel()
	m.setLanguage("zh-CN")
	m.streamBuffer = &bytes.Buffer{}

	m.appendStreamChunk("[conversation compacted]\n")

	got := m.output.String()
	if !strings.Contains(got, "● [会话已压缩]\n") {
		t.Fatalf("expected localized compacted status, got %q", got)
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

func TestCancelActiveRunClearsVisibleActivityStateImmediately(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Thinking..."
	m.statusToolName = "npm run type-check"
	m.statusToolArg = "type-check"
	m.statusToolCount = 1
	m.activityGroups = []toolActivityGroup{{Title: "Running checks", Active: true, Items: []toolActivityItem{{Summary: "Run npm run type-check", Running: true}}}}
	m.spinner.Start("npm run type-check")

	m.cancelActiveRun()

	if m.loading {
		t.Fatal("expected normal run cancel to release loading immediately")
	}
	if m.statusActivity != "" {
		t.Fatalf("expected normal run status to clear immediately, got %q", m.statusActivity)
	}
	if m.statusToolName != "" || m.statusToolArg != "" || m.statusToolCount != 0 {
		t.Fatalf("expected tool status to clear, got %q / %q / %d", m.statusToolName, m.statusToolArg, m.statusToolCount)
	}
	if len(m.activityGroups) != 0 {
		t.Fatalf("expected live activity groups to clear, got %#v", m.activityGroups)
	}
	if m.spinner.IsActive() {
		t.Fatal("expected spinner to stop on cancel")
	}
}

func TestCancelActiveHarnessRunKeepsCancellingStatus(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.harnessRunProject = &harness.Project{}
	m.statusActivity = "Thinking..."
	m.spinner.Start("running")

	m.cancelActiveRun()

	if !m.loading {
		t.Fatal("expected harness run cancel to stay loading until tracked result arrives")
	}
	if m.statusActivity != m.t("status.cancelling") {
		t.Fatalf("expected harness cancel status, got %q", m.statusActivity)
	}
}

func TestCancelledRunIgnoresLateStatusAndToolUpdates(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.runCanceled = true
	m.statusActivity = m.t("status.cancelling")

	next, cmd := m.Update(statusMsg{Activity: "Thinking...", ToolName: "npm run type-check", ToolArg: "type-check", ToolCount: 3})
	if cmd != nil {
		t.Fatal("expected no cmd for ignored late status")
	}
	m = next.(Model)
	if m.statusActivity != m.t("status.cancelling") || m.statusToolName != "" || m.statusToolCount != 0 {
		t.Fatalf("expected cancelled status to remain clean, got %q / %q / %d", m.statusActivity, m.statusToolName, m.statusToolCount)
	}

	next, cmd = m.Update(toolStatusMsg{ToolName: "run_command", DisplayName: "Run", Detail: "npm run type-check", Activity: "Executing", Running: true})
	if cmd != nil {
		t.Fatal("expected no cmd for ignored late tool update")
	}
	m = next.(Model)
	if len(m.activityGroups) != 0 {
		t.Fatalf("expected no activity groups after ignored late tool update, got %#v", m.activityGroups)
	}
}

func TestAgentRunMessagesIgnoreStaleRunID(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 2
	m.statusActivity = "Thinking..."

	next, cmd := m.Update(agentStatusMsg{RunID: 1, statusMsg: statusMsg{Activity: "Writing...", ToolName: "old"}})
	if cmd != nil {
		t.Fatal("expected no cmd for stale agent status")
	}
	m = next.(Model)
	if m.statusActivity != "Thinking..." {
		t.Fatalf("expected stale status to be ignored, got %q", m.statusActivity)
	}

	next, cmd = m.Update(agentDoneMsg{RunID: 1})
	if cmd != nil {
		t.Fatal("expected no cmd for stale agent completion")
	}
	m = next.(Model)
	if !m.loading {
		t.Fatal("expected stale completion not to end active run")
	}
}

func TestStartAgentReservesRunIDBeforeAsyncCommandRuns(t *testing.T) {
	m := newTestModel()

	cmd := m.startAgent("hello")
	if cmd == nil {
		t.Fatal("expected startAgent to return a command")
	}
	if m.activeAgentRunID != 1 {
		t.Fatalf("expected startAgent to reserve run id synchronously, got %d", m.activeAgentRunID)
	}
	if m.cancelFunc == nil {
		t.Fatal("expected startAgent to install cancel func before async work starts")
	}
}

func TestFinishedRunIgnoresLateStatusUpdates(t *testing.T) {
	m := newTestModel()
	m.loading = false

	next, cmd := m.Update(statusMsg{Activity: "Thinking...", ToolName: "npm run type-check", ToolArg: "type-check", ToolCount: 2})
	if cmd != nil {
		t.Fatal("expected no cmd for ignored post-run status")
	}
	m = next.(Model)
	if m.statusActivity != "" || m.statusToolName != "" || m.statusToolCount != 0 {
		t.Fatalf("expected stale status update to be ignored, got %q / %q / %d", m.statusActivity, m.statusToolName, m.statusToolCount)
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

func TestExitConfirmStartsOnFreshLine(t *testing.T) {
	m := newTestModel()
	m.output.WriteString("partial line")

	m.promptExitConfirm()

	if !strings.Contains(m.output.String(), "partial line\n\nPress Ctrl-C again to exit.") {
		t.Fatalf("expected exit confirm to start after a blank line, got %q", m.output.String())
	}
	if !strings.Contains(m.output.String(), "Press Ctrl-C again to exit.\n") {
		t.Fatalf("expected exit confirm body to be followed by newline, got %q", m.output.String())
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
