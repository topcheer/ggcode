package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const previewContextRadius = 6

var (
	previewTokenPattern = regexp.MustCompile(`(?:~/|/|\.\.?/)?(?:[A-Za-z0-9._-]+/)*[A-Za-z0-9._-]+\.[A-Za-z0-9._-]+(?::\d+(?::\d+)?)?`)
	ansiEscapePattern   = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
)

type previewPanelState struct {
	DisplayPath string
	AbsPath     string
	TargetLine  int
	StartLine   int
	EndLine     int
	TotalLines  int
	Lines       []string
	Error       string
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
	contentX, contentY := m.conversationContentOrigin()
	localX := mouseX - contentX
	localY := mouseY - contentY
	if localX < 0 || localY < 0 || localX >= m.conversationInnerWidth() {
		return "", false
	}
	lines := visibleViewportLines(m.conversationViewport().View())
	if localY >= len(lines) {
		return "", false
	}
	line := lines[localY]
	for _, match := range previewTokenPattern.FindAllStringIndex(line, -1) {
		start := lipgloss.Width(line[:match[0]])
		end := lipgloss.Width(line[:match[1]])
		if localX >= start && localX < end {
			return line[match[0]:match[1]], true
		}
	}
	return "", false
}

func (m Model) conversationContentOrigin() (int, int) {
	header := ""
	if m.topHeaderEnabled() {
		header = m.renderHeader()
	}
	top := lipgloss.Height(header) + lipgloss.Height(m.renderStartupBanner())
	return 2, top + 1
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
		return &previewPanelState{
			DisplayPath: displayPath,
			AbsPath:     absPath,
			TargetLine:  targetLine,
			Error:       err.Error(),
		}
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start, end := previewSnippetWindow(len(lines), targetLine)
	return &previewPanelState{
		DisplayPath: displayPath,
		AbsPath:     absPath,
		TargetLine:  targetLine,
		StartLine:   start,
		EndLine:     end,
		TotalLines:  len(lines),
		Lines:       append([]string(nil), lines[start-1:end]...),
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

func previewSnippetWindow(totalLines, targetLine int) (int, int) {
	if totalLines <= 0 {
		return 1, 0
	}
	if targetLine <= 0 {
		end := min(totalLines, previewContextRadius*2+1)
		return 1, end
	}
	start := targetLine - previewContextRadius
	if start < 1 {
		start = 1
	}
	end := targetLine + previewContextRadius
	if end > totalLines {
		end = totalLines
	}
	if end-start < previewContextRadius*2 && totalLines > previewContextRadius*2+1 {
		if start == 1 {
			end = min(totalLines, previewContextRadius*2+1)
		} else if end == totalLines {
			start = max(1, totalLines-previewContextRadius*2)
		}
	}
	return start, end
}

func (m Model) renderPreviewPanel() string {
	if m.previewPanel == nil {
		return ""
	}
	return m.renderPreviewPanelBox(m.boxInnerWidth(m.mainColumnWidth()))
}

func (m Model) renderSidebarPreviewPanel(totalHeight int) string {
	if m.previewPanel == nil {
		return ""
	}
	width := m.boxInnerWidth(m.sidebarWidth())
	body := m.renderPreviewPanelBody(width)
	innerHeight := max(lipgloss.Height(body), totalHeight-2)
	title := lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true).Render(" " + previewText(m.currentLanguage(), "title"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Height(innerHeight).
		Width(width).
		Render(title + "\n" + body)
}

func (m Model) renderPreviewPanelBox(width int) string {
	return m.renderContextBox(previewText(m.currentLanguage(), "title"), m.renderPreviewPanelBody(width), lipgloss.Color("13"))
}

func (m Model) renderPreviewPanelBody(width int) string {
	state := m.previewPanel
	if state == nil {
		return ""
	}
	lines := []string{
		fmt.Sprintf("%s %s", previewText(m.currentLanguage(), "path"), truncateDisplayWidth(state.DisplayPath, max(12, width-6))),
	}
	if state.TargetLine > 0 {
		lines = append(lines, fmt.Sprintf("%s %d", previewText(m.currentLanguage(), "line"), state.TargetLine))
	}
	lines = append(lines, "")
	if state.Error != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(truncateDisplayWidth(state.Error, width)))
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(previewText(m.currentLanguage(), "hint")))
		return strings.Join(lines, "\n")
	}
	if state.StartLine > 1 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("…"))
	}
	lineNoWidth := len(strconv.Itoa(max(state.EndLine, state.TargetLine)))
	lineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	targetStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	for idx, content := range state.Lines {
		lineNo := state.StartLine + idx
		prefix := "  "
		numberStyle := lineStyle
		if lineNo == state.TargetLine {
			prefix = "› "
			numberStyle = targetStyle
			content = targetStyle.Render(truncateDisplayWidth(content, max(12, width-lineNoWidth-5)))
		} else {
			content = truncateDisplayWidth(content, max(12, width-lineNoWidth-5))
		}
		lines = append(lines, fmt.Sprintf("%s%s %s", prefix, numberStyle.Render(fmt.Sprintf("%*d", lineNoWidth, lineNo)), content))
	}
	if state.EndLine > 0 && state.EndLine < state.TotalLines {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("…"))
	}
	lines = append(lines, "", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(previewText(m.currentLanguage(), "hint")))
	return strings.Join(lines, "\n")
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
		}
	}
	return key
}
