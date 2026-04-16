package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/session"
)

const (
	resumePickerFlagValue = "__ggcode_resume_picker__"
	resumePickerPageSize  = 5
)

type resumePickerGroup int

const (
	resumePickerCurrentWorkspace resumePickerGroup = iota
	resumePickerOtherWorkspaces
)

type resumePickerItem struct {
	group      resumePickerGroup
	groupIndex int
	overallID  string
	session    *session.Session
}

type resumePickerModel struct {
	current      []*session.Session
	others       []*session.Session
	currentPage  int
	otherPage    int
	cursorGroup  resumePickerGroup
	cursorIndex  int
	selectedID   string
	cancelled    bool
	currentLabel string
}

func pickResumeSession(store session.Store, currentWorkspace string) (string, error) {
	sessions, err := store.List()
	if err != nil {
		return "", fmt.Errorf("listing sessions: %w", err)
	}
	current, others := groupResumePickerSessions(sessions, currentWorkspace)
	if len(current) == 0 && len(others) == 0 {
		return "", nil
	}
	model := newResumePickerModel(current, others, currentWorkspace)
	finalModel, err := tea.NewProgram(model).Run()
	if err != nil {
		return "", fmt.Errorf("running resume picker: %w", err)
	}
	result, ok := finalModel.(resumePickerModel)
	if !ok {
		return "", fmt.Errorf("unexpected resume picker model type %T", finalModel)
	}
	if result.cancelled {
		return "", nil
	}
	return strings.TrimSpace(result.selectedID), nil
}

func groupResumePickerSessions(sessions []*session.Session, currentWorkspace string) ([]*session.Session, []*session.Session) {
	currentWorkspace = session.NormalizeWorkspacePath(currentWorkspace)
	current := make([]*session.Session, 0, len(sessions))
	others := make([]*session.Session, 0, len(sessions))
	for _, ses := range sessions {
		if ses == nil {
			continue
		}
		if currentWorkspace != "" && session.NormalizeWorkspacePath(ses.Workspace) == currentWorkspace {
			current = append(current, ses)
			continue
		}
		others = append(others, ses)
	}
	return current, others
}

func newResumePickerModel(current, others []*session.Session, currentWorkspace string) resumePickerModel {
	m := resumePickerModel{
		current:      current,
		others:       others,
		currentLabel: compactWorkspaceLabel(currentWorkspace),
	}
	switch {
	case len(current) > 0:
		m.cursorGroup = resumePickerCurrentWorkspace
	case len(others) > 0:
		m.cursorGroup = resumePickerOtherWorkspaces
	}
	return m
}

func (m resumePickerModel) Init() tea.Cmd { return nil }

func (m resumePickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "left", "h":
			m.turnPage(-1)
		case "right", "l":
			m.turnPage(1)
		case "enter":
			if item := m.selectedItem(); item != nil && item.session != nil {
				m.selectedID = item.session.ID
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m resumePickerModel) View() tea.View {
	var b strings.Builder
	b.WriteString("Resume session\n\n")
	if m.currentLabel != "" {
		b.WriteString("Workspace: ")
		b.WriteString(m.currentLabel)
		b.WriteString("\n\n")
	}
	if len(m.current) > 0 {
		b.WriteString(m.renderGroup("Current workspace", resumePickerCurrentWorkspace))
	}
	if len(m.others) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.renderGroup("Other workspaces", resumePickerOtherWorkspaces))
	}
	b.WriteString("\n↑/↓ move • ←/→ page active group • Enter resume • Esc start a new session")
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m resumePickerModel) renderGroup(title string, group resumePickerGroup) string {
	items := m.visibleItemsForGroup(group)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	page, totalPages := m.pageInfo(group)
	start, end, total := m.pageBounds(group)
	fmt.Fprintf(&b, "%s (%d/%d, %d-%d of %d)\n", title, page+1, totalPages, start+1, end, total)
	for _, item := range items {
		cursor := "  "
		if item.group == m.cursorGroup && item.groupIndex == m.cursorIndex {
			cursor = "> "
		}
		b.WriteString(cursor)
		b.WriteString(formatResumePickerSession(item.session, m.currentLabel))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *resumePickerModel) move(delta int) {
	items := m.visibleItems()
	if len(items) == 0 {
		return
	}
	selectedPos := 0
	for i, item := range items {
		if item.group == m.cursorGroup && item.groupIndex == m.cursorIndex {
			selectedPos = i
			break
		}
	}
	next := (selectedPos + delta + len(items)) % len(items)
	m.cursorGroup = items[next].group
	m.cursorIndex = items[next].groupIndex
}

func (m *resumePickerModel) turnPage(delta int) {
	switch m.cursorGroup {
	case resumePickerCurrentWorkspace:
		total := m.totalPages(resumePickerCurrentWorkspace)
		if total <= 1 {
			return
		}
		m.currentPage = clampResumePage(m.currentPage+delta, total)
		m.cursorIndex = m.firstVisibleGroupIndex(resumePickerCurrentWorkspace)
	case resumePickerOtherWorkspaces:
		total := m.totalPages(resumePickerOtherWorkspaces)
		if total <= 1 {
			return
		}
		m.otherPage = clampResumePage(m.otherPage+delta, total)
		m.cursorIndex = m.firstVisibleGroupIndex(resumePickerOtherWorkspaces)
	}
}

func (m resumePickerModel) selectedItem() *resumePickerItem {
	for _, item := range m.visibleItems() {
		if item.group == m.cursorGroup && item.groupIndex == m.cursorIndex {
			return &item
		}
	}
	return nil
}

func (m resumePickerModel) visibleItems() []resumePickerItem {
	items := m.visibleItemsForGroup(resumePickerCurrentWorkspace)
	items = append(items, m.visibleItemsForGroup(resumePickerOtherWorkspaces)...)
	return items
}

func (m resumePickerModel) visibleItemsForGroup(group resumePickerGroup) []resumePickerItem {
	sessions := m.groupSessions(group)
	if len(sessions) == 0 {
		return nil
	}
	start, end := m.groupPageRange(group)
	items := make([]resumePickerItem, 0, end-start)
	for idx := start; idx < end; idx++ {
		items = append(items, resumePickerItem{
			group:      group,
			groupIndex: idx,
			overallID:  sessions[idx].ID,
			session:    sessions[idx],
		})
	}
	return items
}

func (m resumePickerModel) firstVisibleGroupIndex(group resumePickerGroup) int {
	start, end := m.groupPageRange(group)
	if start >= end {
		return 0
	}
	return start
}

func (m resumePickerModel) pageInfo(group resumePickerGroup) (int, int) {
	switch group {
	case resumePickerCurrentWorkspace:
		return m.currentPage, m.totalPages(group)
	default:
		return m.otherPage, m.totalPages(group)
	}
}

func (m resumePickerModel) pageBounds(group resumePickerGroup) (int, int, int) {
	sessions := m.groupSessions(group)
	start, end := m.groupPageRange(group)
	return start, end, len(sessions)
}

func (m resumePickerModel) totalPages(group resumePickerGroup) int {
	sessions := m.groupSessions(group)
	if len(sessions) == 0 {
		return 0
	}
	return (len(sessions) + resumePickerPageSize - 1) / resumePickerPageSize
}

func (m resumePickerModel) groupPageRange(group resumePickerGroup) (int, int) {
	sessions := m.groupSessions(group)
	if len(sessions) == 0 {
		return 0, 0
	}
	page := 0
	switch group {
	case resumePickerCurrentWorkspace:
		page = clampResumePage(m.currentPage, m.totalPages(group))
	case resumePickerOtherWorkspaces:
		page = clampResumePage(m.otherPage, m.totalPages(group))
	}
	start := page * resumePickerPageSize
	end := start + resumePickerPageSize
	if end > len(sessions) {
		end = len(sessions)
	}
	return start, end
}

func (m resumePickerModel) groupSessions(group resumePickerGroup) []*session.Session {
	switch group {
	case resumePickerCurrentWorkspace:
		return m.current
	default:
		return m.others
	}
}

func clampResumePage(page, total int) int {
	if total <= 0 {
		return 0
	}
	if page < 0 {
		return 0
	}
	if page >= total {
		return total - 1
	}
	return page
}

func formatResumePickerSession(ses *session.Session, currentLabel string) string {
	if ses == nil {
		return ""
	}
	title := strings.TrimSpace(ses.Title)
	if title == "" {
		title = "Untitled session"
	}
	meta := []string{ses.ID}
	if !ses.UpdatedAt.IsZero() {
		meta = append(meta, ses.UpdatedAt.Local().Format(time.DateTime))
	}
	workspace := compactWorkspaceLabel(ses.Workspace)
	if workspace != "" && workspace != currentLabel {
		meta = append(meta, workspace)
	}
	return title + "\n    " + strings.Join(meta, " • ")
}

func compactWorkspaceLabel(path string) string {
	normalized := session.NormalizeWorkspacePath(path)
	if normalized == "" {
		return ""
	}
	base := filepath.Base(normalized)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return normalized
	}
	return base + " — " + normalized
}
