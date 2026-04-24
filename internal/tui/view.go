package tui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/muesli/reflow/wordwrap"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/permission"
)

var (
	chromeBorderColor = compat.AdaptiveColor{Light: lipgloss.Color("240"), Dark: lipgloss.Color("250")}
	mutedTextColor    = compat.AdaptiveColor{Light: lipgloss.Color("240"), Dark: lipgloss.Color("248")}
)

func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	if m.fileBrowser != nil {
		return tea.NewView(m.renderFileBrowser())
	}
	if m.previewPanel != nil {
		return tea.NewView(m.renderPreviewPanel())
	}
	header := ""
	if m.topHeaderEnabled() {
		header = m.renderHeader()
	}
	startupBanner := m.renderStartupBanner()
	actionPanel := m.renderContextPanel()
	statusBar := m.renderStatusBar()
	deviceBanner := m.renderDeviceCodeBanner()
	composer := m.renderComposerPanel()

	availableHeight := m.viewHeight() - lipgloss.Height(header) - lipgloss.Height(startupBanner) - lipgloss.Height(composer)
	if actionPanel != "" {
		availableHeight -= lipgloss.Height(actionPanel)
	}
	if statusBar != "" {
		availableHeight -= lipgloss.Height(statusBar)
	}
	if deviceBanner != "" {
		availableHeight -= lipgloss.Height(deviceBanner)
	}
	if availableHeight < 8 {
		availableHeight = 8
	}

	debug.Log("layout", "vh=%d h=%d s=%d c=%d a=%d sb=%d d=%d",
		m.viewHeight(),
		lipgloss.Height(header), lipgloss.Height(startupBanner), lipgloss.Height(composer),
		lipgloss.Height(actionPanel), lipgloss.Height(statusBar), lipgloss.Height(deviceBanner))
	debug.Log("layout", "avail=%d", availableHeight)

	conversation := m.renderConversationPanel(availableHeight)

	sections := make([]string, 0, 7)
	if header != "" {
		sections = append(sections, header)
	}
	if startupBanner != "" {
		sections = append(sections, startupBanner)
	}
	sections = append(sections, conversation)
	if deviceBanner != "" {
		sections = append(sections, deviceBanner)
	}
	if actionPanel != "" {
		sections = append(sections, actionPanel)
	}
	if statusBar != "" {
		sections = append(sections, statusBar)
	}
	sections = append(sections, composer)
	left := lipgloss.JoinVertical(lipgloss.Left, sections...)
	right := m.renderAuxColumn()
	if right == "" {
		v := tea.NewView(left)
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}
	result := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	v := tea.NewView(result)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) conversationInnerWidth() int {
	width := m.mainColumnWidth() - 4
	if width < 1 {
		width = 1
	}
	return width
}

func conversationInnerHeight(panelHeight int) int {
	innerHeight := panelHeight - 2
	if innerHeight < 3 {
		innerHeight = 3
	}
	return innerHeight
}

func (m Model) conversationPanelHeight() int {
	header := ""
	if m.topHeaderEnabled() {
		header = m.renderHeader()
	}
	startupBanner := m.renderStartupBanner()
	actionPanel := m.renderContextPanel()
	statusBar := m.renderStatusBar()
	deviceBanner := m.renderDeviceCodeBanner()
	composer := m.renderComposerPanel()

	availableHeight := m.viewHeight() - lipgloss.Height(header) - lipgloss.Height(startupBanner) - lipgloss.Height(composer)
	if actionPanel != "" {
		availableHeight -= lipgloss.Height(actionPanel)
	}
	if statusBar != "" {
		availableHeight -= lipgloss.Height(statusBar)
	}
	if deviceBanner != "" {
		availableHeight -= lipgloss.Height(deviceBanner)
	}

	if availableHeight < 8 {
		availableHeight = 8
	}

	return availableHeight
}

func (m Model) renderHeader() string {
	logoWidth := 44
	if m.viewWidth() >= 120 {
		logoWidth = 48
	}
	logoCard := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("12")).
		Padding(1, 1, 0, 1).
		Render(renderHeaderLogo(logoWidth, m.t("header.terminal_native")))

	vendor, endpoint, model := m.currentSelection()
	sessionLine := m.t("label.session") + " " + m.t("session.ephemeral")
	if m.session != nil && m.session.ID != "" {
		sessionLine = fmt.Sprintf("%s %s", m.t("label.session"), truncateString(m.session.ID, 18))
	}
	agentLine := fmt.Sprintf("%s  %s", m.t("label.agents"), m.t("agents.idle"))

	metaCard := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Render(strings.Join([]string{
			m.styles.title.Render("ggcode"),
			lipgloss.NewStyle().Foreground(mutedTextColor).Render(m.t("workspace.tagline")),
			"",
			fmt.Sprintf("%s   %s", m.t("label.vendor"), vendor),
			fmt.Sprintf("%s %s", m.t("label.endpoint"), endpoint),
			fmt.Sprintf("%s   %s", m.t("label.model"), model),
			m.t("label.mode") + "    " + m.renderModeBadge(),
			agentLine,
			sessionLine,
		}, "\n"))

	if m.viewWidth() >= 90 {
		return lipgloss.JoinHorizontal(lipgloss.Top, logoCard, " ", metaCard)
	}
	return lipgloss.JoinVertical(lipgloss.Left, logoCard, metaCard)
}

func (m Model) renderStartupBanner() string {
	if !m.startupBannerVisible {
		return ""
	}
	return m.renderContextBox(m.t("panel.startup"), m.t("startup.banner"), lipgloss.Color("11"))
}

func (m Model) renderSidebar() string {
	if tracker := m.renderSidebarTaskTracker(); tracker != "" {
		return tracker
	}
	vendor, _, model := m.currentSelection()

	body := strings.Join([]string{
		"",
		renderSidebarLogo(m.sidebarWidth()-4, sidebarHomepageURL),
		"",
		m.styles.title.Render("ggcode"),
		m.renderSidebarDetailRow(m.t("label.model"), vendor+"/"+model, m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.branch"), firstNonEmpty(m.sidebarGitBranch(), "-"), m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.skills"), fmt.Sprintf("%d", m.loadedSkillCount()), m.sidebarWidth()-4),
		"",
		m.renderSidebarModeSection(),
		m.renderSidebarUpdateSection(),
		"",
		m.renderSidebarIMSection(),
		"",
		m.renderSwarmSidebar(),
		m.renderSidebarMCPSection(),
	}, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Width(m.boxInnerWidth(m.sidebarWidth())).
		Render(body)
}

func (m Model) renderSidebarModeSection() string {
	width := max(12, m.sidebarWidth()-4)
	rows := []string{
		m.renderSidebarSectionTitle(m.t("panel.mode_policy")),
		m.renderSidebarDetailRow(m.t("label.approval_policy"), m.t(sidebarModeApprovalKey(m.mode)), width),
		m.renderSidebarDetailRow(m.t("label.tool_policy"), m.t(sidebarModeToolsKey(m.mode)), width),
		m.renderSidebarDetailRow(m.t("label.agent_policy"), m.t(sidebarModeAgentKey(m.mode)), width),
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderSidebarContextSection() string {
	width := max(12, m.sidebarWidth()-4)
	rows := []string{m.renderSidebarSectionTitle(m.t("panel.context"))}
	stats, ok := m.sidebarContextStats()
	if !ok {
		rows = append(rows, truncateString(m.t("context.unavailable"), width))
		return strings.Join(rows, "\n")
	}

	rows = append(rows,
		m.renderSidebarDetailRow(m.t("label.window"), humanizeTokenCount(stats.maxTokens), width),
		m.renderSidebarDetailRow(m.t("label.usage"), fmt.Sprintf("%d%%", stats.usagePercent), width),
		m.renderSidebarDetailRow(m.t("label.compact"), fmt.Sprintf("%d%% %s", stats.remainingPercent, m.t("context.until_compact")), width),
	)
	return strings.Join(rows, "\n")
}

func (m Model) renderSidebarMCPSection() string {
	width := max(12, m.sidebarWidth()-4)
	rows := []string{m.renderSidebarSectionTitle(m.t("panel.mcp"))}
	// Filter out disabled MCP servers
	activeServers := make([]MCPInfo, 0, len(m.mcpServers))
	for _, srv := range m.mcpServers {
		if !srv.Disabled {
			activeServers = append(activeServers, srv)
		}
	}
	if len(activeServers) == 0 {
		rows = append(rows, truncateString(m.t("mcp.none"), width))
		return strings.Join(rows, "\n")
	}
	connected, pending, failed := 0, 0, 0
	for _, srv := range activeServers {
		switch {
		case srv.Connected:
			connected++
		case srv.Pending:
			pending++
		default:
			failed++
		}
	}
	rows = append(rows, truncateString(fmt.Sprintf("%d up • %d pending • %d failed", connected, pending, failed), width))
	visibleServers := activeServers
	if len(visibleServers) > 5 {
		visibleServers = visibleServers[:5]
	}
	for _, srv := range visibleServers {
		icon := "✕"
		switch {
		case srv.Connected:
			icon = "✓"
		case srv.Pending:
			icon = "…"
		}
		label := fmt.Sprintf("%s %s (%s)", icon, srv.Name, firstNonEmpty(srv.Transport, "stdio"))
		rows = append(rows, truncateString(label, width))
	}
	if hidden := len(activeServers) - len(visibleServers); hidden > 0 {
		rows = append(rows, truncateString(m.t("mcp.more", hidden), width))
	}
	if active := m.activeMCPToolSummaries(); len(active) > 0 {
		rows = append(rows, "", truncateString(m.t("mcp.active_tools"), width))
		for _, item := range active {
			rows = append(rows, truncateString("• "+item, width))
		}
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderSidebarIMSection() string {
	width := max(12, m.sidebarWidth()-4)
	rows := []string{m.renderSidebarSectionTitle(m.t("panel.im"))}
	if m.imManager == nil && (m.config == nil || !m.config.IM.Enabled) {
		rows = append(rows, truncateString(m.sidebarIMRuntimeStatus(), width))
		return strings.Join(rows, "\n")
	}
	adapters := m.sidebarIMAdapters()
	if len(adapters) == 0 {
		rows = append(rows, truncateString(m.t("im.none"), width))
		return strings.Join(rows, "\n")
	}
	healthy := 0
	for _, state := range adapters {
		if state.Healthy {
			healthy++
		}
	}
	rows = append(rows, truncateString(m.t("im.summary", len(adapters), healthy), width))
	visible := adapters
	if len(visible) > 5 {
		visible = visible[:5]
	}
	for _, state := range visible {
		rows = append(rows, truncateString(sidebarIMAdapterLabel(state), width))
	}
	if hidden := len(adapters) - len(visible); hidden > 0 {
		rows = append(rows, truncateString(m.t("im.more", hidden), width))
	}
	return strings.Join(rows, "\n")
}

func (m Model) sidebarIMSnapshot() (im.StatusSnapshot, bool) {
	if m.imManager == nil {
		return im.StatusSnapshot{}, false
	}
	return m.imManager.Snapshot(), true
}

func (m Model) sidebarIMAdapters() []im.AdapterState {
	statesByName := make(map[string]im.AdapterState)
	mutedAdapters := make(map[string]bool)
	var currentBindings []im.ChannelBinding
	if snapshot, ok := m.sidebarIMSnapshot(); ok {
		currentBindings = snapshot.CurrentBindings
		for _, state := range snapshot.Adapters {
			statesByName[state.Name] = state
		}
		for _, b := range currentBindings {
			if b.Muted {
				mutedAdapters[b.Adapter] = true
			}
		}
	}
	if len(currentBindings) == 0 {
		return nil
	}
	var result []im.AdapterState
	seen := make(map[string]bool)
	for _, binding := range currentBindings {
		name := strings.TrimSpace(binding.Adapter)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if state, ok := statesByName[name]; ok {
			if mutedAdapters[name] {
				state.Status = "muted"
				state.Healthy = false
			}
			result = append(result, state)
		} else if m.config != nil {
			if adapter, ok := m.config.IM.Adapters[name]; ok && adapter.Enabled {
				status := m.t("im.status.not_started")
				if mutedAdapters[name] {
					status = "muted"
				}
				result = append(result, im.AdapterState{
					Name:     name,
					Platform: im.Platform(strings.TrimSpace(adapter.Platform)),
					Status:   status,
				})
			}
		}
	}
	return result
}

func (m Model) sidebarIMRuntimeStatus() string {
	if m.imManager != nil {
		return m.t("im.runtime.available")
	}
	if m.config == nil || !m.config.IM.Enabled {
		return m.t("im.runtime.disabled")
	}
	return m.t("im.runtime.not_started")
}

func sidebarIMAdapterLabel(state im.AdapterState) string {
	icon := "✕"
	switch {
	case state.Healthy:
		icon = "✓"
	case strings.TrimSpace(state.Status) == "muted":
		icon = "○"
	case strings.TrimSpace(state.LastError) == "":
		icon = "…"
	}
	status := compactSingleLine(firstNonEmpty(strings.TrimSpace(state.Status), strings.TrimSpace(state.LastError), "unknown"))
	if status == "" {
		status = "unknown"
	}
	platform := strings.TrimSpace(string(state.Platform))
	if platform == "" {
		platform = "im"
	}
	return fmt.Sprintf("%s %s (%s) %s", icon, firstNonEmpty(strings.TrimSpace(state.Name), "adapter"), platform, status)
}

func (m Model) renderSidebarTaskTracker() string {
	tasks := m.sidebarTrackedTodos()
	if len(tasks) == 0 {
		return ""
	}
	width := max(12, m.sidebarWidth()-4)
	rows := []string{
		"",
		m.renderSidebarSectionTitle(sidebarTaskTrackerTitle(m.currentLanguage())),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(sidebarTaskTrackerHint(m.currentLanguage())),
		"",
	}
	for _, task := range tasks {
		rows = append(rows, m.renderSidebarTaskRow(task, width)...)
	}
	body := strings.Join(rows, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Width(m.boxInnerWidth(m.sidebarWidth())).
		Render(body)
}

func (m Model) sidebarTrackedTodos() []todoStateItem {
	if len(m.todoSnapshot) == 0 {
		return nil
	}
	items := make([]todoStateItem, 0, len(m.todoOrder))
	for _, id := range m.todoOrder {
		if td, ok := m.todoSnapshot[id]; ok {
			items = append(items, td)
		}
	}
	return items
}

func (m Model) renderSidebarTaskRow(task todoStateItem, width int) []string {
	bullet, statusLabel := sidebarTaskStatusDecor(m.currentLanguage(), task.Status)
	titleWidth := max(8, width-2)
	title := compactSingleLine(task.Content)
	if title == "" {
		title = firstNonEmpty(task.ID, "-")
	}
	wrapped := wordwrap.String(title, titleWidth)
	lines := strings.Split(wrapped, "\n")
	rows := make([]string, 0, len(lines)+1)
	for i, line := range lines {
		if i == 0 {
			rows = append(rows, bullet+" "+line)
		} else {
			// Continuation lines aligned with the title text (bullet + space = 2 chars)
			rows = append(rows, "  "+line)
		}
	}
	detail := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  " + statusLabel)
	rows = append(rows, detail)
	return rows
}

func sidebarTaskStatusDecor(lang Language, status string) (string, string) {
	switch status {
	case "done":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("●"), sidebarTaskStatusText(lang, status)
	case "blocked", "failed":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("●"), sidebarTaskStatusText(lang, status)
	case "in_progress":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render("●"), sidebarTaskStatusText(lang, status)
	default: // pending and unknown
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("○"), sidebarTaskStatusText(lang, status)
	}
}

func sidebarTaskStatusText(lang Language, status string) string {
	switch lang {
	case LangZhCN:
		switch status {
		case "done":
			return "已完成"
		case "blocked", "failed":
			return "已失败"
		case "in_progress":
			return "进行中"
		default:
			return "待处理"
		}
	default:
		switch status {
		case "done":
			return "done"
		case "blocked", "failed":
			return "failed"
		case "in_progress":
			return "in progress"
		default:
			return "pending"
		}
	}
}

func sidebarTaskTrackerTitle(lang Language) string {
	if lang == LangZhCN {
		return "当前任务"
	}
	return "Current tasks"
}

func sidebarTaskTrackerHint(lang Language) string {
	if lang == LangZhCN {
		return "活动会话任务追踪（按启动时间倒序）"
	}
	return "Active-session task tracker (newest started first)"
}

func sidebarTaskTrackerMore(lang Language, remaining int) string {
	if lang == LangZhCN {
		return fmt.Sprintf("… 还有 %d 项", remaining)
	}
	return fmt.Sprintf("… %d more", remaining)
}

func (m Model) renderSidebarSectionTitle(title string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("111")).
		Italic(true).
		Bold(true).
		Render(title)
}

func (m Model) renderSidebarDetailRow(label, value string, width int) string {
	const labelWidth = 9
	key := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Width(labelWidth).
		MaxWidth(labelWidth).
		Render(truncateDisplayWidth(label, labelWidth))
	valueWidth := max(1, width-labelWidth-1)
	return lipgloss.JoinHorizontal(lipgloss.Top, key, " ", truncateDisplayWidth(value, valueWidth))
}

func (m Model) renderSidebarBadgeRow(label, badge string) string {
	const labelWidth = 9
	key := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Width(labelWidth).
		MaxWidth(labelWidth).
		Render(truncateDisplayWidth(label, labelWidth))
	return key + " " + badge
}

func truncateDisplayWidth(s string, maxWidth int) string {
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
	return fitDisplayWidth(s, maxWidth-3) + "..."
}

func fitDisplayWidth(s string, maxWidth int) string {
	if maxWidth <= 0 || s == "" {
		return ""
	}
	var b strings.Builder
	currentWidth := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if currentWidth+rw > maxWidth {
			break
		}
		b.WriteRune(r)
		currentWidth += rw
	}
	return b.String()
}

func sidebarModeApprovalKey(mode permission.PermissionMode) string {
	switch mode {
	case permission.PlanMode:
		return "mode.approval.none"
	case permission.AutoMode:
		return "mode.approval.none"
	case permission.BypassMode:
		return "mode.approval.critical"
	case permission.AutopilotMode:
		return "mode.approval.none"
	default:
		return "mode.approval.ask"
	}
}

func sidebarModeToolsKey(mode permission.PermissionMode) string {
	switch mode {
	case permission.PlanMode:
		return "mode.tools.readonly"
	case permission.AutoMode:
		return "mode.tools.safe"
	case permission.BypassMode:
		return "mode.tools.open"
	case permission.AutopilotMode:
		return "mode.tools.open"
	default:
		return "mode.tools.rules"
	}
}

func sidebarModeAgentKey(mode permission.PermissionMode) string {
	switch mode {
	case permission.AutopilotMode:
		return "mode.agent.autocontinue"
	default:
		return "mode.agent.waits"
	}
}

type sidebarContextStatLine struct {
	maxTokens        int
	usagePercent     int
	remainingPercent int
}

func (m Model) sidebarContextStats() (sidebarContextStatLine, bool) {
	if m.agent == nil {
		return sidebarContextStatLine{}, false
	}
	cm := m.agent.ContextManager()
	if cm == nil {
		return sidebarContextStatLine{}, false
	}
	maxTokens := cm.MaxTokens()
	tokenCount := cm.TokenCount()
	threshold := cm.AutoCompactThreshold()
	if maxTokens <= 0 || threshold <= 0 {
		return sidebarContextStatLine{}, false
	}

	usagePercent := int(float64(tokenCount) / float64(maxTokens) * 100)
	if usagePercent < 0 {
		usagePercent = 0
	}
	if usagePercent > 100 {
		usagePercent = 100
	}

	remainingPercent := int(float64(threshold-tokenCount) / float64(threshold) * 100)
	if remainingPercent < 0 {
		remainingPercent = 0
	}
	if remainingPercent > 100 {
		remainingPercent = 100
	}

	return sidebarContextStatLine{
		maxTokens:        maxTokens,
		usagePercent:     usagePercent,
		remainingPercent: remainingPercent,
	}, true
}

func humanizeTokenCount(n int) string {
	if n >= 1000000 && n%1000000 == 0 {
		return fmt.Sprintf("%dm", n/1000000)
	}
	if n >= 1000 && n%1000 == 0 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

func (m Model) sidebarWorkingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return shortenSidebarPath(cwd)
}

func (m Model) sidebarGitBranch() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	branch, err := gitBranchForDir(cwd)
	if err != nil {
		return ""
	}
	return branch
}

func (m Model) loadedSkillCount() int {
	if m.commandMgr == nil {
		return 0
	}
	count := 0
	for _, cmd := range m.commandMgr.Commands() {
		if cmd == nil || !cmd.Enabled {
			continue
		}
		switch cmd.LoadedFrom {
		case commands.LoadedFromSkills, commands.LoadedFromBundled, commands.LoadedFromPlugin:
			count++
		}
	}
	return count
}

func shortenSidebarPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = filepath.ToSlash(filepath.Clean(value))
	home, err := os.UserHomeDir()
	if err == nil {
		home = filepath.ToSlash(filepath.Clean(home))
		if value == home {
			return "~"
		}
		if strings.HasPrefix(value, home+"/") {
			return "~/" + strings.TrimPrefix(value, home+"/")
		}
	}
	return value
}

func gitBranchForDir(start string) (string, error) {
	gitDir, err := resolveGitDir(start)
	if err != nil {
		return "", err
	}
	headBytes, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(headBytes))
	const prefix = "ref: refs/heads/"
	if strings.HasPrefix(head, prefix) {
		return strings.TrimPrefix(head, prefix), nil
	}
	return "", nil
}

func resolveGitDir(start string) (string, error) {
	dir := start
	for {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			if info.IsDir() {
				return gitPath, nil
			}
			data, readErr := os.ReadFile(gitPath)
			if readErr != nil {
				return "", readErr
			}
			line := strings.TrimSpace(string(data))
			const prefix = "gitdir:"
			if !strings.HasPrefix(strings.ToLower(line), prefix) {
				return "", fmt.Errorf("unsupported .git file format")
			}
			target := strings.TrimSpace(line[len(prefix):])
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			return target, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func (m Model) renderConversationPanel(panelHeight int) string {
	innerW := m.conversationInnerWidth()
	innerH := conversationInnerHeight(panelHeight)

	debug.Log("layout", "panel ph=%d iw=%d ih=%d n=%d",
		panelHeight, innerW, innerH, m.chatList.Len())

	if m.chatList != nil {
		m.chatList.SetSize(innerW, innerH)
	} else {
		m.chatList = chat.NewList(innerW, innerH)
	}

	var content string
	if m.chatList.Len() == 0 {
		var sb strings.Builder
		sb.WriteString(m.styles.assistant.Render(m.t("empty.ask")))
		sb.WriteString("\n")
		sb.WriteString(m.styles.prompt.Render(m.t("empty.tips")))
		sb.WriteString("\n\n")
		content = sb.String()
	} else {
		content = m.chatList.Render()
	}

	contentLines := lipgloss.Height(content)
	if contentLines > innerH {
		debug.Log("layout", "OVERFLOW: contentLines=%d > innerH=%d panelH=%d items=%d",
			contentLines, innerH, panelHeight, m.chatList.Len())
	}

	width := m.boxInnerWidth(m.mainColumnWidth())
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("208")).
		Padding(0, 1).
		Width(width).
		Height(panelHeight).
		MaxHeight(panelHeight).
		Render(content)
}

func (m Model) renderApprovalOptions(options []approvalOption, cursor int) string {
	var rows []string
	for i, opt := range options {
		label := fmt.Sprintf("%s (%s)", opt.label, opt.shortcut)
		if i == cursor {
			rows = append(rows, m.styles.approvalCursor.Render(" ❯ "+label))
			continue
		}
		rows = append(rows, m.styles.approvalDim.Render("   "+label))
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderLanguageOptions(options []languageOption, cursor int) string {
	var rows []string
	for i, opt := range options {
		label := fmt.Sprintf("%s (%s)", opt.label, opt.shortcut)
		if i == cursor {
			rows = append(rows, m.styles.approvalCursor.Render(" ❯ "+label))
			continue
		}
		rows = append(rows, m.styles.approvalDim.Render("   "+label))
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderAutoComplete() string {
	if len(m.autoCompleteItems) == 0 {
		return ""
	}

	maxVisible := 8
	start := 0
	if len(m.autoCompleteItems) > maxVisible {
		start = m.autoCompleteIndex
		if start >= len(m.autoCompleteItems)-maxVisible/2 {
			start = len(m.autoCompleteItems) - maxVisible
		}
		if start < 0 {
			start = 0
		}
	}
	end := start + maxVisible
	if end > len(m.autoCompleteItems) {
		end = len(m.autoCompleteItems)
	}

	items := m.autoCompleteItems[start:end]
	maxWidth := 0
	for _, item := range items {
		label := item
		if m.autoCompleteKind == "mention" {
			if strings.HasSuffix(item, "/") {
				label = "📁 " + item
			} else {
				label = "📄 " + item
			}
		}
		if len(label) > maxWidth {
			maxWidth = len(label)
		}
	}

	title := m.t("panel.commands")
	if m.autoCompleteKind == "mention" {
		title = m.t("panel.files")
	}
	var rows []string
	for i, item := range items {
		realIdx := start + i
		selected := realIdx == m.autoCompleteIndex

		label := item
		desc := ""
		if m.autoCompleteKind == "mention" {
			if strings.HasSuffix(item, "/") {
				label = "📁 " + item
				desc = m.t("label.directory")
			} else {
				label = "📄 " + item
				desc = m.t("label.file")
			}
		} else if _, ok := SlashCommandDescriptions[item]; ok {
			desc = localizeSlashDescription(m.currentLanguage(), item)
		} else if cmdName := strings.TrimPrefix(item, "/"); cmdName != item {
			if cmd, ok := m.customCmds[cmdName]; ok {
				desc = cmd.Description
			}
		}

		row := fmt.Sprintf(" %-*s", maxWidth, label)
		if m.autoCompleteKind == "mention" {
			row = fmt.Sprintf(" %d. %-*s", realIdx+1, maxWidth, label)
		}
		if selected {
			row = lipgloss.NewStyle().
				Foreground(lipgloss.Color("226")).
				Background(lipgloss.Color("236")).
				Render(row)
		}
		if desc != "" {
			row += " " + lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(desc)
		}
		rows = append(rows, row)
	}

	hint := " " + m.t("hint.autocomplete")
	if m.autoCompleteKind == "mention" {
		hint = " " + m.t("hint.mention")
	}
	rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(hint))
	return m.renderContextBox(title, strings.Join(rows, "\n"), lipgloss.Color("12"))
}

func (m Model) renderStatusBar() string {
	if !m.loading {
		return ""
	}

	var sb strings.Builder

	activity := m.statusActivity
	if activity == "" {
		if m.activeTodo != nil {
			activity = localizeTodoFocus(m.currentLanguage(), m.activeTodo.Content)
		} else {
			activity = m.t("status.thinking")
		}
	}

	spinnerChar := spinnerFrameGlyph(m.spinner.CurrentFrame())

	line1 := fmt.Sprintf(" %s %s", spinnerChar, activity)
	if count := m.pendingSubmissionCount(); count > 0 {
		line1 += fmt.Sprintf(" │ 📨 %s", m.t("queued.count", count))
	}
	sb.WriteString(m.styles.statusBar.Render(line1))

	if m.activeTodo != nil || m.statusToolCount > 0 || m.statusToolName != "" {
		sb.WriteString("\n ")
		if m.activeTodo != nil {
			sb.WriteString("🎯 ")
			sb.WriteString(truncateString(compactSingleLine(m.activeTodo.Content), 56))
			if m.statusToolCount > 0 || m.statusToolName != "" {
				sb.WriteString(" │ ")
			}
		}
		if m.statusToolCount > 0 {
			sb.WriteString(fmt.Sprintf("🔧 %s", m.t("status.tools_used", m.statusToolCount)))
			if m.statusToolName != "" {
				sb.WriteString(" │ ")
			}
		}
		if m.statusToolName != "" {
			sb.WriteString(m.statusToolName)
			if m.statusToolArg != "" {
				arg := m.statusToolArg
				if len(arg) > 50 {
					arg = arg[:50] + "..."
				}
				sb.WriteString(fmt.Sprintf(": %s", arg))
			}
		}
	}

	return m.renderContextBox("", sb.String(), lipgloss.Color("6"))
}

func (m Model) sidebarActivity() string {
	if m.activeTodo != nil {
		return truncateString(localizeTodoFocus(m.currentLanguage(), m.activeTodo.Content), m.sidebarWidth()-12)
	}
	if m.statusActivity != "" {
		return m.statusActivity
	}
	return m.t("activity.idle")
}

func (m Model) renderContextPanel() string {
	switch {
	case m.modelPanel != nil:
		return m.renderModelPanel()
	case m.tgPanel != nil:
		return m.renderTGPanel()
	case m.qqPanel != nil:
		return m.renderQQPanel()
	case m.pcPanel != nil:
		return m.renderPCPanel()
	case m.discordPanel != nil:
		return m.renderDiscordPanel()
	case m.feishuPanel != nil:
		return m.renderFeishuPanel()
	case m.slackPanel != nil:
		return m.renderSlackPanel()
	case m.dingtalkPanel != nil:
		return m.renderDingtalkPanel()
	case m.imPanel != nil:
		return m.renderIMPanel()
	case m.mcpPanel != nil:
		return m.renderMCPPanel()
	case m.skillsPanel != nil:
		return m.renderSkillsPanel()
	case m.swarmPanel != nil:
		return m.renderSwarmPanel()
	case m.inspectorPanel != nil:
		return m.renderInspectorPanel()
	case m.harnessContextPrompt != nil:
		return m.renderHarnessContextPrompt()
	case m.harnessPanel != nil:
		return m.renderHarnessPanel()
	case m.impersonatePanel != nil:
		return m.renderImpersonatePanel()
	case m.agentDetailPanel != nil:
		return m.renderAgentDetailPanel()
	case m.providerPanel != nil:
		return m.renderProviderPanel()
	case m.pendingPairingChallenge() != nil:
		return m.renderIMPairingPanel()
	case m.pendingQuestionnaire != nil:
		return m.renderQuestionnairePanel()
	case m.pendingApproval != nil:
		title := m.t("panel.approval_required")
		accent := lipgloss.Color("11")
		if m.mode == permission.BypassMode || m.mode == permission.AutopilotMode {
			title = m.t("panel.bypass_approval")
			accent = lipgloss.Color("9")
		}
		present := describeTool(m.currentLanguage(), m.pendingApproval.ToolName, m.pendingApproval.Input)
		toolLine := formatToolInline(present.DisplayName, present.Detail)
		body := fmt.Sprintf(" %s   %s\n %s  %s\n\n%s\n%s",
			m.t("label.tool"),
			toolLine,
			m.t("label.input"),
			truncateString(compactToolArgsPreview(strings.ReplaceAll(m.pendingApproval.Input, "\n", " ")), 220),
			m.renderApprovalOptions(m.approvalOptions, m.approvalCursor),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" Tab/j/k move • Enter confirm • y/n/a shortcuts"),
		)
		return m.renderContextBox(title, body, accent)
	case m.pendingDiffConfirm != nil:
		body := fmt.Sprintf(" %s   %s\n\n%s\n\n%s\n%s",
			m.t("label.file"),
			displayToolFileTarget(m.pendingDiffConfirm.FilePath),
			truncateLines(strings.TrimSpace(FormatDiff(m.pendingDiffConfirm.DiffText)), 12),
			m.renderApprovalOptions(m.diffOptions, m.diffCursor),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" Tab/j/k move • Enter confirm • y/n shortcuts"),
		)
		return m.renderContextBox(m.t("panel.review_file_change"), body, lipgloss.Color("13"))
	case m.pendingHarnessCheckpointConfirm != nil:
		checkpoint := m.pendingHarnessCheckpointConfirm.Checkpoint
		body := fmt.Sprintf(" Dirty workspace\n\n %s\n %s   %s\n\n%s\n%s",
			truncateLines(strings.TrimSpace(checkpoint.Summary), 6),
			"commit",
			checkpoint.CommitMessage,
			m.renderApprovalOptions(m.diffOptions, m.diffCursor),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" Tab/j/k move • Enter confirm • y/n shortcuts"),
		)
		return m.renderContextBox("Confirm harness checkpoint", body, lipgloss.Color("13"))
	case len(m.langOptions) > 0:
		title := languageSwitchLabel(m.currentLanguage())
		bodyLine := m.t("lang.selection.current", m.languageLabel())
		hint := m.t("lang.selection.hint")
		accent := lipgloss.Color("10")
		if m.languagePromptRequired {
			title = m.t("lang.first_use.title")
			bodyLine = m.t("lang.first_use.body")
			hint = m.t("lang.first_use.hint")
			accent = lipgloss.Color("11")
		}
		body := fmt.Sprintf("%s\n\n%s\n%s",
			bodyLine,
			m.renderLanguageOptions(m.langOptions, m.langCursor),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(hint),
		)
		return m.renderContextBox(title, body, accent)
	case m.autoCompleteActive && len(m.autoCompleteItems) > 0:
		return m.renderAutoComplete()
	default:
		return ""
	}
}

func (m Model) renderIMPairingPanel() string {
	challenge := m.pendingPairingChallenge()
	if challenge == nil {
		return ""
	}
	platformName := platformDisplayName(challenge.Platform)
	title := fmt.Sprintf("%s pairing required", platformName)
	bodyText := fmt.Sprintf("A %s channel is requesting to bind this workspace. Ask the user to enter the 4-digit code shown below in %s.", platformName, platformName)
	channelLabel := "request channel"
	boundLabel := "currently bound"
	codeLabel := "pairing code"
	hint := fmt.Sprintf("Esc reject • the correct code in %s will complete binding automatically", platformName)
	if m.currentLanguage() == LangZhCN {
		cnName := platformCNName(challenge.Platform)
		title = fmt.Sprintf("%s 绑定验证", cnName)
		bodyText = fmt.Sprintf("有一个 %s 渠道正在请求绑定当前工作区。请让用户在 %s 中输入下方 4 位绑定码。", cnName, cnName)
		channelLabel = "请求渠道"
		boundLabel = "当前绑定"
		codeLabel = "绑定码"
		hint = fmt.Sprintf("Esc 拒绝 • %s 中输入正确绑定码后会自动完成绑定", cnName)
	}
	if challenge.Kind == im.PairingKindRebind {
		if m.currentLanguage() == LangZhCN {
			cnName := platformCNName(challenge.Platform)
			title = fmt.Sprintf("%s 重新绑定验证", cnName)
			bodyText = fmt.Sprintf("当前 bot 已经绑定到其他渠道。新渠道在 %s 中输入下方 4 位绑定码后，将解绑旧渠道并切换到当前渠道。", cnName)
		} else {
			title = fmt.Sprintf("%s rebind requested", platformName)
			bodyText = fmt.Sprintf("This bot is already bound to another channel. Entering the 4-digit code below in %s will unbind the old channel and switch to the new channel.", platformName)
		}
	}

	codeDigits := strings.Join(strings.Split(challenge.Code, ""), "   ")
	codeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("57")).
		Padding(1, 3).
		Margin(1, 0).
		Render(codeDigits)

	lines := []string{bodyText, ""}
	lines = append(lines, fmt.Sprintf(" %s   %s", channelLabel, firstNonEmpty(strings.TrimSpace(challenge.ChannelID), "-")))
	if challenge.ExistingBinding != nil && strings.TrimSpace(challenge.ExistingBinding.ChannelID) != "" {
		lines = append(lines, fmt.Sprintf(" %s   %s", boundLabel, challenge.ExistingBinding.ChannelID))
	}
	lines = append(lines,
		"",
		lipgloss.NewStyle().Bold(true).Render(codeLabel),
		codeStyle,
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(hint),
	)
	return m.renderContextBox(title, strings.Join(lines, "\n"), lipgloss.Color("11"))
}

func platformDisplayName(p im.Platform) string {
	switch p {
	case im.PlatformQQ:
		return "QQ"
	case im.PlatformFeishu:
		return "Feishu"
	case im.PlatformTelegram:
		return "Telegram"
	case im.PlatformDiscord:
		return "Discord"
	case im.PlatformDingTalk:
		return "DingTalk"
	case im.PlatformSlack:
		return "Slack"
	default:
		return "IM"
	}
}

func platformCNName(p im.Platform) string {
	switch p {
	case im.PlatformQQ:
		return "QQ"
	case im.PlatformFeishu:
		return "飞书"
	case im.PlatformTelegram:
		return "Telegram"
	case im.PlatformDiscord:
		return "Discord"
	case im.PlatformDingTalk:
		return "钉钉"
	case im.PlatformSlack:
		return "Slack"
	default:
		return "IM"
	}
}

func (m Model) renderAuxColumn() string {
	if m.sidebarEnabled() {
		return m.renderSidebar()
	}
	return ""
}

func (m Model) renderComposerPanel() string {
	accent := m.modeColor()
	hints := []string{
		m.t("hint.mode") + " " + m.renderModeBadge(),
		m.t("hint.enter_send"),
		m.t("hint.help"),
	}
	if !m.autoCompleteActive {
		hints = append(hints, m.t("hint.shift_tab_mode"))
	}
	if m.loading {
		hints = append(hints, m.t("hint.ctrlc_cancel"))
	} else {
		hints = append(hints, m.t("hint.ctrlc_exit"))
	}
	if count := m.pendingSubmissionCount(); count > 0 {
		hints = append(hints, m.t("queued.count", count))
	}
	if m.pendingImage != nil {
		hints = append(hints, m.t("hint.image_attached"))
	}

	body := m.renderComposerInput() + "\n" +
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(strings.Join(hints, " • "))
	width := m.boxInnerWidth(m.mainColumnWidth())
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(0, 1).
		Width(width).
		Render(body)
}

func (m Model) renderComposerInput() string {
	return m.input.View()
}

func (m Model) renderContextBox(title, body string, accent color.Color) string {
	width := m.boxInnerWidth(m.mainColumnWidth())
	content := body
	if strings.TrimSpace(title) != "" {
		content = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(" "+title) + "\n" + body
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Width(width).
		Render(content)
}

func (m Model) currentSelection() (string, string, string) {
	vendor := firstNonEmptyValue(m.activeVendor, m.startupVendor)
	endpoint := firstNonEmptyValue(m.activeEndpoint, m.startupEndpoint)
	model := firstNonEmptyValue(m.activeModel, m.startupModel)
	if vendor == "" {
		vendor = "unknown"
	}
	if endpoint == "" {
		endpoint = "unknown"
	}
	if model == "" {
		model = "unknown"
	}
	return vendor, endpoint, model
}

func (m Model) viewWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 80
}

func (m Model) viewHeight() int {
	if m.height > 0 {
		return m.height
	}
	return 24
}

func (m Model) modeColor() color.Color {
	switch m.mode {
	case permission.SupervisedMode:
		return lipgloss.Color("220")
	case permission.PlanMode:
		return lipgloss.Color("39")
	case permission.AutoMode:
		return lipgloss.Color("42")
	case permission.BypassMode:
		return lipgloss.Color("196")
	case permission.AutopilotMode:
		return lipgloss.Color("129")
	default:
		return lipgloss.Color("8")
	}
}

func (m Model) renderModeBadge() string {
	fg := lipgloss.Color("0")
	if m.mode == permission.PlanMode || m.mode == permission.BypassMode || m.mode == permission.AutopilotMode {
		fg = lipgloss.Color("15")
	}
	return lipgloss.NewStyle().
		Foreground(fg).
		Background(m.modeColor()).
		Bold(true).
		Padding(0, 1).
		Render(m.mode.String())
}

func (m Model) sidebarWidth() int {
	if m.viewWidth() >= 156 {
		return 44
	}
	return 40
}

func (m Model) terminalLeftMargin() int {
	return 0
}

func (m Model) terminalRightMargin() int {
	return 0
}

func (m Model) boxInnerWidth(totalWidth int) int {
	innerWidth := totalWidth
	return innerWidth
}

func (m Model) sidebarEnabled() bool {
	return m.sidebarVisible && m.sidebarAvailableByWidth()
}

func (m Model) topHeaderEnabled() bool {
	// No longer show the two-box header; logo is embedded in conversation.
	return false
}

// narrowMode returns true when the sidebar cannot fit and we're in the main view.
func (m Model) narrowMode() bool {
	return !m.sidebarEnabled()
}

func (m Model) sidebarAvailableByWidth() bool {
	required := 72 + m.sidebarWidth() + 1 + m.terminalLeftMargin() + m.terminalRightMargin()
	return m.viewWidth() >= required
}

func (m Model) mainColumnWidth() int {
	if !m.sidebarEnabled() {
		width := m.viewWidth() - m.terminalLeftMargin() - m.terminalRightMargin()
		if width < 1 {
			width = 1
		}
		return width
	}
	width := m.viewWidth() - m.sidebarWidth() - 1 - m.terminalLeftMargin() - m.terminalRightMargin()
	if width < 1 {
		width = 1
	}
	return width
}

func truncateLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n") + "\n..."
}

func (m Model) helpText() string {
	return m.t("help.text")
}
