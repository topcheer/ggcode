package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/lsp"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/version"
)

type inspectorPanelKind string

const (
	inspectorPanelSessions    inspectorPanelKind = "sessions"
	inspectorPanelAgents      inspectorPanelKind = "agents"
	inspectorPanelCheckpoints inspectorPanelKind = "checkpoints"
	inspectorPanelMemory      inspectorPanelKind = "memory"
	inspectorPanelTodos       inspectorPanelKind = "todos"
	inspectorPanelPlugins     inspectorPanelKind = "plugins"
	inspectorPanelConfig      inspectorPanelKind = "config"
	inspectorPanelStatus      inspectorPanelKind = "status"
	inspectorPanelLSPInstall  inspectorPanelKind = "lsp-install"
)

type inspectorPanelState struct {
	kind              inspectorPanelKind
	cursor            int
	message           string
	lspLanguageID     string
	lspLanguageName   string
	lspInstallOptions []lsp.InstallOption
}

type inspectorPanelItem struct {
	ID       string
	Title    string
	Summary  string
	Detail   string
	Disabled bool
}

func (m *Model) openInspectorPanel(kind inspectorPanelKind) {
	m.inspectorPanel = &inspectorPanelState{kind: kind}
}

func (m *Model) closeInspectorPanel() {
	m.inspectorPanel = nil
}

func (m *Model) setInspectorMessage(message string) {
	if m.inspectorPanel == nil {
		return
	}
	m.inspectorPanel.message = message
}

func (m Model) renderInspectorPanel() string {
	if m.inspectorPanel == nil {
		return ""
	}
	title := m.inspectorPanelTitle(m.inspectorPanel.kind)
	items := m.inspectorPanelItems(m.inspectorPanel.kind)
	cursor := clampInspectorCursor(m.inspectorPanel.cursor, len(items))
	width := m.boxInnerWidth(m.mainColumnWidth())
	leftWidth := inspectorPanelLeftWidth(width)
	rightWidth := max(28, width-leftWidth-2)
	if leftWidth+2+rightWidth > width {
		leftWidth = max(18, width-rightWidth-2)
	}
	height := 18
	leftLines := m.renderInspectorPanelListLines(items, cursor, leftWidth, height)
	rightLines := m.renderInspectorPanelDetailLines(items, cursor, rightWidth, height)
	body := joinHarnessPanelColumns(leftLines, rightLines, leftWidth, rightWidth, height)
	footer := m.renderInspectorPanelFooter(width)
	if footer != "" {
		body += "\n\n" + footer
	}
	return m.renderContextBox(title, body, lipgloss.Color("12"))
}

func inspectorPanelLeftWidth(totalWidth int) int {
	switch {
	case totalWidth >= 88:
		return 32
	case totalWidth >= 72:
		return 28
	default:
		return max(18, totalWidth/3)
	}
}

func (m Model) renderInspectorPanelListLines(items []inspectorPanelItem, cursor, width, height int) []string {
	if len(items) == 0 {
		return wrapHarnessPanelText(m.inspectorPanelEmptyState(), width, height)
	}
	start, end := inspectorPanelWindow(len(items), cursor, max(1, height))
	lines := make([]string, 0, height)
	for i := start; i < end; i++ {
		item := items[i]
		prefix := "  "
		style := lipgloss.NewStyle()
		if i == cursor {
			prefix = "› "
			style = style.Foreground(lipgloss.Color("12")).Bold(true)
		} else if item.Disabled {
			style = style.Foreground(lipgloss.Color("8"))
		}
		lines = append(lines, style.Render(prefix+truncateDisplayWidth(item.Title, max(1, width-2))))
		if item.Summary != "" {
			summary := truncateDisplayWidth(item.Summary, max(1, width-4))
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  "+summary))
		}
		if len(lines) >= height {
			break
		}
	}
	return lines
}

func (m Model) renderInspectorPanelDetailLines(items []inspectorPanelItem, cursor, width, height int) []string {
	if len(items) == 0 || cursor < 0 || cursor >= len(items) {
		return wrapHarnessPanelText(m.inspectorPanelEmptyState(), width, height)
	}
	item := items[cursor]
	detail := strings.TrimSpace(item.Detail)
	if detail == "" {
		detail = item.Title
	}
	return wrapHarnessPanelText(detail, width, height)
}

func (m Model) renderInspectorPanelFooter(width int) string {
	if m.inspectorPanel == nil {
		return ""
	}
	lines := []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.inspectorPanelHints(m.inspectorPanel.kind)),
	}
	if msg := strings.TrimSpace(m.inspectorPanel.message); msg != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(truncateDisplayWidth(msg, width)))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) handleInspectorPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.inspectorPanel == nil {
		return *m, nil
	}
	items := m.inspectorPanelItems(m.inspectorPanel.kind)
	m.inspectorPanel.cursor = clampInspectorCursor(m.inspectorPanel.cursor, len(items))
	switch msg.String() {
	case "up", "k":
		if len(items) > 0 {
			m.inspectorPanel.cursor = (m.inspectorPanel.cursor - 1 + len(items)) % len(items)
		}
	case "down", "j", "tab":
		if len(items) > 0 {
			m.inspectorPanel.cursor = (m.inspectorPanel.cursor + 1) % len(items)
		}
	case "shift+tab":
		if len(items) > 0 {
			m.inspectorPanel.cursor = (m.inspectorPanel.cursor - 1 + len(items)) % len(items)
		}
	case "enter":
		return m.handleInspectorPrimaryAction(items)
	case "e", "E":
		if m.inspectorPanel.kind == inspectorPanelSessions {
			return m.handleInspectorSessionExport(items)
		}
	case "x", "X":
		if m.inspectorPanel.kind == inspectorPanelAgents {
			return m.handleInspectorAgentCancel(items)
		}
	case "c", "C":
		switch m.inspectorPanel.kind {
		case inspectorPanelMemory:
			return m.handleInspectorMemoryClear()
		case inspectorPanelTodos:
			return m.handleInspectorTodoClear()
		}
	case "esc", "ctrl+c":
		if m.inspectorPanel.kind == inspectorPanelLSPInstall {
			m.openInspectorPanel(inspectorPanelStatus)
			return *m, nil
		}
		m.closeInspectorPanel()
	}
	return *m, nil
}

func (m *Model) handleInspectorPrimaryAction(items []inspectorPanelItem) (Model, tea.Cmd) {
	if m.inspectorPanel == nil || len(items) == 0 {
		return *m, nil
	}
	idx := clampInspectorCursor(m.inspectorPanel.cursor, len(items))
	item := items[idx]
	switch m.inspectorPanel.kind {
	case inspectorPanelSessions:
		if item.ID == "" {
			return *m, nil
		}
		m.closeInspectorPanel()
		return *m, m.resumeSession(item.ID)
	case inspectorPanelCheckpoints:
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil || item.ID == "" {
			return *m, nil
		}
		cp, err := cpMgr.Revert(item.ID)
		if err != nil {
			m.setInspectorMessage(inspectorText(m.currentLanguage(), "revert_failed", err))
			return *m, nil
		}
		m.setInspectorMessage(inspectorText(m.currentLanguage(), "reverted", displayToolFileTarget(cp.FilePath)))
		return *m, nil
	case inspectorPanelStatus:
		return m.handleInspectorLSPStatusAction(items)
	case inspectorPanelLSPInstall:
		return m.handleInspectorLSPInstallAction(items)
	default:
		return *m, nil
	}
}

func (m *Model) handleInspectorLSPStatusAction(items []inspectorPanelItem) (Model, tea.Cmd) {
	if m.inspectorPanel == nil || len(items) == 0 {
		return *m, nil
	}
	item := items[clampInspectorCursor(m.inspectorPanel.cursor, len(items))]
	if !strings.HasPrefix(item.ID, "lsp-") {
		return *m, nil
	}
	langID := strings.TrimPrefix(item.ID, "lsp-")
	status := lsp.DetectWorkspaceStatus(workingDirFromModel(m))
	for _, lang := range status.Languages {
		if lang.ID != langID || lang.Available {
			continue
		}
		if len(lang.InstallOptions) == 0 {
			m.setInspectorMessage(inspectorText(m.currentLanguage(), "lsp_install_unavailable"))
			return *m, nil
		}
		if len(lang.InstallOptions) == 1 {
			m.closeInspectorPanel()
			return *m, m.submitInspectorShellCommand(lang.InstallOptions[0].Command)
		}
		m.openLSPInstallPanel(lang)
		return *m, nil
	}
	return *m, nil
}

func (m *Model) handleInspectorLSPInstallAction(items []inspectorPanelItem) (Model, tea.Cmd) {
	if m.inspectorPanel == nil || len(items) == 0 {
		return *m, nil
	}
	idx := clampInspectorCursor(m.inspectorPanel.cursor, len(items))
	if idx >= len(m.inspectorPanel.lspInstallOptions) {
		return *m, nil
	}
	command := strings.TrimSpace(m.inspectorPanel.lspInstallOptions[idx].Command)
	if command == "" {
		m.setInspectorMessage(inspectorText(m.currentLanguage(), "lsp_install_unavailable"))
		return *m, nil
	}
	m.closeInspectorPanel()
	return *m, m.submitInspectorShellCommand(command)
}

func (m *Model) submitInspectorShellCommand(command string) tea.Cmd {
	if m.shellCommandSubmitter != nil {
		return m.shellCommandSubmitter(command, true)
	}
	return m.submitShellCommand(command, true)
}

func (m *Model) openLSPInstallPanel(lang lsp.LanguageStatus) {
	options := slices.Clone(lang.InstallOptions)
	m.inspectorPanel = &inspectorPanelState{
		kind:              inspectorPanelLSPInstall,
		lspLanguageID:     lang.ID,
		lspLanguageName:   lang.DisplayName,
		lspInstallOptions: options,
	}
}

func (m *Model) handleInspectorSessionExport(items []inspectorPanelItem) (Model, tea.Cmd) {
	if m.inspectorPanel == nil || len(items) == 0 {
		return *m, nil
	}
	item := items[clampInspectorCursor(m.inspectorPanel.cursor, len(items))]
	if item.ID == "" {
		return *m, nil
	}
	return *m, m.exportSession(item.ID)
}

func (m *Model) handleInspectorAgentCancel(items []inspectorPanelItem) (Model, tea.Cmd) {
	if m.inspectorPanel == nil || len(items) == 0 || m.subAgentMgr == nil {
		return *m, nil
	}
	item := items[clampInspectorCursor(m.inspectorPanel.cursor, len(items))]
	if item.ID == "" {
		return *m, nil
	}
	if m.subAgentMgr.Cancel(item.ID) {
		m.setInspectorMessage(inspectorText(m.currentLanguage(), "agent_cancelled", item.ID))
	} else {
		m.setInspectorMessage(inspectorText(m.currentLanguage(), "agent_cancel_failed", item.ID))
	}
	return *m, nil
}

func (m *Model) handleInspectorMemoryClear() (Model, tea.Cmd) {
	if m.autoMem == nil {
		m.setInspectorMessage(inspectorText(m.currentLanguage(), "memory_unavailable"))
		return *m, nil
	}
	if err := m.autoMem.Clear(); err != nil {
		m.setInspectorMessage(inspectorText(m.currentLanguage(), "clear_failed", err))
		return *m, nil
	}
	m.setInspectorMessage(inspectorText(m.currentLanguage(), "memory_cleared"))
	return *m, nil
}

func (m *Model) handleInspectorTodoClear() (Model, tea.Cmd) {
	path := todoFilePath(workingDirFromModel(m))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		m.setInspectorMessage(inspectorText(m.currentLanguage(), "clear_failed", err))
		return *m, nil
	}
	m.todoSnapshot = nil
	m.activeTodo = nil
	m.setInspectorMessage(inspectorText(m.currentLanguage(), "todo_cleared"))
	return *m, nil
}

func (m Model) inspectorPanelItems(kind inspectorPanelKind) []inspectorPanelItem {
	switch kind {
	case inspectorPanelSessions:
		return m.inspectorSessionItems()
	case inspectorPanelAgents:
		return m.inspectorAgentItems()
	case inspectorPanelCheckpoints:
		return m.inspectorCheckpointItems()
	case inspectorPanelMemory:
		return m.inspectorMemoryItems()
	case inspectorPanelTodos:
		return m.inspectorTodoItems()
	case inspectorPanelPlugins:
		return m.inspectorPluginItems()
	case inspectorPanelConfig:
		return m.inspectorConfigItems()
	case inspectorPanelStatus:
		return m.inspectorStatusItems()
	case inspectorPanelLSPInstall:
		return m.inspectorLSPInstallItems()
	default:
		return nil
	}
}

func (m Model) inspectorSessionItems() []inspectorPanelItem {
	if m.sessionStore == nil {
		return nil
	}
	sessions, err := m.sessionStore.List()
	if err != nil {
		return []inspectorPanelItem{{Title: inspectorText(m.currentLanguage(), "sessions_error"), Detail: err.Error(), Disabled: true}}
	}
	currentWD, _ := os.Getwd()
	currentWS := session.NormalizeWorkspacePath(currentWD)
	slices.SortStableFunc(sessions, func(a, b *session.Session) int {
		aCurrent := a != nil && session.NormalizeWorkspacePath(a.Workspace) == currentWS
		bCurrent := b != nil && session.NormalizeWorkspacePath(b.Workspace) == currentWS
		if aCurrent != bCurrent {
			if aCurrent {
				return -1
			}
			return 1
		}
		switch {
		case a == nil && b == nil:
			return 0
		case a == nil:
			return 1
		case b == nil:
			return -1
		case a.UpdatedAt.Equal(b.UpdatedAt):
			return strings.Compare(a.ID, b.ID)
		case a.UpdatedAt.After(b.UpdatedAt):
			return -1
		default:
			return 1
		}
	})
	items := make([]inspectorPanelItem, 0, len(sessions))
	for _, ses := range sessions {
		if ses == nil {
			continue
		}
		title := strings.TrimSpace(ses.Title)
		if title == "" {
			title = inspectorText(m.currentLanguage(), "untitled_session")
		}
		workspace := compactWorkspaceLabelForTUI(ses.Workspace)
		summaryParts := []string{ses.ID}
		if !ses.UpdatedAt.IsZero() {
			summaryParts = append(summaryParts, ses.UpdatedAt.Local().Format(time.DateTime))
		}
		if workspace != "" && session.NormalizeWorkspacePath(ses.Workspace) != currentWS {
			summaryParts = append(summaryParts, workspace)
		} else if workspace != "" {
			summaryParts = append(summaryParts, inspectorText(m.currentLanguage(), "current_workspace"))
		}
		detail := []string{
			title,
			"",
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "session_id"), ses.ID),
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "updated"), formatInspectorTime(ses.UpdatedAt)),
			fmt.Sprintf("%s: %d", inspectorText(m.currentLanguage(), "messages"), len(ses.Messages)),
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "vendor"), firstNonEmpty(ses.Vendor, "-")),
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "endpoint"), firstNonEmpty(ses.Endpoint, "-")),
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "model"), firstNonEmpty(ses.Model, "-")),
		}
		if workspace != "" {
			detail = append(detail, fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "workspace"), workspace))
		}
		items = append(items, inspectorPanelItem{
			ID:      ses.ID,
			Title:   title,
			Summary: strings.Join(summaryParts, " • "),
			Detail:  strings.Join(detail, "\n"),
		})
	}
	return items
}

func (m Model) inspectorAgentItems() []inspectorPanelItem {
	if m.subAgentMgr == nil {
		return nil
	}
	agents := m.subAgentMgr.List()
	slices.SortStableFunc(agents, func(a, b *subagent.SubAgent) int {
		switch {
		case a == nil && b == nil:
			return 0
		case a == nil:
			return 1
		case b == nil:
			return -1
		case a.CreatedAt.Equal(b.CreatedAt):
			return strings.Compare(a.ID, b.ID)
		case a.CreatedAt.After(b.CreatedAt):
			return -1
		default:
			return 1
		}
	})
	items := make([]inspectorPanelItem, 0, len(agents))
	for _, sa := range agents {
		if sa == nil {
			continue
		}
		snap, ok := m.subAgentMgr.Snapshot(sa.ID)
		if !ok {
			continue
		}
		title := firstNonEmpty(snap.DisplayTask, snap.Task)
		if title == "" {
			title = snap.ID
		}
		summary := fmt.Sprintf("%s • %s • %d tools", snap.ID, snap.Status, snap.ToolCallCount)
		var detail []string
		detail = append(detail, title, "")
		detail = append(detail,
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "status"), snap.Status),
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "created"), formatInspectorTime(snap.CreatedAt)),
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "started"), formatInspectorTime(snap.StartedAt)),
		)
		if !snap.EndedAt.IsZero() {
			detail = append(detail, fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "ended"), formatInspectorTime(snap.EndedAt)))
		}
		if snap.CurrentPhase != "" {
			detail = append(detail, fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "phase"), snap.CurrentPhase))
		}
		if snap.ProgressSummary != "" {
			detail = append(detail, fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "progress"), snap.ProgressSummary))
		}
		if snap.Result != "" {
			detail = append(detail, "", fmt.Sprintf("%s:\n%s", inspectorText(m.currentLanguage(), "result"), snap.Result))
		}
		if snap.Error != "" {
			detail = append(detail, "", fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "error"), snap.Error))
		}
		items = append(items, inspectorPanelItem{ID: snap.ID, Title: title, Summary: summary, Detail: strings.Join(detail, "\n")})
	}
	return items
}

func (m Model) inspectorCheckpointItems() []inspectorPanelItem {
	cpMgr := m.agent.CheckpointManager()
	if cpMgr == nil {
		return nil
	}
	checkpoints := cpMgr.List()
	items := make([]inspectorPanelItem, 0, len(checkpoints))
	for i := len(checkpoints) - 1; i >= 0; i-- {
		cp := checkpoints[i]
		diffText := truncateLines(strings.TrimSpace(FormatDiff(diff.UnifiedDiff(cp.NewContent, cp.OldContent, 2))), 12)
		items = append(items, inspectorPanelItem{
			ID:      cp.ID,
			Title:   displayToolFileTarget(cp.FilePath),
			Summary: fmt.Sprintf("%s • %s", cp.ToolCall, cp.Timestamp.Format(time.DateTime)),
			Detail: strings.Join([]string{
				displayToolFileTarget(cp.FilePath),
				"",
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "checkpoint_id"), cp.ID),
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "tool"), cp.ToolCall),
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "updated"), formatInspectorTime(cp.Timestamp)),
				"",
				diffText,
			}, "\n"),
		})
	}
	return items
}

func (m Model) inspectorMemoryItems() []inspectorPanelItem {
	items := []inspectorPanelItem{
		{
			ID:      "project",
			Title:   inspectorText(m.currentLanguage(), "project_memory"),
			Summary: fmt.Sprintf("%d %s", len(m.projMemFiles), inspectorText(m.currentLanguage(), "files")),
			Detail:  inspectorListDetail(m.currentLanguage(), inspectorText(m.currentLanguage(), "project_memory"), m.projMemFiles),
		},
		{
			ID:      "auto",
			Title:   inspectorText(m.currentLanguage(), "auto_memory"),
			Summary: fmt.Sprintf("%d %s", len(m.autoMemFiles), inspectorText(m.currentLanguage(), "files")),
			Detail:  inspectorListDetail(m.currentLanguage(), inspectorText(m.currentLanguage(), "auto_memory"), m.autoMemFiles),
		},
	}
	return items
}

func (m Model) inspectorTodoItems() []inspectorPanelItem {
	todos, err := readInspectorTodos(workingDirFromModel(&m))
	if err != nil {
		return []inspectorPanelItem{{Title: inspectorText(m.currentLanguage(), "todos_error"), Detail: err.Error(), Disabled: true}}
	}
	items := make([]inspectorPanelItem, 0, len(todos))
	for _, td := range todos {
		summary := td.Status
		if td.ID != "" {
			summary = td.ID + " • " + td.Status
		}
		items = append(items, inspectorPanelItem{
			ID:      td.ID,
			Title:   firstNonEmpty(td.Content, td.ID),
			Summary: summary,
			Detail: strings.Join([]string{
				firstNonEmpty(td.Content, td.ID),
				"",
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "todo_id"), firstNonEmpty(td.ID, "-")),
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "status"), td.Status),
			}, "\n"),
		})
	}
	return items
}

func (m Model) inspectorPluginItems() []inspectorPanelItem {
	if m.pluginMgr == nil {
		return nil
	}
	results := m.pluginMgr.Results()
	items := make([]inspectorPanelItem, 0, len(results))
	for _, r := range results {
		summary := inspectorText(m.currentLanguage(), "loaded")
		if !r.Success {
			summary = inspectorText(m.currentLanguage(), "failed")
		}
		detail := []string{
			r.Name,
			"",
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "status"), summary),
			fmt.Sprintf("%s: %d", inspectorText(m.currentLanguage(), "tools"), len(r.Tools)),
		}
		if len(r.Tools) > 0 {
			detail = append(detail, "", strings.Join(r.Tools, "\n"))
		}
		if r.Error != nil {
			detail = append(detail, "", fmt.Sprintf("%s: %v", inspectorText(m.currentLanguage(), "error"), r.Error))
		}
		items = append(items, inspectorPanelItem{
			Title:    r.Name,
			Summary:  summary,
			Detail:   strings.Join(detail, "\n"),
			Disabled: !r.Success,
		})
	}
	return items
}

func (m Model) inspectorConfigItems() []inspectorPanelItem {
	if m.config == nil {
		return nil
	}
	items := []inspectorPanelItem{
		{
			ID:      "selection",
			Title:   inspectorText(m.currentLanguage(), "active_selection"),
			Summary: fmt.Sprintf("%s • %s • %s", m.config.Vendor, m.config.Endpoint, m.config.Model),
			Detail: strings.Join([]string{
				inspectorText(m.currentLanguage(), "active_selection"),
				"",
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "vendor"), m.config.Vendor),
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "endpoint"), m.config.Endpoint),
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "model"), m.config.Model),
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "language"), m.languageLabel()),
			}, "\n"),
		},
		{
			ID:      "runtime",
			Title:   inspectorText(m.currentLanguage(), "runtime_limits"),
			Summary: fmt.Sprintf("%d vendors • %d MCP", len(m.config.Vendors), len(m.config.MCPServers)),
			Detail:  inspectorRuntimeDetail(m),
		},
	}
	return items
}

func (m Model) inspectorStatusItems() []inspectorPanelItem {
	connected := 0
	for _, srv := range m.mcpServers {
		if srv.Connected {
			connected++
		}
	}
	items := []inspectorPanelItem{
		{
			ID:      "runtime",
			Title:   inspectorText(m.currentLanguage(), "runtime"),
			Summary: fmt.Sprintf("%s • %s", version.Display(), m.mode),
			Detail: strings.Join([]string{
				inspectorText(m.currentLanguage(), "runtime"),
				"",
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "version"), version.Display()),
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "mode"), m.mode),
				fmt.Sprintf("%s: %t", inspectorText(m.currentLanguage(), "fullscreen"), m.fullscreen),
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "language"), m.languageLabel()),
			}, "\n"),
		},
		{
			ID:      "agents",
			Title:   inspectorText(m.currentLanguage(), "agents"),
			Summary: fmt.Sprintf("%d %s", m.runningAgentCount(), inspectorText(m.currentLanguage(), "running")),
			Detail: strings.Join([]string{
				inspectorText(m.currentLanguage(), "agents"),
				"",
				fmt.Sprintf("%s: %d", inspectorText(m.currentLanguage(), "running"), m.runningAgentCount()),
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "update"), m.updateStatusSummary()),
				fmt.Sprintf("%s: %d/%d", inspectorText(m.currentLanguage(), "mcp"), connected, len(m.mcpServers)),
			}, "\n"),
		},
	}
	if m.session != nil {
		items = append(items, inspectorPanelItem{
			ID:      "session",
			Title:   inspectorText(m.currentLanguage(), "session"),
			Summary: fmt.Sprintf("%s • %d", truncateDisplayWidth(m.session.ID, 18), len(m.session.Messages)),
			Detail: strings.Join([]string{
				inspectorText(m.currentLanguage(), "session"),
				"",
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "session_id"), m.session.ID),
				fmt.Sprintf("%s: %d", inspectorText(m.currentLanguage(), "messages"), len(m.session.Messages)),
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "workspace"), compactWorkspaceLabelForTUI(m.session.Workspace)),
			}, "\n"),
		})
	}
	items = append(items, m.inspectorLSPStatusItems()...)
	return items
}

func (m Model) inspectorLSPStatusItems() []inspectorPanelItem {
	workspace := workingDirFromModel(&m)
	status := lsp.DetectWorkspaceStatus(workspace)
	if len(status.Languages) == 0 {
		return []inspectorPanelItem{{
			ID:      "lsp",
			Title:   inspectorText(m.currentLanguage(), "lsp"),
			Summary: inspectorText(m.currentLanguage(), "lsp_no_supported_languages"),
			Detail: strings.Join([]string{
				inspectorText(m.currentLanguage(), "lsp"),
				"",
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "workspace"), compactWorkspaceLabelForTUI(workspace)),
				fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "status"), inspectorText(m.currentLanguage(), "lsp_no_supported_languages")),
			}, "\n"),
			Disabled: true,
		}}
	}

	readyCount := 0
	detectedNames := make([]string, 0, len(status.Languages))
	for _, lang := range status.Languages {
		detectedNames = append(detectedNames, lang.DisplayName)
		if lang.Available {
			readyCount++
		}
	}
	items := []inspectorPanelItem{{
		ID:      "lsp",
		Title:   inspectorText(m.currentLanguage(), "lsp"),
		Summary: fmt.Sprintf("%d/%d %s • %s", readyCount, len(status.Languages), inspectorText(m.currentLanguage(), "lsp_ready"), strings.Join(detectedNames, ", ")),
		Detail: strings.Join([]string{
			inspectorText(m.currentLanguage(), "lsp"),
			"",
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "workspace"), compactWorkspaceLabelForTUI(workspace)),
			fmt.Sprintf("%s: %d/%d", inspectorText(m.currentLanguage(), "lsp_ready"), readyCount, len(status.Languages)),
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "language"), strings.Join(detectedNames, ", ")),
		}, "\n"),
	}}
	for _, lang := range status.Languages {
		summary := inspectorText(m.currentLanguage(), "lsp_unavailable")
		if lang.Available {
			summary = fmt.Sprintf("%s • %s", inspectorText(m.currentLanguage(), "lsp_ready"), lang.Binary)
		} else if labels := lspInstallLabels(lang.InstallOptions); len(labels) > 0 {
			summary = fmt.Sprintf("%s • %s", inspectorText(m.currentLanguage(), "lsp_install"), strings.Join(labels, ", "))
		} else if lang.Binary != "" {
			summary = fmt.Sprintf("%s • %s", inspectorText(m.currentLanguage(), "lsp_install"), lang.Binary)
		}
		detailLines := []string{
			lang.DisplayName,
			"",
			fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "status"), ternaryString(lang.Available, inspectorText(m.currentLanguage(), "lsp_ready"), inspectorText(m.currentLanguage(), "lsp_unavailable"))),
		}
		if lang.Binary != "" {
			detailLines = append(detailLines, fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "lsp_binary"), lang.Binary))
		}
		if len(lang.Evidence) > 0 {
			detailLines = append(detailLines, fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "lsp_detected_by"), strings.Join(lang.Evidence, ", ")))
		}
		if !lang.Available {
			switch len(lang.InstallOptions) {
			case 0:
				if label := firstNonEmpty(lang.Binary, lang.DisplayName); label != "" {
					detailLines = append(detailLines, fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "lsp_install"), label))
				}
			case 1:
				detailLines = append(detailLines, fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "lsp_install"), lspInstallOptionDetailLabel(lang.InstallOptions[0], m.currentLanguage())))
			default:
				detailLines = append(detailLines, inspectorText(m.currentLanguage(), "lsp_install_options")+":")
				for _, option := range lang.InstallOptions {
					detailLines = append(detailLines, "- "+lspInstallOptionDetailLabel(option, m.currentLanguage()))
				}
			}
			detailLines = append(detailLines, "", inspectorText(m.currentLanguage(), "lsp_install_enter_hint"))
		}
		items = append(items, inspectorPanelItem{
			ID:      "lsp-" + lang.ID,
			Title:   lang.DisplayName,
			Summary: summary,
			Detail:  strings.Join(detailLines, "\n"),
		})
	}
	return items
}

func (m Model) inspectorLSPInstallItems() []inspectorPanelItem {
	if m.inspectorPanel == nil {
		return nil
	}
	options := m.inspectorPanel.lspInstallOptions
	items := make([]inspectorPanelItem, 0, len(options))
	for _, option := range options {
		title := option.Label
		summary := option.Binary
		if option.Recommended {
			if summary == "" {
				summary = inspectorText(m.currentLanguage(), "lsp_recommended")
			} else {
				summary += " • " + inspectorText(m.currentLanguage(), "lsp_recommended")
			}
		}
		detailLines := []string{
			firstNonEmpty(title, m.inspectorPanel.lspLanguageName),
			"",
		}
		if option.Binary != "" {
			detailLines = append(detailLines, fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "lsp_binary"), option.Binary))
		}
		detailLines = append(detailLines, fmt.Sprintf("%s: %s", inspectorText(m.currentLanguage(), "lsp_install"), lspInstallOptionDetailLabel(option, m.currentLanguage())))
		detailLines = append(detailLines, "", inspectorText(m.currentLanguage(), "lsp_install_enter_hint"))
		items = append(items, inspectorPanelItem{
			ID:      option.ID,
			Title:   title,
			Summary: summary,
			Detail:  strings.Join(detailLines, "\n"),
		})
	}
	return items
}

func lspInstallLabels(options []lsp.InstallOption) []string {
	labels := make([]string, 0, len(options))
	for _, option := range options {
		if label := strings.TrimSpace(option.Label); label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

func lspInstallOptionDetailLabel(option lsp.InstallOption, lang Language) string {
	label := firstNonEmpty(option.Label, option.Binary, option.ID)
	if option.Recommended && label != "" {
		return fmt.Sprintf("%s (%s)", label, inspectorText(lang, "lsp_recommended"))
	}
	return label
}

func ternaryString(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

func (m Model) runningAgentCount() int {
	if m.subAgentMgr == nil {
		return 0
	}
	return m.subAgentMgr.RunningCount()
}

func (m Model) inspectorPanelTitle(kind inspectorPanelKind) string {
	return "/" + string(kind)
}

func (m Model) inspectorPanelEmptyState() string {
	if m.inspectorPanel == nil {
		return ""
	}
	switch m.inspectorPanel.kind {
	case inspectorPanelSessions:
		return inspectorText(m.currentLanguage(), "sessions_empty")
	case inspectorPanelAgents:
		return inspectorText(m.currentLanguage(), "agents_empty")
	case inspectorPanelCheckpoints:
		return inspectorText(m.currentLanguage(), "checkpoints_empty")
	case inspectorPanelMemory:
		return inspectorText(m.currentLanguage(), "memory_empty")
	case inspectorPanelTodos:
		return inspectorText(m.currentLanguage(), "todos_empty")
	case inspectorPanelPlugins:
		return inspectorText(m.currentLanguage(), "plugins_empty")
	case inspectorPanelLSPInstall:
		return inspectorText(m.currentLanguage(), "lsp_install_unavailable")
	default:
		return inspectorText(m.currentLanguage(), "panel_empty")
	}
}

func (m Model) inspectorPanelHints(kind inspectorPanelKind) string {
	switch kind {
	case inspectorPanelSessions:
		return inspectorText(m.currentLanguage(), "hint_sessions")
	case inspectorPanelAgents:
		return inspectorText(m.currentLanguage(), "hint_agents")
	case inspectorPanelCheckpoints:
		return inspectorText(m.currentLanguage(), "hint_checkpoints")
	case inspectorPanelMemory:
		return inspectorText(m.currentLanguage(), "hint_memory")
	case inspectorPanelTodos:
		return inspectorText(m.currentLanguage(), "hint_todos")
	case inspectorPanelStatus:
		return inspectorText(m.currentLanguage(), "hint_status")
	case inspectorPanelLSPInstall:
		return inspectorText(m.currentLanguage(), "hint_lsp_install")
	default:
		return inspectorText(m.currentLanguage(), "hint_default")
	}
}

func clampInspectorCursor(cursor, length int) int {
	if length <= 0 {
		return 0
	}
	if cursor < 0 {
		return 0
	}
	if cursor >= length {
		return length - 1
	}
	return cursor
}

func inspectorPanelWindow(total, cursor, height int) (int, int) {
	if total <= 0 || height <= 0 {
		return 0, 0
	}
	if total <= height {
		return 0, total
	}
	start := cursor - height/2
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > total {
		end = total
		start = max(0, end-height)
	}
	return start, end
}

func compactWorkspaceLabelForTUI(path string) string {
	normalized := session.NormalizeWorkspacePath(path)
	if normalized == "" {
		return ""
	}
	return truncateMiddleDisplayWidth(shortenSidebarPath(normalized), 56)
}

func truncateMiddleDisplayWidth(s string, maxWidth int) string {
	s = strings.TrimSpace(s)
	if maxWidth <= 0 || s == "" {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	if maxWidth <= 3 {
		return fitDisplayWidth(s, maxWidth)
	}
	runes := []rune(s)
	leftBudget := (maxWidth - 3) / 2
	rightBudget := maxWidth - 3 - leftBudget
	left := fitDisplayWidth(string(runes), leftBudget)
	right := fitDisplayWidthFromEnd(string(runes), rightBudget)
	return left + "..." + right
}

func fitDisplayWidthFromEnd(s string, maxWidth int) string {
	if maxWidth <= 0 || s == "" {
		return ""
	}
	runes := []rune(s)
	currentWidth := 0
	start := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		rw := lipgloss.Width(string(runes[i]))
		if currentWidth+rw > maxWidth {
			break
		}
		currentWidth += rw
		start = i
	}
	return string(runes[start:])
}

func formatInspectorTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.Local().Format(time.DateTime)
}

func todoFilePath(workspace string) string {
	return toolpkg.TodoFilePath(workspace)
}

func readInspectorTodos(workspace string) ([]toolpkg.Todo, error) {
	data, err := os.ReadFile(todoFilePath(workspace))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var todos []toolpkg.Todo
	if err := json.Unmarshal(data, &todos); err != nil {
		return nil, err
	}
	return todos, nil
}

func inspectorListDetail(lang Language, title string, paths []string) string {
	if len(paths) == 0 {
		return title + "\n\n" + inspectorText(lang, "none")
	}
	return title + "\n\n" + strings.Join(paths, "\n")
}

func inspectorRuntimeDetail(m Model) string {
	lines := []string{
		inspectorText(m.currentLanguage(), "runtime_limits"),
		"",
		fmt.Sprintf("%s: %d", inspectorText(m.currentLanguage(), "vendors"), len(m.config.Vendors)),
		fmt.Sprintf("%s: %d", inspectorText(m.currentLanguage(), "mcp_servers"), len(m.config.MCPServers)),
	}
	if resolved, err := m.config.ResolveActiveEndpoint(); err == nil {
		if resolved.ContextWindow > 0 {
			lines = append(lines, fmt.Sprintf("%s: %d", inspectorText(m.currentLanguage(), "context_window"), resolved.ContextWindow))
		}
		if resolved.MaxTokens > 0 {
			lines = append(lines, fmt.Sprintf("%s: %d", inspectorText(m.currentLanguage(), "max_tokens"), resolved.MaxTokens))
		}
	}
	return strings.Join(lines, "\n")
}

func inspectorText(lang Language, key string, args ...any) string {
	msg := key
	switch lang {
	case LangZhCN:
		switch key {
		case "hint_sessions":
			msg = "↑/↓ 选择 • Enter 恢复 • E 导出 • Esc 关闭"
		case "hint_agents":
			msg = "↑/↓ 选择 • X 取消 • Esc 关闭"
		case "hint_checkpoints":
			msg = "↑/↓ 选择 • Enter 回退到所选检查点 • Esc 关闭"
		case "hint_memory":
			msg = "↑/↓ 选择 • C 清空自动记忆 • Esc 关闭"
		case "hint_todos":
			msg = "↑/↓ 选择 • C 清空 todo • Esc 关闭"
		case "hint_default":
			msg = "↑/↓ 选择 • Esc 关闭"
		case "hint_status":
			msg = "↑/↓ 选择 • Enter 安装缺失的 LSP • Esc 关闭"
		case "hint_lsp_install":
			msg = "↑/↓ 选择 • Enter 执行安装 • Esc 返回 /status"
		case "sessions_empty":
			msg = "暂无会话。"
		case "agents_empty":
			msg = "暂无子 Agent。"
		case "checkpoints_empty":
			msg = "暂无检查点。"
		case "memory_empty":
			msg = "暂无记忆内容。"
		case "todos_empty":
			msg = "暂无 todo。"
		case "plugins_empty":
			msg = "暂无插件。"
		case "panel_empty":
			msg = "暂无可显示内容。"
		case "revert_failed":
			msg = "回退失败：%v"
		case "reverted":
			msg = "已回退到检查点：%s"
		case "agent_cancelled":
			msg = "已取消子 Agent %s"
		case "agent_cancel_failed":
			msg = "取消子 Agent %s 失败"
		case "memory_unavailable":
			msg = "自动记忆不可用"
		case "memory_cleared":
			msg = "已清空自动记忆"
		case "todo_cleared":
			msg = "已清空 todo"
		case "clear_failed":
			msg = "清理失败：%v"
		case "untitled_session":
			msg = "未命名会话"
		case "current_workspace":
			msg = "当前工作区"
		case "session_id":
			msg = "会话"
		case "updated":
			msg = "更新"
		case "messages":
			msg = "消息"
		case "vendor":
			msg = "供应商"
		case "endpoint":
			msg = "接入点"
		case "model":
			msg = "模型"
		case "workspace":
			msg = "工作区"
		case "status":
			msg = "状态"
		case "created":
			msg = "创建"
		case "started":
			msg = "开始"
		case "ended":
			msg = "结束"
		case "phase":
			msg = "阶段"
		case "progress":
			msg = "进度"
		case "result":
			msg = "结果"
		case "error":
			msg = "错误"
		case "checkpoint_id":
			msg = "检查点"
		case "tool":
			msg = "工具"
		case "project_memory":
			msg = "项目记忆"
		case "auto_memory":
			msg = "自动记忆"
		case "files":
			msg = "文件"
		case "none":
			msg = "无"
		case "todos_error":
			msg = "读取 todo 失败"
		case "sessions_error":
			msg = "读取会话失败"
		case "loaded":
			msg = "已加载"
		case "failed":
			msg = "失败"
		case "tools":
			msg = "工具"
		case "active_selection":
			msg = "当前选择"
		case "runtime_limits":
			msg = "运行限制"
		case "language":
			msg = "语言"
		case "runtime":
			msg = "运行时"
		case "version":
			msg = "版本"
		case "mode":
			msg = "模式"
		case "fullscreen":
			msg = "全屏"
		case "agents":
			msg = "子 Agent"
		case "running":
			msg = "运行中"
		case "update":
			msg = "更新"
		case "mcp":
			msg = "MCP"
		case "session":
			msg = "会话"
		case "todo_id":
			msg = "Todo"
		case "vendors":
			msg = "供应商数量"
		case "mcp_servers":
			msg = "MCP 服务数"
		case "context_window":
			msg = "上下文窗口"
		case "max_tokens":
			msg = "最大输出"
		case "lsp":
			msg = "LSP"
		case "lsp_ready":
			msg = "可用"
		case "lsp_unavailable":
			msg = "不可用"
		case "lsp_binary":
			msg = "二进制"
		case "lsp_install":
			msg = "安装"
		case "lsp_detected_by":
			msg = "检测依据"
		case "lsp_install_options":
			msg = "安装选项"
		case "lsp_recommended":
			msg = "推荐"
		case "lsp_install_enter_hint":
			msg = "按 Enter 直接安装；如果有多个候选，会先让你选择。"
		case "lsp_install_unavailable":
			msg = "当前没有可用的安装选项"
		case "lsp_no_supported_languages":
			msg = "当前工作区未检测到已支持的语言"
		}
	default:
		switch key {
		case "hint_sessions":
			msg = "↑/↓ select • Enter resume • E export • Esc close"
		case "hint_agents":
			msg = "↑/↓ select • X cancel • Esc close"
		case "hint_checkpoints":
			msg = "↑/↓ select • Enter revert selected checkpoint • Esc close"
		case "hint_memory":
			msg = "↑/↓ select • C clear auto memory • Esc close"
		case "hint_todos":
			msg = "↑/↓ select • C clear todos • Esc close"
		case "hint_default":
			msg = "↑/↓ select • Esc close"
		case "hint_status":
			msg = "↑/↓ select • Enter install missing LSP • Esc close"
		case "hint_lsp_install":
			msg = "↑/↓ select • Enter run installer • Esc back to /status"
		case "sessions_empty":
			msg = "No sessions saved."
		case "agents_empty":
			msg = "No sub-agents recorded."
		case "checkpoints_empty":
			msg = "No checkpoints recorded."
		case "memory_empty":
			msg = "No memory sources available."
		case "todos_empty":
			msg = "No todos recorded."
		case "plugins_empty":
			msg = "No plugins loaded."
		case "panel_empty":
			msg = "Nothing to show."
		case "revert_failed":
			msg = "Revert failed: %v"
		case "reverted":
			msg = "Reverted checkpoint for %s"
		case "agent_cancelled":
			msg = "Cancelled sub-agent %s"
		case "agent_cancel_failed":
			msg = "Failed to cancel sub-agent %s"
		case "memory_unavailable":
			msg = "Auto memory is unavailable"
		case "memory_cleared":
			msg = "Auto memory cleared"
		case "todo_cleared":
			msg = "Todos cleared"
		case "clear_failed":
			msg = "Clear failed: %v"
		case "untitled_session":
			msg = "Untitled session"
		case "current_workspace":
			msg = "current workspace"
		case "session_id":
			msg = "Session"
		case "updated":
			msg = "Updated"
		case "messages":
			msg = "Messages"
		case "vendor":
			msg = "Vendor"
		case "endpoint":
			msg = "Endpoint"
		case "model":
			msg = "Model"
		case "workspace":
			msg = "Workspace"
		case "status":
			msg = "Status"
		case "created":
			msg = "Created"
		case "started":
			msg = "Started"
		case "ended":
			msg = "Ended"
		case "phase":
			msg = "Phase"
		case "progress":
			msg = "Progress"
		case "result":
			msg = "Result"
		case "error":
			msg = "Error"
		case "checkpoint_id":
			msg = "Checkpoint"
		case "tool":
			msg = "Tool"
		case "project_memory":
			msg = "Project memory"
		case "auto_memory":
			msg = "Auto memory"
		case "files":
			msg = "files"
		case "none":
			msg = "None"
		case "todos_error":
			msg = "Failed to read todos"
		case "sessions_error":
			msg = "Failed to read sessions"
		case "loaded":
			msg = "loaded"
		case "failed":
			msg = "failed"
		case "tools":
			msg = "Tools"
		case "active_selection":
			msg = "Active selection"
		case "runtime_limits":
			msg = "Runtime limits"
		case "language":
			msg = "Language"
		case "runtime":
			msg = "Runtime"
		case "version":
			msg = "Version"
		case "mode":
			msg = "Mode"
		case "fullscreen":
			msg = "Fullscreen"
		case "agents":
			msg = "Agents"
		case "running":
			msg = "running"
		case "update":
			msg = "Update"
		case "mcp":
			msg = "MCP"
		case "session":
			msg = "Session"
		case "todo_id":
			msg = "Todo"
		case "vendors":
			msg = "Vendors"
		case "mcp_servers":
			msg = "MCP servers"
		case "context_window":
			msg = "Context window"
		case "max_tokens":
			msg = "Max tokens"
		case "lsp":
			msg = "LSP"
		case "lsp_ready":
			msg = "ready"
		case "lsp_unavailable":
			msg = "unavailable"
		case "lsp_binary":
			msg = "Binary"
		case "lsp_install":
			msg = "Install"
		case "lsp_detected_by":
			msg = "Detected by"
		case "lsp_install_options":
			msg = "Install options"
		case "lsp_recommended":
			msg = "recommended"
		case "lsp_install_enter_hint":
			msg = "Press Enter to install. Languages with multiple candidates will open a chooser first."
		case "lsp_install_unavailable":
			msg = "No install options available"
		case "lsp_no_supported_languages":
			msg = "No supported workspace languages detected"
		}
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}
