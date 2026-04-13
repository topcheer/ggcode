package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/topcheer/ggcode/internal/session"
)

type fileBrowserState struct {
	rootPath     string
	selectedPath string
	selected     int
	entries      []fileBrowserEntry
	expanded     map[string]bool
	filter       string
	filtering    bool
	treeViewport ViewportModel
	preview      *previewPanelState
}

type fileBrowserEntry struct {
	path  string
	name  string
	depth int
	isDir bool
}

var skippedBrowserDirs = map[string]struct{}{
	".git":         {},
	".gradle":      {},
	".cxx":         {},
	".next":        {},
	".nuxt":        {},
	".cache":       {},
	".dart_tool":   {},
	".terraform":   {},
	"node_modules": {},
	"vendor":       {},
	".venv":        {},
	"venv":         {},
	"pods":         {},
	"deriveddata":  {},
	"classes":      {},
	"bin":          {},
	"dist":         {},
	"build":        {},
	"out":          {},
	"debug":        {},
	"release":      {},
	"target":       {},
	"coverage":     {},
	"__pycache__":  {},
}

func newFileBrowserState(root string) *fileBrowserState {
	root = session.NormalizeWorkspacePath(root)
	vp := NewViewportModel(1, 1)
	vp.autoFollow = false
	return &fileBrowserState{
		rootPath:     root,
		expanded:     map[string]bool{root: true},
		treeViewport: vp,
	}
}

func (m *Model) toggleFileBrowser() {
	if m.fileBrowser != nil {
		m.fileBrowser = nil
		return
	}
	m.previewPanel = nil
	root, err := os.Getwd()
	if err != nil {
		return
	}
	state := newFileBrowserState(root)
	m.fileBrowser = state
	m.syncFileBrowser(true)
}

func (m *Model) syncFileBrowser(initial bool) {
	if m.fileBrowser == nil {
		return
	}
	state := m.fileBrowser
	state.entries = buildFileBrowserEntries(state.rootPath, state.expanded, state.filter)
	if len(state.entries) == 0 {
		state.selected = 0
		state.selectedPath = ""
		state.preview = nil
		return
	}
	if initial && strings.TrimSpace(state.selectedPath) == "" {
		for _, entry := range state.entries {
			if !entry.isDir {
				state.selectedPath = entry.path
				break
			}
		}
		if state.selectedPath == "" {
			state.selectedPath = state.entries[0].path
		}
	}
	resolveFileBrowserSelection(state)

	treeWidth := m.fileBrowserTreeWidth()
	treeHeight := m.fileBrowserContentHeight()
	state.treeViewport.SetSize(treeWidth, treeHeight)
	state.treeViewport.SetContent(renderFileBrowserTree(state.entries, state.expanded, state.selected, treeWidth))
	ensureViewportSelectionVisible(&state.treeViewport, state.selected)

	entry := state.entries[state.selected]
	if entry.isDir {
		state.preview = &previewPanelState{
			DisplayPath: displayPreviewPath("", entry.path),
			AbsPath:     entry.path,
			Content:     fileBrowserText(m.currentLanguage(), "dir_preview"),
		}
		state.preview.viewport = newPreviewViewport()
	} else {
		state.preview = buildPreviewPanelStateForPath(entry.path, 0)
	}
	if state.preview != nil {
		m.syncFileBrowserPreview(false)
	}
}

func resolveFileBrowserSelection(state *fileBrowserState) {
	if state == nil || len(state.entries) == 0 {
		return
	}
	selectedPath := session.NormalizeWorkspacePath(state.selectedPath)
	if state.selected >= 0 && state.selected < len(state.entries) && state.entries[state.selected].path == selectedPath {
		state.selectedPath = selectedPath
		return
	}
	selected := 0
	for i, entry := range state.entries {
		if entry.path == selectedPath {
			selected = i
			break
		}
	}
	state.selected = selected
	state.selectedPath = state.entries[selected].path
}

func buildFileBrowserEntries(root string, expanded map[string]bool, filter string) []fileBrowserEntry {
	filter = strings.TrimSpace(strings.ToLower(filter))
	entries, _ := collectFileBrowserEntries(root, 0, expanded, filter)
	return entries
}

func collectFileBrowserEntries(dir string, depth int, expanded map[string]bool, filter string) ([]fileBrowserEntry, bool) {
	children, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	slices.SortFunc(children, func(a, b os.DirEntry) int {
		if a.IsDir() != b.IsDir() {
			if a.IsDir() {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name()))
	})

	var entries []fileBrowserEntry
	matched := false
	for _, child := range children {
		name := child.Name()
		if child.IsDir() {
			if _, skip := skippedBrowserDirs[strings.ToLower(name)]; skip {
				continue
			}
		}
		path := session.NormalizeWorkspacePath(filepath.Join(dir, name))
		nameMatches := filter == "" || strings.Contains(strings.ToLower(name), filter)
		if child.IsDir() {
			childEntries, childMatched := collectFileBrowserEntries(path, depth+1, expanded, filter)
			if !nameMatches && !childMatched {
				continue
			}
			entries = append(entries, fileBrowserEntry{
				path:  path,
				name:  name,
				depth: depth,
				isDir: true,
			})
			matched = true
			if expanded[path] || filter != "" {
				entries = append(entries, childEntries...)
			}
			continue
		}
		if !nameMatches {
			continue
		}
		entries = append(entries, fileBrowserEntry{
			path:  path,
			name:  name,
			depth: depth,
			isDir: false,
		})
		matched = true
	}
	return entries, matched
}

func renderFileBrowserTree(entries []fileBrowserEntry, expanded map[string]bool, selected, width int) string {
	var rows []string
	for i, entry := range entries {
		indent := strings.Repeat("  ", entry.depth)
		icon := "· "
		if entry.isDir {
			if expanded[entry.path] {
				icon = "▾ "
			} else {
				icon = "▸ "
			}
		}
		row := indent + icon + entry.name
		row = truncateDisplayWidth(row, max(8, width))
		if i == selected {
			row = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true).Render("❯ " + row)
		} else {
			row = "  " + row
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

func ensureViewportSelectionVisible(vp *ViewportModel, selected int) {
	if vp == nil {
		return
	}
	visible := max(1, vp.VisibleLineCount())
	offset := vp.YOffset()
	switch {
	case selected < offset:
		vp.vp.YOffset = selected
	case selected >= offset+visible:
		vp.vp.YOffset = selected - visible + 1
	}
}

func (m Model) fileBrowserTreeWidth() int {
	width := m.viewWidth() / 3
	if width < 24 {
		width = 24
	}
	return width
}

func (m Model) fileBrowserContentHeight() int {
	height := m.viewHeight() - 5
	if height < 3 {
		height = 3
	}
	return height
}

func (m Model) fileBrowserPreviewWidth() int {
	width := m.viewWidth() - m.fileBrowserTreeWidth() - 8
	if width < 20 {
		width = 20
	}
	return width
}

func (m *Model) syncFileBrowserPreview(initial bool) {
	if m.fileBrowser == nil || m.fileBrowser.preview == nil {
		return
	}
	state := m.fileBrowser.preview
	width := m.fileBrowserPreviewWidth()
	height := m.fileBrowserContentHeight()
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

func (m Model) renderFileBrowser() string {
	if m.fileBrowser == nil {
		return ""
	}
	state := m.fileBrowser
	title := lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true).Render(fileBrowserText(m.currentLanguage(), "title"))
	meta := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(displayPreviewPath("", state.rootPath))
	filterLine := lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Render(fileBrowserFilterText(m.currentLanguage(), state.filter, state.filtering))
	left := lipgloss.NewStyle().
		Width(m.fileBrowserTreeWidth()).
		Render(strings.Join([]string{title, meta, filterLine, state.treeViewport.View()}, "\n"))

	previewTitle := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true).Render(fileBrowserText(m.currentLanguage(), "preview"))
	previewMeta := ""
	previewBody := fileBrowserText(m.currentLanguage(), "empty")
	if state.preview != nil {
		previewMeta = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Bold(true).
			Render(truncateDisplayWidth(displayPreviewPath("", state.preview.AbsPath), max(20, m.fileBrowserPreviewWidth())))
		previewBody = state.preview.viewport.View()
	}
	right := lipgloss.NewStyle().
		Width(m.fileBrowserPreviewWidth()).
		Render(strings.Join([]string{previewTitle, previewMeta, "", previewBody}, "\n"))

	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(fileBrowserText(m.currentLanguage(), "hint"))
	content := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Width(max(1, m.viewWidth()-m.terminalRightMargin())).
		Height(max(6, m.viewHeight())).
		Render(strings.Join([]string{content, footer}, "\n\n"))
}

func (m *Model) handleFileBrowserMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if m.fileBrowser == nil {
		return *m, nil
	}
	if msg.Alt {
		return *m, nil
	}
	switch msg.Type {
	case tea.MouseWheelUp:
		if m.fileBrowser.preview != nil {
			m.fileBrowser.preview.viewport.ScrollUp(3)
		}
	case tea.MouseWheelDown:
		if m.fileBrowser.preview != nil {
			m.fileBrowser.preview.viewport.ScrollDown(3)
		}
	}
	return *m, nil
}

func (m *Model) handleFileBrowserKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.fileBrowser == nil {
		return *m, nil
	}
	state := m.fileBrowser
	resolveFileBrowserSelection(state)
	switch msg.String() {
	case "ctrl+f", "esc":
		if state.filtering || state.filter != "" {
			if state.filter != "" {
				state.filter = ""
				m.syncFileBrowser(false)
			}
			state.filtering = false
			return *m, nil
		}
		m.fileBrowser = nil
		return *m, nil
	case "/":
		state.filtering = true
		return *m, nil
	case "enter":
		if state.filtering {
			state.filtering = false
			return *m, nil
		}
		entry := state.entries[state.selected]
		if entry.isDir {
			if state.expanded[entry.path] {
				delete(state.expanded, entry.path)
			} else {
				state.expanded[entry.path] = true
			}
			m.syncFileBrowser(false)
		}
		return *m, nil
	case "backspace":
		if state.filtering && state.filter != "" {
			state.filter = state.filter[:len(state.filter)-1]
			m.syncFileBrowser(false)
		}
		return *m, nil
	case "up", "k":
		if state.selected > 0 {
			state.selected--
			state.selectedPath = state.entries[state.selected].path
			m.syncFileBrowser(false)
		}
	case "down", "j":
		if state.selected < len(state.entries)-1 {
			state.selected++
			state.selectedPath = state.entries[state.selected].path
			m.syncFileBrowser(false)
		}
	case "right", "l":
		entry := state.entries[state.selected]
		if entry.isDir {
			state.expanded[entry.path] = true
			m.syncFileBrowser(false)
		}
	case "left", "h":
		entry := state.entries[state.selected]
		if entry.isDir && state.expanded[entry.path] {
			delete(state.expanded, entry.path)
			m.syncFileBrowser(false)
			return *m, nil
		}
		parent := filepath.Dir(entry.path)
		for i, candidate := range state.entries {
			if candidate.path == parent {
				state.selected = i
				state.selectedPath = candidate.path
				break
			}
		}
		m.syncFileBrowser(false)
	case "pgup":
		if state.preview != nil {
			state.preview.viewport.ScrollUp(max(1, state.preview.viewport.VisibleLineCount()/2))
		}
	case "pgdown":
		if state.preview != nil {
			state.preview.viewport.ScrollDown(max(1, state.preview.viewport.VisibleLineCount()/2))
		}
	default:
		if state.filtering && len(msg.Runes) > 0 {
			state.filter += string(msg.Runes)
			m.syncFileBrowser(false)
		}
	}
	return *m, nil
}

func fileBrowserFilterText(lang Language, filter string, active bool) string {
	label := fileBrowserText(lang, "filter")
	value := filter
	if value == "" {
		value = fileBrowserText(lang, "filter_empty")
	}
	prefix := ""
	if active {
		prefix = "/"
	}
	return fmt.Sprintf("%s %s%s", label, prefix, value)
}

func fileBrowserText(lang Language, key string) string {
	switch lang {
	case LangZhCN:
		switch key {
		case "title":
			return "项目文件"
		case "preview":
			return "文件预览"
		case "empty":
			return "选择一个文件进行预览"
		case "dir_preview":
			return "目录不可直接预览。请在左侧展开并选择文件。"
		case "filter":
			return "过滤:"
		case "filter_empty":
			return "按 / 过滤文件名"
		case "hint":
			return "Ctrl-F / Esc 关闭 • ↑↓ 选择 • Enter 展开/收起目录 • / 过滤文件名 • PgUp / PgDn 滚动预览"
		}
	default:
		switch key {
		case "title":
			return "Project files"
		case "preview":
			return "Preview"
		case "empty":
			return "Select a file to preview"
		case "dir_preview":
			return "Directories are not previewable. Expand the folder and select a file."
		case "filter":
			return "Filter:"
		case "filter_empty":
			return "press / to filter filenames"
		case "hint":
			return "Ctrl-F / Esc closes • Up/Down selects • Enter toggles folders • / filters filenames • PgUp/PgDn scrolls preview"
		}
	}
	return ""
}

func isBinaryPreviewData(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	if !utf8.Valid(data) {
		return true
	}
	controlRunes := 0
	totalRunes := 0
	for _, r := range string(data) {
		totalRunes++
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			controlRunes++
		}
	}
	return totalRunes > 0 && controlRunes*5 > totalRunes
}
