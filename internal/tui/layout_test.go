package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/markdown"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

// stripAnsi removes ANSI escape sequences (CSI and OSC) from a string so
// that text comparisons work regardless of lipgloss v2 styling differences.
var ansiRe = regexp.MustCompile(`\x1b\][^\x1b]*\x1b\\|\x1b\[[0-9;]*[A-Za-z]`)

func stripAnsi(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func newTestModel() Model {
	m := NewModel(nil, permission.NewConfigPolicy(nil, nil))
	m.startedAt = time.Now().Add(-2 * time.Second)
	m.inputReady = true
	return m
}

// renderedOutput returns the conversation content from whichever path is active.
// When chatList has items, it renders from chatList; otherwise falls back to m.output.
func renderedOutput(m *Model) string {
	if m.chatList != nil && m.chatList.Len() > 0 {
		w := m.conversationInnerWidth()
		h := conversationInnerHeight(m.conversationPanelHeight())
		if w < 80 {
			w = 80
		}
		if h < 20 {
			h = 20
		}
		m.chatList.SetSize(w, h)
		rendered := m.chatList.Render()
		return rendered
	}
	// Empty state — render the same guidance shown in conversation panel
	var sb strings.Builder
	sb.WriteString(m.styles.assistant.Render(m.t("empty.ask")))
	sb.WriteString("\n")
	sb.WriteString(m.styles.prompt.Render(m.t("empty.tips")))
	sb.WriteString("\n\n")
	return sb.String()
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
	// textarea.SetWidth accounts for prompt/frame internally, so Width()
	// returns the edit-area width which is smaller than what was set.
	// We verify SetWidth was called with the correct total width.
	expectedSetWidth := m.mainColumnWidth() - 6
	if got := m.input.Width(); got > expectedSetWidth || got < expectedSetWidth-10 {
		t.Errorf("expected input width ~%d (±10), got %d", expectedSetWidth, got)
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

	model, cmd := m.Update(spinnerMsg{generation: m.spinner.generation})
	m2 := model.(Model)

	if m2.spinner.CurrentFrame() == before {
		t.Fatal("expected spinner frame to advance on spinner tick")
	}
	if cmd == nil {
		t.Fatal("expected spinner tick to schedule the next frame")
	}
}

func TestSpinnerIgnoresStaleTickAfterRestart(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.spinner.Start("first")
	staleGeneration := m.spinner.generation
	m.spinner.Start("second")
	before := m.spinner.CurrentFrame()

	model, cmd := m.Update(spinnerMsg{generation: staleGeneration})
	m2 := model.(Model)

	if m2.spinner.CurrentFrame() != before {
		t.Fatal("expected stale spinner tick not to advance the current spinner")
	}
	if cmd != nil {
		t.Fatal("expected stale spinner tick not to schedule another frame")
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
	view := m.View().Content
	// The input prompt "❯ " should appear in the view even with empty content
	if !strings.Contains(view, "❯") {
		t.Error("expected input prompt in view")
	}
}

func TestViewContainsInputPlaceholder(t *testing.T) {
	m := newTestModel()
	m.handleResize(80, 24)
	view := stripAnsi(m.View().Content)
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
	if !strings.Contains(renderedOutput(&m), "已切换语言为") {
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

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	if !strings.Contains(renderedOutput(&m2), "已切换语言为") {
		t.Fatal("expected language switch output after confirmation")
	}
}

func TestLanguageSelectorEscClosesWithoutChangingLanguage(t *testing.T) {
	m := newTestModel()
	m.handleLangCommand([]string{"/lang"})

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
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

	next, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
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

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
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

	view := stripAnsi(m.View().Content)
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

func TestWideLayoutUsesRightSidebar(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)

	if !m.sidebarEnabled() {
		t.Fatal("expected sidebar layout to be enabled")
	}
	if m.mainColumnWidth() >= m.viewWidth() {
		t.Fatal("expected sidebar to reduce main column width")
	}

	view := m.View().Content
	if !strings.Contains(view, "model") || !strings.Contains(view, "branch") {
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
	if total != m.viewWidth()-m.terminalLeftMargin()-m.terminalRightMargin() {
		t.Fatalf("expected composed width %d, got %d", m.viewWidth()-m.terminalLeftMargin()-m.terminalRightMargin(), total)
	}

	if margin := m.terminalRightMargin(); margin != 0 {
		t.Fatalf("expected sidebar right margin 0, got %d", margin)
	}
}

func TestNarrowLayoutLeavesRightMarginForMainPanels(t *testing.T) {
	m := newTestModel()
	m.sidebarVisible = false
	m.handleResize(128, 28)

	if m.sidebarEnabled() {
		t.Fatal("expected sidebar to be disabled")
	}

	if got := m.mainColumnWidth(); got != m.viewWidth()-m.terminalLeftMargin()-m.terminalRightMargin() {
		t.Fatalf("expected main column width %d, got %d", m.viewWidth()-m.terminalLeftMargin()-m.terminalRightMargin(), got)
	}

	view := m.View().Content
	if got := lipgloss.Width(view); got > m.viewWidth() {
		t.Fatalf("expected rendered width <= %d, got %d", m.viewWidth(), got)
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
	if got := lipgloss.Width(m.renderSidebar()); got > m.sidebarWidth() {
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

func TestWideLayoutSidebarDoesNotExceedScreenHeight(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 40)

	sidebar := m.renderSidebar()
	sidebarH := lipgloss.Height(sidebar)

	// Sidebar uses content-based height and should not exceed total view height.
	if sidebarH > m.viewHeight() {
		t.Fatalf("sidebar height %d exceeds view height %d", sidebarH, m.viewHeight())
	}
}

func TestComposerPanelHeightDoesNotShrinkWhenInputWraps(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 28)

	samples := []string{
		strings.Repeat("输入稳定性测试", 4),
		strings.Repeat("输入稳定性测试", 6),
		strings.Repeat("输入稳定性测试", 8),
	}

	prevHeight := 0
	for _, sample := range samples {
		m.input.SetValue(sample)
		m.input.CursorEnd()
		height := lipgloss.Height(m.renderComposerPanel())
		if height < prevHeight {
			t.Fatalf("expected composer height to stay monotonic, got %d after %d", height, prevHeight)
		}
		prevHeight = height
	}
}

func TestComposerPanelMultilineDraftRendersPromptsPerLine(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	m.input.SetValue("first line\nsecond line\nthird line")
	m.input.CursorEnd()

	rendered := m.renderComposerPanel()

	// With DynamicHeight, the textarea renders a prompt per visible line.
	// Three logical lines should produce at least 3 prompts.
	if got := strings.Count(rendered, m.input.Prompt); got < 3 {
		t.Fatalf("expected at least 3 composer prompts for 3 lines, got %d in %q", got, rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) > m.mainColumnWidth() {
			t.Fatalf("expected composer line width <= %d, got %d for %q", m.mainColumnWidth(), lipgloss.Width(line), line)
		}
	}
}

func TestComposerPanelKeepsMixedWideTextInsideBorder(t *testing.T) {
	m := newTestModel()
	m.setLanguage("zh-CN")
	m.handleResize(128, 28)
	m.input.SetValue("说的方法：阿萨德浪费阿萨德浪费阿萨德浪费asdfasdfgggadggaaa")
	m.input.CursorEnd()

	rendered := m.renderComposerPanel()

	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) > m.mainColumnWidth() {
			t.Fatalf("expected composer line width <= %d, got %d for %q", m.mainColumnWidth(), lipgloss.Width(line), line)
		}
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

	view := m.View().Content
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

	view := m.View().Content
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

func TestSidebarRendersIMRuntimeDisabledState(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	m.config = &config.Config{}

	view := m.View().Content
	if !strings.Contains(view, "IM") {
		t.Fatal("expected IM section in sidebar")
	}
	if !strings.Contains(view, "disabled") {
		t.Fatalf("expected disabled IM runtime state, got %q", view)
	}
}

func TestSidebarRendersIMAdapterStatuses(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	m.config = &config.Config{}
	m.config.IM.Enabled = true
	m.config.IM.Adapters = map[string]config.IMAdapterConfig{
		"backup": {Enabled: true, Platform: "qq"},
		"hermes": {Enabled: true, Platform: "qq"},
	}
	imMgr := im.NewManager()
	imMgr.PublishAdapterState(im.AdapterState{
		Name:     "hermes",
		Platform: im.PlatformQQ,
		Healthy:  true,
		Status:   "ready",
	})
	imMgr.PublishAdapterState(im.AdapterState{
		Name:      "backup",
		Platform:  im.PlatformQQ,
		Healthy:   false,
		Status:    "connecting",
		LastError: "dial failed",
	})
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: m.currentWorkspacePath()}, nil)
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Workspace: m.currentWorkspacePath(),
		Platform:  im.PlatformQQ,
		Adapter:   "hermes",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	view := m.View().Content
	if !strings.Contains(view, "1 adapters • 1 healthy") {
		t.Fatalf("expected IM summary in sidebar, got %q", view)
	}
	if !strings.Contains(view, "✓ hermes (qq) ready") {
		t.Fatalf("expected healthy IM adapter row, got %q", view)
	}
	if strings.Contains(view, "backup (qq) connecting") {
		t.Fatalf("expected unbound adapter to stay hidden, got %q", view)
	}
}

func TestSidebarRendersConfiguredIMAdapterWithoutRuntimeState(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	m.config = &config.Config{}
	m.config.IM.Enabled = true
	m.config.IM.Adapters = map[string]config.IMAdapterConfig{
		"ggcodetest": {Enabled: true, Platform: "qq"},
	}
	imMgr := im.NewManager()
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: m.currentWorkspacePath()}, nil)
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Workspace: m.currentWorkspacePath(),
		Platform:  im.PlatformQQ,
		Adapter:   "ggcodetest",
		TargetID:  "ops",
		ChannelID: "group-ops",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	view := m.View().Content
	if !strings.Contains(view, "1 adapters • 0 healthy") {
		t.Fatalf("expected IM summary for configured adapter, got %q", view)
	}
	if !strings.Contains(view, "… ggcodetest (qq) not started") {
		t.Fatalf("expected configured adapter to appear before runtime starts, got %q", view)
	}
}

func TestSidebarHidesUnboundIMAdapters(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	m.config = &config.Config{}
	m.config.IM.Enabled = true
	m.config.IM.Adapters = map[string]config.IMAdapterConfig{
		"hermes": {Enabled: true, Platform: "qq"},
	}
	imMgr := im.NewManager()
	imMgr.PublishAdapterState(im.AdapterState{
		Name:     "hermes",
		Platform: im.PlatformQQ,
		Healthy:  true,
		Status:   "ready",
	})
	m.SetIMManager(imMgr)

	view := m.View().Content
	if strings.Contains(view, "hermes (qq) ready") {
		t.Fatalf("expected unbound IM adapter to stay hidden, got %q", view)
	}
	if !strings.Contains(view, m.t("im.none")) {
		t.Fatalf("expected IM none state without current binding, got %q", view)
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

	view := m.View().Content
	if !strings.Contains(view, "branch") || !strings.Contains(view, "feature/sidebar") {
		t.Fatalf("expected sidebar branch row, got %q", view)
	}
}

func TestSidebarRendersHomepageHyperlink(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)

	sidebar := m.renderSidebar()

	if !strings.Contains(sidebar, sidebarHomepageURL) {
		t.Fatalf("expected sidebar homepage url, got %q", sidebar)
	}
	if !strings.Contains(sidebar, "\x1b]8;;"+sidebarHomepageURL) {
		t.Fatalf("expected sidebar homepage hyperlink escape, got %q", sidebar)
	}
}

func TestLoadedSkillCountExcludesLegacyCommandsAndMCP(t *testing.T) {
	m := newTestModel()
	m.commandMgr = commands.NewManager(t.TempDir())
	base := m.loadedSkillCount()
	m.commandMgr.SetExtraProviders(func() []*commands.Command {
		return []*commands.Command{
			{Name: "extra-skill", LoadedFrom: commands.LoadedFromSkills, Enabled: true},
			{Name: "legacy-command", LoadedFrom: commands.LoadedFromCommands},
			{Name: "mcp-prompt", LoadedFrom: commands.LoadedFromMCP},
		}
	})

	if got := m.loadedSkillCount(); got != base+1 {
		t.Fatalf("expected loaded skill count delta of 1, got base=%d current=%d", base, got)
	}
}

func TestSidebarHidesContextSection(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	m.agent = agent.NewAgent(nil, tool.NewRegistry(), "", 1)
	m.agent.ContextManager().SetMaxTokens(1000)
	m.agent.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("x", 400)}}})

	view := m.View().Content
	if strings.Contains(view, "Context") {
		t.Fatal("expected context section to be removed from sidebar")
	}
}

func TestCtrlRTogglesSidebarVisibility(t *testing.T) {
	m := newTestModel()
	m.handleResize(128, 28)
	if !m.sidebarEnabled() {
		t.Fatal("expected sidebar enabled by default on wide layout")
	}

	model, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+r"})
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
	if strings.Contains(m.View().Content, "geek AI workspace") {
		t.Fatal("expected ctrl+r sidebar hide to suppress the top header too")
	}

	model, _ = m.Update(tea.KeyPressMsg{Text: "ctrl+r"})
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

	model, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+r"})
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

func TestProviderPanelRendersSelectedVendorEnvVar(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendor = "aliyun"
	cfg.Endpoint = "coding-openai"
	cfg.Model = "qwen3-coder-plus"
	m.SetConfig(cfg)
	m.openProviderPanel()

	rendered := m.renderProviderPanel()
	if !strings.Contains(rendered, "DASHSCOPE_API_KEY") {
		t.Fatalf("expected provider panel to show selected vendor env var, got %q", rendered)
	}
}

func TestProviderPanelEndpointSectionKeepsTightHeight(t *testing.T) {
	height := providerPanelEndpointHeight(2)
	if height != 3 {
		t.Fatalf("expected endpoint section height 3 after lifting models, got %d", height)
	}
}

func TestProviderPanelVendorSectionUsesWindowedHeight(t *testing.T) {
	height := providerPanelVendorHeight(20)
	if height != 17 {
		t.Fatalf("expected vendor section height 17, got %d", height)
	}
}

func TestProviderPanelModelSectionUsesStableFiveRowWindow(t *testing.T) {
	height := providerPanelModelHeight(false)
	if height != 6 {
		t.Fatalf("expected provider model section height 6, got %d", height)
	}
}

func TestProviderPanelModelSectionAddsFilterRowWhenNeeded(t *testing.T) {
	height := providerPanelModelHeight(true)
	if height != 7 {
		t.Fatalf("expected provider model section height 7 with filter row, got %d", height)
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

	view := m.View().Content
	if !strings.Contains(view, "line 079") {
		t.Error("expected latest streamed output to remain visible")
	}
}

func TestMouseEventsDoNotReachInput(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 28)
	m.input.SetValue("hello")
	m.input.CursorEnd()

	next, cmd := m.Update(tea.MouseClickMsg{
		X:      5,
		Y:      5,
		Button: tea.MouseLeft,
	})
	m = next.(Model)

	if cmd != nil {
		t.Error("expected mouse click to be handled without command")
	}
	if m.input.Value() != "hello" {
		t.Errorf("expected mouse event not to alter input, got %q", m.input.Value())
	}
}

func TestMouseClickDoesNotOpenPreviewPanelForVisibleFileToken(t *testing.T) {
	workspace := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	if err := os.MkdirAll(filepath.Join(workspace, "internal", "tui"), 0755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "internal", "tui", "model.go"), []byte("alpha\nbeta\ngamma\ndelta\nepsilon\n"), 0644); err != nil {
		t.Fatalf("write preview target: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}

	m := newTestModel()
	m.handleResize(140, 36)
	m.chatWriteSystem(nextSystemID(), "● See internal/tui/model.go:3 for details\n")
	m.syncConversationViewport()
	next, cmd := m.Update(tea.MouseClickMsg{
		X:      18,
		Y:      6,
		Button: tea.MouseLeft,
	})
	if cmd != nil {
		t.Fatal("expected mouse preview click not to schedule a command")
	}
	m = next.(Model)

	if m.previewPanel != nil {
		t.Fatal("expected mouse click not to open preview panel")
	}
}

func TestConversationViewportDoesNotUnderlinePaths(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")

	m := newTestModel()
	m.handleResize(120, 30)
	m.chatWriteSystem(nextSystemID(), "● model.go:3\n")

	view := m.renderConversationPanel(12)
	if strings.Contains(view, "\x1b[4;") || strings.Contains(view, "\x1b[4m") {
		t.Fatalf("expected conversation paths to remain plain text, got %q", view)
	}
}

func TestPreviewViewportScrollControls(t *testing.T) {
	workspace := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	target := filepath.Join(workspace, "README.md")
	var content strings.Builder
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&content, "line %02d with enough content to wrap naturally in the preview viewport\n", i)
	}
	if err := os.WriteFile(target, []byte(content.String()), 0644); err != nil {
		t.Fatalf("write preview target: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}

	m := newTestModel()
	m.handleResize(100, 24)
	m.previewPanel = buildPreviewPanelStateForPath(target, 0)
	if m.previewPanel == nil {
		t.Fatal("expected preview state")
	}
	m.syncPreviewViewport(true)
	start := m.previewPanel.viewport.YOffset()

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(Model)
	if got := m.previewPanel.viewport.YOffset(); got <= start {
		t.Fatalf("expected down key to scroll preview, start=%d got=%d", start, got)
	}

	start = m.previewPanel.viewport.YOffset()
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = next.(Model)
	if got := m.previewPanel.viewport.YOffset(); got <= start {
		t.Fatalf("expected page down to scroll preview further, start=%d got=%d", start, got)
	}

	start = m.previewPanel.viewport.YOffset()
	next, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m = next.(Model)
	if got := m.previewPanel.viewport.YOffset(); got <= start {
		t.Fatalf("expected mouse wheel to scroll preview, start=%d got=%d", start, got)
	}
}

func TestMarkdownPreviewStaysRaw(t *testing.T) {
	workspace := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	target := filepath.Join(workspace, "README.md")
	if err := os.WriteFile(target, []byte("# Title\n\n- item one\n- item two\n"), 0644); err != nil {
		t.Fatalf("write markdown preview target: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}

	m := newTestModel()
	m.handleResize(100, 24)
	m.previewPanel = buildPreviewPanelStateForPath(target, 0)
	if m.previewPanel == nil {
		t.Fatal("expected markdown preview state")
	}
	m.syncPreviewViewport(true)
	rendered := m.previewPanel.previewContent(m.previewContentWidth())
	if rendered == m.previewPanel.Content {
		t.Fatalf("expected markdown preview to render markdown, got raw content %q", rendered)
	}
	if !strings.Contains(rendered, "Title") {
		t.Fatalf("expected rendered markdown preview to contain title text, got %q", rendered)
	}
}

func TestSourcePreviewUsesSyntaxHighlighting(t *testing.T) {
	workspace := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	target := filepath.Join(workspace, "main.go")
	raw := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	if err := os.WriteFile(target, []byte(raw), 0644); err != nil {
		t.Fatalf("write source preview target: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}

	m := newTestModel()
	m.handleResize(100, 24)
	m.previewPanel = buildPreviewPanelStateForPath(target, 0)
	if m.previewPanel == nil {
		t.Fatal("expected source preview state")
	}
	m.syncPreviewViewport(true)
	rendered := m.previewPanel.previewContent(m.previewContentWidth())
	if rendered == raw {
		t.Fatalf("expected source preview to be syntax highlighted, got raw content %q", rendered)
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected highlighted preview to contain ANSI color sequences, got %q", rendered)
	}
}

func TestPreviewWrapsLongLinesToViewportWidth(t *testing.T) {
	workspace := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	target := filepath.Join(workspace, "main.go")
	raw := "package main\n\nvar value = \"this_is_a_very_long_preview_line_that_should_wrap_inside_the_preview_pane_instead_of_overflowing_horizontally\"\n"
	if err := os.WriteFile(target, []byte(raw), 0644); err != nil {
		t.Fatalf("write source preview target: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}

	m := newTestModel()
	m.handleResize(80, 24)
	m.previewPanel = buildPreviewPanelStateForPath(target, 0)
	if m.previewPanel == nil {
		t.Fatal("expected preview state")
	}
	m.syncPreviewViewport(true)
	rendered := m.previewPanel.previewContent(24)
	if !strings.Contains(rendered, "\n") {
		t.Fatalf("expected wrapped preview output, got %q", rendered)
	}
}

func TestEscClosesPreviewPanel(t *testing.T) {
	m := newTestModel()
	m.previewPanel = &previewPanelState{DisplayPath: "README.md", Content: "hello"}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected escape to close preview without command")
	}
	m = next.(Model)

	if m.previewPanel != nil {
		t.Fatal("expected preview panel to close on escape")
	}
}

func TestEscClosesPreviewPanelBeforeCancelingRun(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.previewPanel = &previewPanelState{DisplayPath: "README.md", Content: "hello"}
	canceled := false
	m.cancelFunc = func() { canceled = true }

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected escape to close preview without command")
	}
	m = next.(Model)

	if m.previewPanel != nil {
		t.Fatal("expected preview panel to close before canceling run")
	}
	if canceled {
		t.Fatal("expected active run not to be canceled while closing preview panel")
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

	output := stripAnsi(renderedOutput(&m))
	hasRead := strings.Contains(output, "Read")
	hasSearch := strings.Contains(output, "Search") || strings.Contains(output, "Grep")
	if !hasRead && !hasSearch {
		t.Fatalf("expected tool activity in output, got %q", output)
	}
}

func TestTodoWriteMovesTaskTrackingToSidebar(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true
	raw := `{"todos":[{"id":"todo-1","content":"Polish TUI activity flow","status":"in_progress"},{"id":"todo-2","content":"Refresh docs","status":"pending"}]}`

	m.startToolActivity(ToolStatusMsg{ToolName: "todo_write", DisplayName: "Update todos", Running: true, RawArgs: raw})
	m.finishToolActivity(ToolStatusMsg{ToolName: "todo_write", DisplayName: "Update todos", Running: false, RawArgs: raw})
	m.startToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "internal/tui/view.go", Running: true})
	m.finishToolActivity(ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "internal/tui/view.go", Running: false, Result: "line1\nline2"})

	output := renderedOutput(&m)

	if strings.Contains(output, "Todo:") || strings.Contains(output, "🎯") {
		t.Fatalf("expected main content to omit todo heading, got %q", output)
	}
	if strings.Contains(output, "Advancing tasks") || strings.Contains(output, "推进任务") {
		t.Fatalf("expected main content to omit task tracker groups, got %q", output)
	}
	if !strings.Contains(stripAnsi(output), "Read") && !strings.Contains(stripAnsi(output), "read_file") {
		t.Fatalf("expected following tool work to render (either grouped or as individual items), got %q", output)
	}
	sidebar := m.renderSidebar()
	if !strings.Contains(sidebar, "Polish TUI activity flow") {
		t.Fatalf("expected active task in sidebar tracker, got %q", sidebar)
	}
}

func TestSidebarTaskTrackerPreservesOriginalOrder(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	now := time.Now()
	m.todoSnapshot = map[string]todoStateItem{
		"todo-1": {ID: "todo-1", Content: "First task", Status: "pending", UpdatedAt: now},
		"todo-2": {ID: "todo-2", Content: "Second task", Status: "in_progress", StartedAt: now, UpdatedAt: now},
		"todo-3": {ID: "todo-3", Content: "Third task", Status: "done", StartedAt: now.Add(-time.Minute), UpdatedAt: now},
	}
	m.todoOrder = []string{"todo-1", "todo-2", "todo-3"}

	sidebar := m.renderSidebar()
	idx1 := strings.Index(sidebar, "First task")
	idx2 := strings.Index(sidebar, "Second task")
	idx3 := strings.Index(sidebar, "Third task")
	if idx1 == -1 || idx2 == -1 || idx3 == -1 {
		t.Fatalf("expected all tasks in sidebar, got %q", sidebar)
	}
	if !(idx1 < idx2 && idx2 < idx3) {
		t.Fatalf("expected original order [First, Second, Third], got indices %d, %d, %d", idx1, idx2, idx3)
	}
	if strings.Contains(sidebar, "供应商") || strings.Contains(sidebar, "vendor") {
		t.Fatalf("expected task tracker to replace default sidebar details, got %q", sidebar)
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

	// Verify chatList has all 7 tool items with correct params
	if m.chatList == nil || m.chatList.Len() != 7 {
		t.Fatalf("expected 7 chatList items, got %d", m.chatList.Len())
	}
	for i := 0; i < 7; i++ {
		item := m.chatList.ItemAt(i)
		rendered := item.Render(m.conversationInnerWidth())
		if !strings.Contains(rendered, fmt.Sprintf("step-%d.md", i+1)) {
			t.Errorf("item %d: expected step-%d.md in render, got: %q", i, i+1, rendered)
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

	output := stripAnsi(m.renderGroupedActivities())

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
	t.Skip("TODO: subagent status rendering needs to be wired into chatList")
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

	output := renderedOutput(&m)

	if !strings.Contains(output, "🤖") || !strings.Contains(output, "Investigate parser behavior") {
		t.Fatalf("expected independent subagent state block, got %q", output)
	}
	if !strings.Contains(output, "Reading docs/spec.md") {
		t.Fatalf("expected friendly subagent activity summary, got %q", output)
	}
	// spawn_agent/wait_agent internal tool names must stay hidden,
	// but the /agent prefix + ID is intentionally shown as a user-visible label.
	if strings.Contains(output, "spawn_agent") || strings.Contains(output, "wait_agent") {
		t.Fatalf("expected subagent lifecycle internals to stay hidden, got %q", output)
	}
}

func TestRenderOutputShowsSubAgentProgressSummary(t *testing.T) {
	t.Skip("TODO: subagent status rendering needs to be wired into chatList")
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

	output := renderedOutput(&m)

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

	output := renderedOutput(&m)

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

	output := stripAnsi(renderedOutput(&m))
	if !strings.Contains(output, "Read") {
		t.Fatalf("expected tool activity in output, got %q", output)
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

	output := stripAnsi(renderedOutput(&m))
	if !strings.Contains(output, "Read") {
		t.Fatalf("expected tool activity to appear in output, got %q", output)
	}
}

func TestTrimLeadingRenderedSpacing(t *testing.T) {
	trimmed := trimLeadingRenderedSpacing("\n\n  Hello\n")
	if trimmed != "Hello\n" {
		t.Fatalf("expected leading rendered spacing to be trimmed, got %q", trimmed)
	}
}

func TestNormalizeTerminalMarkdownAnnotatesBareCodeFencesAsText(t *testing.T) {
	input := "## Tree\n\n```\nroot/\n├── child\n└── leaf\n```\n"
	normalized := markdown.Normalize(input)
	if !strings.Contains(normalized, "```text\nroot/") {
		t.Fatalf("expected bare markdown fence to be annotated as text, got %q", normalized)
	}
}

func TestMarkdownStyleConfigUsesCalmerCodeColors(t *testing.T) {
	dark := markdown.StyleConfigForDarkMode(true)
	if dark.Code.BackgroundColor != nil {
		t.Fatalf("expected dark markdown inline code to avoid background fills, got %#v", dark.Code.BackgroundColor)
	}
	if dark.Code.Color == nil || *dark.Code.Color == "#ff5555" || *dark.Code.Color == "203" {
		t.Fatalf("expected dark markdown inline code to avoid the old red palette, got %#v", dark.Code.Color)
	}
	if dark.CodeBlock.Chroma == nil || dark.CodeBlock.Chroma.Punctuation.Color == nil || *dark.CodeBlock.Chroma.Punctuation.Color != "#a9b1d6" {
		t.Fatalf("expected dark markdown punctuation to use the calmer text color, got %#v", dark.CodeBlock.Chroma)
	}

	light := markdown.StyleConfigForDarkMode(false)
	if light.Code.BackgroundColor != nil {
		t.Fatalf("expected light markdown inline code to avoid background fills, got %#v", light.Code.BackgroundColor)
	}
	if light.Code.Color == nil || *light.Code.Color != "#005f87" {
		t.Fatalf("expected light markdown inline code to use the calmer blue accent, got %#v", light.Code.Color)
	}
}

func TestRenderOutputKeepsWaitingIndicatorOutOfConversationPane(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.statusActivity = "Thinking..."
	m.spinner.Start("Thinking...")

	output := renderedOutput(&m)
	if strings.Contains(output, "Thinking...") {
		t.Fatalf("expected waiting indicator to stay in status bar only, got %q", output)
	}
}

func TestAgentStreamMsgRendersMarkdownLive(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 32)
	m.loading = true
	m.activeAgentRunID = 1
	m.streamBuffer = &bytes.Buffer{}

	next, _ := m.Update(agentStreamMsg{
		RunID: 1,
		Text:  "### Streaming title\n\nUse `foo` now.",
	})
	updated := next.(Model)

	// Verify streaming text appears in rendered output (chatList or legacy)
	got := renderedOutput(&updated)
	if !strings.Contains(stripAnsi(got), "Streaming title") {
		t.Fatalf("expected streaming title in output, got %q", stripAnsi(got))
	}
}

func TestToolBoundaryFlushPreservesRenderedMarkdown(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 32)
	m.loading = true
	m.activeAgentRunID = 1
	m.streamBuffer = &bytes.Buffer{}

	next, _ := m.Update(agentStreamMsg{
		RunID: 1,
		Text:  "### Partial title",
	})
	m = next.(Model)

	next, cmd := m.Update(agentToolStatusMsg{
		RunID: 1,
		ToolStatusMsg: ToolStatusMsg{
			ToolID:      "tool-1",
			ToolName:    "bash",
			DisplayName: "Run",
			Detail:      "first",
			Running:     true,
		},
	})
	if cmd == nil {
		t.Fatal("expected tool start to schedule spinner work")
	}
	m = next.(Model)

	// Verify both the streaming text and tool status appear in rendered output
	got := stripAnsi(renderedOutput(&m))
	if !strings.Contains(got, "Partial title") {
		t.Fatalf("expected partial title preserved in output, got %q", got)
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

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	// Verify help text was written to chatList
	if m.chatList == nil || m.chatList.Len() == 0 {
		t.Fatal("expected chatList to have help text")
	}
	item := m.chatList.ItemAt(0)
	rendered := stripAnsi(item.Render(120))
	if !strings.Contains(rendered, "Available commands:") {
		t.Errorf("expected help text in chatList item, got %q", rendered[:min(100, len(rendered))])
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

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	if cmd != nil {
		t.Error("expected mention autocomplete to only update input")
	}
	if m.input.Value() != "@README.md " {
		t.Errorf("expected mention completion in input, got %q", m.input.Value())
	}
	if m.chatList != nil && m.chatList.Len() != 0 {
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
	if m.chatList != nil && m.chatList.Len() != 0 {
		t.Errorf("expected mode switch not to write output, got %q", renderedOutput(&m))
	}
}

func TestModeCommandSetDoesNotWriteToOutput(t *testing.T) {
	m := newTestModel()

	cmd := m.handleModeCommand([]string{"/mode", "auto"})
	if cmd != nil {
		t.Error("expected no command from /mode set")
	}
	if m.chatList != nil && m.chatList.Len() != 0 {
		t.Errorf("expected /mode set not to write output, got %q", renderedOutput(&m))
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
	if m.chatList != nil && m.chatList.Len() != 0 {
		t.Errorf("expected no output on successful mode persistence, got %q", renderedOutput(&m))
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
	if m.chatList != nil && m.chatList.Len() != 0 {
		t.Errorf("expected no output on successful /mode persistence, got %q", renderedOutput(&m))
	}
}

func TestFinishToolActivityMatchesParallelCompletionsByToolID(t *testing.T) {
	m := newTestModel()

	m.startToolActivity(ToolStatusMsg{ToolID: "tool-1", ToolName: "bash", DisplayName: "Run", Detail: "cmd-1", Running: true, RawArgs: `{"command":"echo first"}`})
	m.startToolActivity(ToolStatusMsg{ToolID: "tool-2", ToolName: "bash", DisplayName: "Run", Detail: "cmd-2", Running: true, RawArgs: `{"command":"echo second"}`})

	m.finishToolActivity(ToolStatusMsg{ToolID: "tool-1", ToolName: "bash", DisplayName: "Run", Detail: "cmd-1", Running: false, Result: "first output", RawArgs: `{"command":"echo first"}`})

	if len(m.activityGroups) != 1 || len(m.activityGroups[0].Items) != 2 {
		t.Fatalf("expected one activity group with two items, got %#v", m.activityGroups)
	}
	first := m.activityGroups[0].Items[0]
	second := m.activityGroups[0].Items[1]
	if first.Running {
		t.Fatal("expected first tool item to be marked complete")
	}
	if len(first.OutputLines) == 0 || first.OutputLines[0] != "first output" {
		t.Fatalf("expected first tool output to be attached to the matching item, got %#v", first.OutputLines)
	}
	if !second.Running {
		t.Fatal("expected second tool item to remain running")
	}
}

func TestAgentToolStatusMsgCountsToolsInsideEventLoop(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 1

	next, cmd := m.Update(agentToolStatusMsg{
		RunID: 1,
		ToolStatusMsg: ToolStatusMsg{
			ToolID:      "tool-1",
			ToolName:    "bash",
			DisplayName: "Run",
			Detail:      "first",
			Running:     true,
		},
	})
	if cmd == nil {
		t.Fatal("expected tool start to schedule spinner work")
	}
	m = next.(Model)

	next, cmd = m.Update(agentToolStatusMsg{
		RunID: 1,
		ToolStatusMsg: ToolStatusMsg{
			ToolID:      "tool-2",
			ToolName:    "bash",
			DisplayName: "Run",
			Detail:      "second",
			Running:     true,
		},
	})
	if cmd == nil {
		t.Fatal("expected second tool start to schedule spinner work")
	}
	m = next.(Model)

	if m.statusToolCount != 2 {
		t.Fatalf("expected tool count 2 after two live tool starts, got %d", m.statusToolCount)
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

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m = next.(Model)
	if _, ok := m.pendingQuestionnaire.answers[0].selected["frontend"]; !ok {
		t.Fatal("expected space to select the highlighted choice")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.pendingQuestionnaire.tabIndex != 1 {
		t.Fatalf("expected enter to advance to next question, got tab %d", m.pendingQuestionnaire.tabIndex)
	}

	m.pendingQuestionnaire.input.SetValue("Release safety first.")

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if !m.pendingQuestionnaire.onSubmitTab() {
		t.Fatal("expected enter on last question to move to submit tab")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
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

func TestIMPairingRenderedInContextPanel(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 32)
	imMgr := im.NewManager()
	if err := imMgr.SetBindingStore(im.NewMemoryBindingStore()); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	if err := imMgr.SetPairingStore(im.NewMemoryPairingStore()); err != nil {
		t.Fatalf("SetPairingStore returned error: %v", err)
	}
	m.SetIMManager(imMgr)
	m.SetSession(&session.Session{ID: "session-1", Workspace: "/tmp/project"}, nil)
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform: im.PlatformQQ,
		Adapter:  "qq",
		TargetID: "ops",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	if _, err := imMgr.HandlePairingInbound(im.InboundMessage{
		Envelope: im.Envelope{
			Adapter:    "qq",
			Platform:   im.PlatformQQ,
			ChannelID:  "group-9",
			SenderID:   "user-9",
			MessageID:  "msg-9",
			ReceivedAt: time.Now(),
		},
		Text: "bind me",
	}); err != nil {
		t.Fatalf("HandlePairingInbound returned error: %v", err)
	}

	result := m.renderContextPanel()

	if !strings.Contains(result, "QQ pairing") && !strings.Contains(result, "QQ 绑定验证") {
		t.Fatal("expected IM pairing panel title")
	}
	if !strings.Contains(result, "group-9") {
		t.Fatal("expected pairing panel to show request channel")
	}
	if !strings.Contains(result, "Esc") {
		t.Fatal("expected pairing panel reject hint")
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

	output := renderedOutput(&m)
	if !strings.Contains(output, "消息格式不兼容") {
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

	output := renderedOutput(&m)
	// UserFacingError returns a generic Chinese message for unrecognized errors.
	// Verify that internal "chat error:" prefix is stripped from the output.
	if strings.Contains(output, "chat error:") {
		t.Fatalf("expected internal chat error prefix to be removed, got %q", output)
	}
	if !strings.Contains(output, "请求失败") {
		t.Fatalf("expected generic failure message, got %q", output)
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

	view := m.View().Content
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
	msg := tea.KeyPressMsg{Text: "ctrl+c"}
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
	if !strings.Contains(renderedOutput(&m2), "Press Ctrl-C again to exit.") {
		t.Error("expected exit confirmation prompt in output")
	}
}

func TestUpdateKeyMsgCtrlCQuitsOnSecondPress(t *testing.T) {
	m := newTestModel()
	m.exitConfirmPending = true
	msg := tea.KeyPressMsg{Text: "ctrl+c"}
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
	msg := tea.KeyPressMsg{Code: tea.KeyEnter}
	model, cmd := m.Update(msg)
	m2 := model.(Model)
	if m2.quitting {
		t.Error("should not quit on empty enter")
	}
	if cmd != nil {
		t.Error("expected nil cmd for empty enter")
	}
}

func TestStartupInputGraceWindowDropsEarlyKeyboardRunes(t *testing.T) {
	// Keyboard input is intentionally NOT suppressed during the startup gate
	// window. Only mouse events are suppressed. Suppressing keys caused
	// character swallowing reported by users, so early keypresses now pass
	// through to the input field.
	// Real keyboard input sends one character per KeyPressMsg.
	m := newTestModel()
	m.startedAt = time.Now()

	for _, ch := range []string{"h", "e", "l", "l", "o"} {
		model, _ := m.Update(tea.KeyPressMsg{Text: ch})
		m = model.(Model)
	}

	if m.input.Value() != "hello" {
		t.Fatalf("expected early startup key input to be preserved, got %q", m.input.Value())
	}
}

func TestCtrlCCancelsAutocomplete(t *testing.T) {
	m := newTestModel()
	m.autoCompleteActive = true
	m.autoCompleteKind = "slash"
	m.autoCompleteItems = []string{"/help"}
	m.input.SetValue("/he")

	model, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
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

	model, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
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
	if !strings.Contains(renderedOutput(&m2), "[interrupted]") {
		t.Error("expected interrupted marker in output")
	}
}

func TestEscLoadingCancelsCurrentActivity(t *testing.T) {
	m := newTestModel()
	cancelled := false
	m.loading = true
	m.cancelFunc = func() { cancelled = true }

	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
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
	if !strings.Contains(renderedOutput(&m2), "[interrupted]") {
		t.Error("expected interrupted marker in output after esc")
	}
}

func TestCtrlCWhileAlreadyCancellingDoesNotDuplicateInterruptOutput(t *testing.T) {
	m := newTestModel()
	cancelCount := 0
	m.loading = true
	m.runCanceled = true
	m.cancelFunc = func() { cancelCount++ }
	m.chatWriteSystem(nextSystemID(), "[interrupted]")

	model, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	m2 := model.(Model)

	if cmd != nil {
		t.Error("expected no command when already cancelling active work")
	}
	if cancelCount != 0 {
		t.Errorf("expected cancel func not to be called again, got %d", cancelCount)
	}
	if strings.Count(stripAnsi(renderedOutput(&m2)), "[interrupted]") != 1 {
		t.Fatalf("expected interrupted marker not to duplicate, got %q", renderedOutput(&m2))
	}
}

func TestLoadingAllowsTypingAndQueuesSubmission(t *testing.T) {
	m := newTestModel()
	m.loading = true

	model, _ := m.Update(tea.KeyPressMsg{Text: "h"})
	m = model.(Model)
	model, _ = m.Update(tea.KeyPressMsg{Text: "i"})
	m = model.(Model)

	if m.input.Value() != "hi" {
		t.Fatalf("expected input to remain editable while loading, got %q", m.input.Value())
	}

	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = model.(Model)

	if cmd != nil {
		t.Error("expected queued submission not to start a new agent immediately")
	}
	if len(m.pending.items) != 1 || m.pending.items[0] != "hi" {
		t.Fatalf("expected one queued submission, got %#v", m.pending.items)
	}
	if m.input.Value() != "" {
		t.Errorf("expected input to clear after queuing, got %q", m.input.Value())
	}
	// User input should be rendered in the conversation view like a normal submission.
	// The prefix "❯ " is styled with ANSI codes, so we just check for the text content.
	outputStr := renderedOutput(&m)
	if !strings.Contains(outputStr, "hi") {
		t.Error("expected user input 'hi' to appear in output, got:", outputStr)
	}
	// Should NOT show the old "[queued N pending]" hint.
	if strings.Contains(outputStr, "[queued") {
		t.Error("should not show [queued...] hint, got:", outputStr)
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

	if !strings.Contains(renderedOutput(&m), "please switch direction") {
		t.Fatalf("expected delivered interrupt text in output, got %q", renderedOutput(&m))
	}
	if !strings.Contains(renderedOutput(&m), "[delivered to active run; revising plan]") {
		t.Fatalf("expected delivery marker in output, got %q", renderedOutput(&m))
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

	model, cmd := m.Update(tea.KeyPressMsg{Text: "$"})
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

	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = model.(Model)

	if cmd == nil {
		t.Fatal("expected shell command to start asynchronously")
	}
	if !m.loading {
		t.Fatal("expected shell command to enter loading state")
	}
	if !strings.Contains(stripAnsi(renderedOutput(&m)), "$ echo hi") {
		t.Fatalf("expected shell command to be echoed in output, got %q", renderedOutput(&m))
	}
}

func TestShellModeEscExitsShellMode(t *testing.T) {
	m := newTestModel()
	m.setShellMode(true)
	m.input.SetValue("echo hi")

	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
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

	model, _ := m.Update(tea.KeyPressMsg{Text: "h"})
	m = model.(Model)
	model, _ = m.Update(tea.KeyPressMsg{Text: "i"})
	m = model.(Model)
	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = model.(Model)

	if cmd != nil {
		t.Fatal("expected submission to queue while project memory is still loading")
	}
	if len(m.pending.items) != 1 || m.pending.items[0] != "hi" {
		t.Fatalf("expected queued submission, got %#v", m.pending.items)
	}
	if m.projectMemoryLoading != true {
		t.Fatal("expected project memory loading flag to remain set until async load completes")
	}
}

func TestProjectMemoryLoadedConsumesQueuedSubmissionAndInjectsMemory(t *testing.T) {
	ag := agent.NewAgent(nil, tool.NewRegistry(), "base", 1)
	m := NewModel(ag, permission.NewConfigPolicy(nil, nil))
	m.projectMemoryLoading = true
	m.pending.items = []string{"hello"}

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
	m.pending.items = []string{"first question", "second question"}
	m.streamBuffer = &bytes.Buffer{}

	model, cmd := m.Update(doneMsg{})
	m = model.(Model)

	if cmd == nil {
		t.Fatal("expected doneMsg to schedule next merged submission")
	}
	if !m.loading {
		t.Error("expected next merged submission to start immediately")
	}
	if len(m.pending.items) != 0 {
		t.Errorf("expected pending submissions to be consumed, got %#v", m.pending.items)
	}
	got := renderedOutput(&m)
	if !strings.Contains(got, "first question") || !strings.Contains(got, "second question") {
		t.Error("expected merged queued text to be submitted as one user message")
	}
}

func TestNextUserMessageStartsOnFreshLineAfterPartialOutput(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.chatWriteSystem(nextSystemID(), "partial tool output")

	m.handleCommand("follow-up")

	got := stripAnsi(renderedOutput(&m))
	if !strings.Contains(got, "follow-up") {
		t.Fatalf("expected follow-up message in output, got %q", got)
	}
}

func TestStreamReplyStartsAfterBlankLine(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.chatWriteSystem(nextSystemID(), "previous block\n")

	model, _ := m.Update(streamMsg("next reply"))
	m = model.(Model)

	got := stripAnsi(renderedOutput(&m))
	if !strings.Contains(got, "next reply") {
		t.Fatalf("expected stream reply in output, got %q", got)
	}
}

func TestCompactionStatusRendersOnOwnLine(t *testing.T) {
	m := newTestModel()
	m.streamBuffer = &bytes.Buffer{}

	m.appendStreamChunk("partial reply")
	m.appendStreamChunk("[compacting conversation to stay within context window]\n")

	got := stripAnsi(renderedOutput(&m))
	if !strings.Contains(got, "partial reply") || !strings.Contains(got, "[compacting conversation to stay within context window]") {
		t.Fatalf("expected compaction status in output, got %q", got)
	}
}

func TestCompactionStatusLocalizesInChinese(t *testing.T) {
	m := newTestModel()
	m.setLanguage("zh-CN")
	m.streamBuffer = &bytes.Buffer{}

	m.appendStreamChunk("[conversation compacted]\n")

	got := stripAnsi(renderedOutput(&m))
	if !strings.Contains(got, "会话已压缩") {
		t.Fatalf("expected localized compacted status, got %q", got)
	}
}

func TestCtrlCRestoresPendingMessagesToInput(t *testing.T) {
	m := newTestModel()
	cancelled := false
	m.loading = true
	m.cancelFunc = func() { cancelled = true }
	m.pending.items = []string{"first question", "second question"}
	m.input.SetValue("draft")

	model, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	m = model.(Model)

	if cmd != nil {
		t.Error("expected no command when cancelling active run")
	}
	if !cancelled {
		t.Error("expected cancel func to run")
	}
	// With textarea, pending items are joined with newlines.
	if got := m.input.Value(); !strings.Contains(got, "first question") || !strings.Contains(got, "second question") || !strings.Contains(got, "draft") {
		t.Fatalf("unexpected restored input: %q", got)
	}
	if len(m.pending.items) != 0 {
		t.Errorf("expected pending submissions to be cleared after restore, got %#v", m.pending.items)
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

	model, _ := m.Update(tea.KeyPressMsg{Text: "a"})
	m2 := model.(Model)

	if m2.exitConfirmPending {
		t.Error("expected exit confirmation to clear on other input")
	}
}

func TestExitConfirmStartsOnFreshLine(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.chatWriteSystem(nextSystemID(), "partial line")

	m.promptExitConfirm()

	got := stripAnsi(renderedOutput(&m))
	if !strings.Contains(got, "Press Ctrl-C again to exit.") {
		t.Fatalf("expected exit confirm in output, got %q", got)
	}
}

func TestResizeStillAllowsNormalRuneInput(t *testing.T) {
	m := newTestModel()
	m.handleResize(100, 30)

	model, _ := m.Update(tea.KeyPressMsg{Text: "a"})
	m2 := model.(Model)

	if m2.input.Value() != "a" {
		t.Errorf("expected normal input after resize, got %q", m2.input.Value())
	}
}

// --- renderOutput ---

func TestRenderOutputEmpty(t *testing.T) {
	m := newTestModel()
	result := renderedOutput(&m)
	if !strings.Contains(result, "Ask for a refactor") {
		t.Errorf("expected starter guidance, got: %q", result)
	}
}

func TestRenderOutputWithContent(t *testing.T) {
	m := newTestModel()
	m.chatWriteSystem(nextSystemID(), "Hello world")
	result := stripAnsi(renderedOutput(&m))
	if !strings.Contains(result, "Hello world") {
		t.Error("expected content in renderedOutput")
	}
}

func TestResetConversationViewClearsTransientState(t *testing.T) {
	m := newTestModel()
	m.chatWriteSystem(nextSystemID(), "Hello")
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

	if m.chatList != nil && m.chatList.Len() != 0 {
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
