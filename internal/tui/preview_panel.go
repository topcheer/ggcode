package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
)

type previewPanelState struct {
	DisplayPath string
	AbsPath     string
	TargetLine  int
	Content     string
	Error       string
	viewport    ViewportModel
	anchored    bool
	rendered    string
	renderWidth int
}

func (m *Model) closePreviewPanel() {
	m.previewPanel = nil
}

func buildPreviewPanelStateForPath(absPath string, targetLine int) *previewPanelState {
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		return nil
	}
	data, err := os.ReadFile(absPath)
	displayPath := displayPreviewPath("", absPath)
	if err != nil {
		state := &previewPanelState{
			DisplayPath: displayPath,
			AbsPath:     absPath,
			TargetLine:  targetLine,
			Error:       err.Error(),
		}
		state.viewport = newPreviewViewport()
		return state
	}
	if isBinaryPreviewData(data) {
		state := &previewPanelState{
			DisplayPath: displayPath,
			AbsPath:     absPath,
			TargetLine:  targetLine,
			Error:       previewText(LangEnglish, "binary"),
		}
		state.viewport = newPreviewViewport()
		return state
	}
	state := &previewPanelState{
		DisplayPath: displayPath,
		AbsPath:     absPath,
		TargetLine:  targetLine,
		Content:     strings.ReplaceAll(string(data), "\r\n", "\n"),
	}
	state.viewport = newPreviewViewport()
	return state
}

func (p *previewPanelState) previewContent(width int) string {
	if p == nil {
		return ""
	}
	if p.Error != "" {
		return p.Error
	}
	if p.rendered != "" && p.renderWidth == width {
		return p.rendered
	}
	rendered := p.Content
	switch {
	case isMarkdownPreviewPath(p.DisplayPath) || isMarkdownPreviewPath(p.AbsPath):
		rendered = trimLeadingRenderedSpacing(RenderMarkdownWidth(p.Content, max(20, width)))
	case shouldSyntaxHighlightPreviewPath(p.DisplayPath) || shouldSyntaxHighlightPreviewPath(p.AbsPath):
		out, ok := renderHighlightedPreview(p.DisplayPath, wrapPreviewText(p.Content, width))
		if ok {
			rendered = out
		} else {
			rendered = wrapPreviewText(p.Content, width)
		}
	default:
		rendered = wrapPreviewText(p.Content, width)
	}
	p.renderWidth = width
	p.rendered = rendered
	return p.rendered
}

func wrapPreviewText(text string, width int) string {
	return strings.Join(wrapConversationText(text, max(1, width)), "\n")
}

func isMarkdownPreviewPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown", ".mdx":
		return true
	default:
		return false
	}
}

func shouldSyntaxHighlightPreviewPath(path string) bool {
	if isMarkdownPreviewPath(path) {
		return false
	}
	base := filepath.Base(path)
	switch {
	case base == "Dockerfile", base == "Containerfile", base == ".env", strings.HasPrefix(base, ".env."):
		return true
	}
	if lexers.Match(path) != nil {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".rs", ".c", ".cc", ".cp", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".m", ".mm",
		".lua", ".swift", ".tf", ".tfvars", ".hcl", ".zig", ".java", ".mjs", ".cjs", ".jsonc",
		".ksh", ".cs", ".csproj", ".sln", ".slnx", ".txt", ".conf":
		return true
	default:
		return false
	}
}

func renderHighlightedPreview(path, content string) (string, bool) {
	lexer := lexers.Match(path)
	if lexer == nil {
		lexer = lexers.Analyse(content)
	}
	if lexer == nil {
		return "", false
	}
	lexer = chroma.Coalesce(lexer)
	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return "", false
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return "", false
	}
	style := chromastyles.Get("dracula")
	if style == nil {
		style = chromastyles.Fallback
	}
	var buf strings.Builder
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return "", false
	}
	return strings.TrimRight(buf.String(), "\n"), true
}

func displayPreviewPath(rawPath, absPath string) string {
	if rawPath != "" {
		return filepath.ToSlash(rawPath)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.ToSlash(absPath)
	}
	if rel, err := filepath.Rel(cwd, absPath); err == nil && rel != "" && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(absPath)
}

func (m Model) renderPreviewPanel() string {
	if m.previewPanel == nil {
		return ""
	}
	state := m.previewPanel
	meta := fmt.Sprintf("%s %s", previewText(m.currentLanguage(), "path"), state.DisplayPath)
	if state.TargetLine > 0 {
		meta += fmt.Sprintf("  •  %s %d", previewText(m.currentLanguage(), "line"), state.TargetLine)
	}
	if scroll := state.viewport.ScrollIndicatorStyle(); scroll != "" {
		meta += "  •  " + scroll
	}
	title := lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true).Render(previewText(m.currentLanguage(), "title"))
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(previewText(m.currentLanguage(), "hint"))
	content := strings.Join([]string{
		title,
		truncateDisplayWidth(meta, max(12, m.previewContentWidth())),
		state.viewport.View().Content,
		footer,
	}, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Width(max(1, m.viewWidth()-m.terminalRightMargin())).
		Height(max(6, m.viewHeight())).
		Render(content)
}

func newPreviewViewport() ViewportModel {
	vp := NewViewportModel(1, 1)
	vp.autoFollow = false
	return vp
}

func (m *Model) syncPreviewViewport(initial bool) {
	if m.previewPanel == nil {
		return
	}
	state := m.previewPanel
	width := m.previewContentWidth()
	height := m.previewContentHeight()
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	oldOffset := state.viewport.YOffset()
	state.viewport.autoFollow = false
	state.viewport.SetSize(width, height)
	content := state.previewContent(width)
	state.viewport.SetContent(content)
	maxOffset := max(0, state.viewport.TotalLineCount()-state.viewport.VisibleLineCount())
	switch {
	case initial && state.TargetLine > 1 && !state.anchored:
		offset := min(maxOffset, state.TargetLine-1)
		state.viewport.vp.SetYOffset(offset)
		state.anchored = true
	default:
		state.viewport.vp.SetYOffset(min(maxOffset, oldOffset))
	}
}

func (m Model) previewContentWidth() int {
	width := m.viewWidth() - m.terminalRightMargin() - 4
	if width < 1 {
		return 1
	}
	return width
}

func (m Model) previewContentHeight() int {
	height := m.viewHeight() - 5
	if height < 3 {
		return 3
	}
	return height
}

func (m *Model) handlePreviewMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if m.previewPanel == nil {
		return *m, nil
	}
	mouse := msg.Mouse()
	if mouse.Mod.Contains(tea.ModAlt) {
		return *m, nil
	}
	switch mouse.Button {
	case tea.MouseWheelUp:
		m.previewPanel.viewport.ScrollUp(3)
	case tea.MouseWheelDown:
		m.previewPanel.viewport.ScrollDown(3)
	}
	return *m, nil
}

func (m *Model) handlePreviewKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.previewPanel == nil {
		return *m, nil
	}
	switch msg.String() {
	case "esc":
		m.closePreviewPanel()
	case "up", "k":
		m.previewPanel.viewport.ScrollUp(1)
	case "down", "j":
		m.previewPanel.viewport.ScrollDown(1)
	case "pgup":
		m.previewPanel.viewport.ScrollUp(max(1, m.previewPanel.viewport.VisibleLineCount()/2))
	case "pgdown":
		m.previewPanel.viewport.ScrollDown(max(1, m.previewPanel.viewport.VisibleLineCount()/2))
	}
	return *m, nil
}

func previewText(lang Language, key string) string {
	switch lang {
	case LangZhCN:
		switch key {
		case "title":
			return "文件预览"
		case "path":
			return "路径:"
		case "line":
			return "定位行:"
		case "hint":
			return "Esc 关闭 • 点击其它路径可切换预览"
		case "binary":
			return "二进制文件不支持预览"
		}
	default:
		switch key {
		case "title":
			return "File preview"
		case "path":
			return "path:"
		case "line":
			return "line:"
		case "hint":
			return "Esc closes • click another path to switch preview"
		case "binary":
			return "Binary files cannot be previewed"
		}
	}
	return key
}
