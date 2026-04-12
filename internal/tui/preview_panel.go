package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	previewTokenPattern = regexp.MustCompile(`(?:~/|/|\.\.?/)?(?:[A-Za-z0-9._-]+/)*[A-Za-z0-9._-]+\.[A-Za-z0-9._-]+(?::\d+(?::\d+)?)?`)
	ansiEscapePattern   = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
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

func (m *Model) handlePreviewClick(msg tea.MouseMsg) bool {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return false
	}
	token, ok := m.previewTokenAt(msg.X, msg.Y)
	if !ok {
		return false
	}
	return m.openPreviewForToken(token)
}

func (m Model) previewTokenAt(mouseX, mouseY int) (string, bool) {
	if mouseX < 0 || mouseY < 0 {
		return "", false
	}
	lines := visibleViewportLines(m.View())
	return previewTokenAtLine(lines, mouseY, mouseX)
}

func previewTokenAtLine(lines []string, y, mouseX int) (string, bool) {
	if y < 0 || y >= len(lines) {
		return "", false
	}
	line := lines[y]
	for _, match := range previewTokenPattern.FindAllStringIndex(line, -1) {
		start := lipgloss.Width(line[:match[0]])
		end := lipgloss.Width(line[:match[1]])
		if mouseX >= start && mouseX < end {
			return line[match[0]:match[1]], true
		}
	}
	return "", false
}

func visibleViewportLines(rendered string) []string {
	plain := ansiEscapePattern.ReplaceAllString(rendered, "")
	plain = strings.TrimRight(plain, "\n")
	if plain == "" {
		return nil
	}
	return strings.Split(plain, "\n")
}

func (m *Model) openPreviewForToken(token string) bool {
	state := m.buildPreviewPanelState(token)
	if state == nil {
		return false
	}
	m.previewPanel = state
	m.syncPreviewViewport(true)
	return true
}

func (m Model) buildPreviewPanelState(token string) *previewPanelState {
	pathPart, targetLine := parsePreviewTarget(token)
	if pathPart == "" {
		return nil
	}
	absPath, ok := resolvePreviewPath(pathPart)
	if !ok {
		return nil
	}
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		return nil
	}
	data, err := os.ReadFile(absPath)
	displayPath := displayPreviewPath(pathPart, absPath)
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
	if !isMarkdownPreviewPath(p.DisplayPath) && !isMarkdownPreviewPath(p.AbsPath) {
		return p.Content
	}
	if p.rendered != "" && p.renderWidth == width {
		return p.rendered
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return p.Content
	}
	rendered, err := renderer.Render(p.Content)
	if err != nil {
		return p.Content
	}
	p.renderWidth = width
	p.rendered = trimLeadingRenderedSpacing(rendered)
	return p.rendered
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

func parsePreviewTarget(token string) (string, int) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", 0
	}
	match := previewTokenPattern.FindString(token)
	if match == "" {
		return "", 0
	}
	parts := strings.Split(match, ":")
	if len(parts) >= 3 {
		if _, colErr := strconv.Atoi(parts[len(parts)-1]); colErr == nil {
			if line, lineErr := strconv.Atoi(parts[len(parts)-2]); lineErr == nil {
				return strings.Join(parts[:len(parts)-2], ":"), line
			}
		}
	}
	if len(parts) >= 2 {
		if line, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			return strings.Join(parts[:len(parts)-1], ":"), line
		}
	}
	return match, 0
}

func resolvePreviewPath(pathPart string) (string, bool) {
	if pathPart == "" {
		return "", false
	}
	if strings.HasPrefix(pathPart, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		pathPart = filepath.Join(home, pathPart[2:])
	}
	if filepath.IsAbs(pathPart) {
		return filepath.Clean(pathPart), true
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return filepath.Clean(filepath.Join(cwd, pathPart)), true
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
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(previewText(m.currentLanguage(), "hint_fullscreen"))
	content := strings.Join([]string{
		title,
		truncateDisplayWidth(meta, max(12, m.previewContentWidth())),
		state.viewport.View(),
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

func (m Model) decoratePreviewTargets(rendered string) string {
	if rendered == "" {
		return rendered
	}
	linkStyle := lipgloss.NewStyle().Underline(true).Foreground(lipgloss.Color("81"))
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = previewTokenPattern.ReplaceAllStringFunc(line, func(token string) string {
			pathPart, _ := parsePreviewTarget(token)
			if pathPart == "" {
				return token
			}
			if !previewPathExists(pathPart) {
				return token
			}
			return linkStyle.Render(token)
		})
	}
	return strings.Join(lines, "\n")
}

func previewPathExists(pathPart string) bool {
	absPath, ok := resolvePreviewPath(pathPart)
	if !ok {
		return false
	}
	info, err := os.Stat(absPath)
	return err == nil && !info.IsDir()
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
		state.viewport.vp.YOffset = offset
		state.anchored = true
	default:
		state.viewport.vp.YOffset = min(maxOffset, oldOffset)
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
	if msg.Alt {
		return *m, nil
	}
	switch msg.Type {
	case tea.MouseWheelUp:
		m.previewPanel.viewport.ScrollUp(3)
	case tea.MouseWheelDown:
		m.previewPanel.viewport.ScrollDown(3)
	}
	return *m, nil
}

func (m *Model) handlePreviewKey(msg tea.KeyMsg) (Model, tea.Cmd) {
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
		case "hint_fullscreen":
			return "Esc 关闭 • 鼠标滚轮 / ↑↓ / PgUp / PgDn 滚动"
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
		case "hint_fullscreen":
			return "Esc closes • mouse wheel / ↑↓ / PgUp / PgDn scroll"
		}
	}
	return key
}
