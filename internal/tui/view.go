package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/permission"
)

var (
	chromeBorderColor = lipgloss.AdaptiveColor{Light: "240", Dark: "250"}
	mutedTextColor    = lipgloss.AdaptiveColor{Light: "240", Dark: "248"}
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.previewPanel != nil {
		return m.renderPreviewPanel()
	}

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

	conversation := m.renderConversationPanel(availableHeight)

	sections := make([]string, 0, 6)
	if header != "" {
		sections = append(sections, header)
	}
	if startupBanner != "" {
		sections = append(sections, startupBanner)
	}
	sections = append(sections, conversation)
	if actionPanel != "" {
		sections = append(sections, actionPanel)
	}
	if statusBar != "" {
		sections = append(sections, statusBar)
	}
	sections = append(sections, composer)
	left := lipgloss.JoinVertical(lipgloss.Left, sections...)
	right := m.renderAuxColumn(lipgloss.Height(left))
	if right == "" {
		return left
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
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

func (m Model) conversationViewport() ViewportModel {
	vp := m.viewport
	panelHeight := m.conversationPanelHeight()
	vp.SetSize(m.conversationInnerWidth(), conversationInnerHeight(panelHeight))
	vp.SetContent(m.renderOutput())
	return vp
}

func (m Model) conversationPanelHeight() int {
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

	return availableHeight
}

func (m Model) renderOutput() string {
	var sb strings.Builder
	output := m.output.String()
	if output == "" && !m.loading && m.pendingApproval == nil && m.pendingDiffConfirm == nil && m.pendingHarnessCheckpointConfirm == nil {
		sb.WriteString(m.styles.assistant.Render(m.t("empty.ask")))
		sb.WriteString("\n")
		sb.WriteString(m.styles.prompt.Render(m.t("empty.tips")))
		sb.WriteString("\n\n")
		return sb.String()
	}
	output = m.decorateStreamingBullet(output)
	output = strings.TrimRight(output, "\n")
	if output != "" {
		sb.WriteString(output)
	}
	liveActivities := m.renderLiveActivities()
	if liveActivities != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(liveActivities)
	}
	if m.loading && m.spinner.IsActive() {
		sb.WriteString("\n")
		sb.WriteString(m.spinner.String())
	} else if m.loading {
		sb.WriteString("▌")
	}
	sb.WriteString("\n\n")
	return sb.String()
}

func (m Model) decorateStreamingBullet(output string) string {
	if !m.loading || !m.streamPrefixWritten || m.streamStartPos < 0 || m.streamStartPos >= len(output) {
		return output
	}
	staticPrefix := assistantBulletStyle.Render("● ")
	if !strings.HasPrefix(output[m.streamStartPos:], staticPrefix) {
		return output
	}
	animatedPrefix := assistantBulletStyle.Render(streamingBulletFrame(m.spinner.CurrentFrame()) + " ")
	return output[:m.streamStartPos] + animatedPrefix + output[m.streamStartPos+len(staticPrefix):]
}

func streamingBulletFrame(frame int) string {
	frames := []string{"●", "◉", "○", "◉"}
	return frames[frame%len(frames)]
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

func (m Model) renderSidebar(totalHeight int) string {
	if tracker := m.renderSidebarTaskTracker(totalHeight); tracker != "" {
		return tracker
	}
	vendor, endpoint, model := m.currentSelection()
	sessionLine := m.t("session.ephemeral")
	if m.session != nil && m.session.ID != "" {
		sessionLine = truncateString(m.session.ID, 18)
	}
	agentLine := m.t("agents.idle")
	activity := m.sidebarActivity()
	if count := m.pendingSubmissionCount(); count > 0 {
		activity = fmt.Sprintf("%s • %s", activity, m.t("queued.count", count))
	}

	body := strings.Join([]string{
		"",
		renderSidebarLogo(m.sidebarWidth()-4, m.t("workspace.tagline")),
		"",
		m.styles.title.Render("ggcode"),
		m.renderSidebarDetailRow(m.t("label.vendor"), vendor, m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.endpoint"), endpoint, m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.model"), model, m.sidebarWidth()-4),
		m.renderSidebarBadgeRow(m.t("label.mode"), m.renderModeBadge()),
		m.renderSidebarDetailRow(m.t("label.session"), sessionLine, m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.agents"), agentLine, m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.cwd"), m.sidebarWorkingDirectory(), m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.branch"), firstNonEmpty(m.sidebarGitBranch(), "-"), m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.skills"), fmt.Sprintf("%d", m.loadedSkillCount()), m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.activity"), activity, m.sidebarWidth()-4),
		"",
		m.renderSidebarContextSection(),
		"",
		m.renderSidebarModeSection(),
		m.renderSidebarUpdateSection(),
		"",
		m.renderSidebarMCPSection(),
	}, "\n")
	innerHeight := max(lipgloss.Height(body), totalHeight-2)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Height(innerHeight).
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
	if len(m.mcpServers) == 0 {
		rows = append(rows, truncateString(m.t("mcp.none"), width))
		return strings.Join(rows, "\n")
	}
	connected, pending, failed := 0, 0, 0
	for _, srv := range m.mcpServers {
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
	visibleServers := m.mcpServers
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
	if hidden := len(m.mcpServers) - len(visibleServers); hidden > 0 {
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

func (m Model) renderSidebarTaskTracker(totalHeight int) string {
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
	maxRows := max(6, totalHeight-4)
	usedRows := len(rows)
	for i, task := range tasks {
		if usedRows+2 > maxRows {
			remaining := len(tasks) - i
			rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(sidebarTaskTrackerMore(m.currentLanguage(), remaining)))
			break
		}
		rows = append(rows, m.renderSidebarTaskRow(task, width)...)
		usedRows += 2
	}
	body := strings.Join(rows, "\n")
	innerHeight := max(lipgloss.Height(body), totalHeight-2)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Height(innerHeight).
		Width(m.boxInnerWidth(m.sidebarWidth())).
		Render(body)
}

func (m Model) sidebarTrackedTodos() []todoStateItem {
	if len(m.todoSnapshot) == 0 {
		return nil
	}
	items := make([]todoStateItem, 0, len(m.todoSnapshot))
	for _, td := range m.todoSnapshot {
		switch td.Status {
		case "in_progress", "done", "blocked", "failed":
			items = append(items, td)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i].StartedAt
		if left.IsZero() {
			left = items[i].UpdatedAt
		}
		right := items[j].StartedAt
		if right.IsZero() {
			right = items[j].UpdatedAt
		}
		if left.Equal(right) {
			return items[i].Content > items[j].Content
		}
		return left.After(right)
	})
	return items
}

func (m Model) renderSidebarTaskRow(task todoStateItem, width int) []string {
	bullet, statusLabel := sidebarTaskStatusDecor(m.currentLanguage(), task.Status)
	titleWidth := max(8, width-2)
	title := truncateDisplayWidth(compactSingleLine(task.Content), titleWidth)
	if title == "" {
		title = firstNonEmpty(task.ID, "-")
	}
	detail := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  " + statusLabel)
	return []string{bullet + " " + title, detail}
}

func sidebarTaskStatusDecor(lang Language, status string) (string, string) {
	switch status {
	case "done":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("●"), sidebarTaskStatusText(lang, status)
	case "blocked", "failed":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("●"), sidebarTaskStatusText(lang, status)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render("●"), sidebarTaskStatusText(lang, status)
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
		default:
			return "进行中"
		}
	default:
		switch status {
		case "done":
			return "done"
		case "blocked", "failed":
			return "failed"
		default:
			return "in progress"
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
		if cmd == nil {
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
	vp := m.conversationViewport()
	content := m.decoratePreviewTargets(vp.View())
	width := m.boxInnerWidth(m.mainColumnWidth())
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("208")).
		Padding(0, 1).
		Width(width).
		Height(panelHeight).
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
	case m.mcpPanel != nil:
		return m.renderMCPPanel()
	case m.skillsPanel != nil:
		return m.renderSkillsPanel()
	case m.inspectorPanel != nil:
		return m.renderInspectorPanel()
	case m.harnessContextPrompt != nil:
		return m.renderHarnessContextPrompt()
	case m.harnessPanel != nil:
		return m.renderHarnessPanel()
	case m.providerPanel != nil:
		return m.renderProviderPanel()
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

func (m Model) renderAuxColumn(totalHeight int) string {
	if m.sidebarEnabled() {
		return m.renderSidebar(totalHeight)
	}
	return ""
}

func (m Model) renderComposerPanel() string {
	accent := m.modeColor()
	hints := []string{
		m.t("hint.mode") + " " + m.renderModeBadge(),
		m.t("hint.enter_send"),
		m.t("hint.ctrlv_image"),
		m.t("hint.ctrlr_sidebar"),
		m.t("hint.help"),
		m.t("hint.add_context"),
		m.t("hint.scroll"),
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

	body := lipgloss.NewStyle().Bold(true).Render(m.input.View()) + "\n" +
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(strings.Join(hints, " • "))
	width := m.boxInnerWidth(m.mainColumnWidth())
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(0, 1).
		Width(width).
		Render(body)
}

func (m Model) renderContextBox(title, body string, accent lipgloss.Color) string {
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

func (m Model) modeColor() lipgloss.Color {
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

func (m Model) terminalRightMargin() int {
	return 1
}

func (m Model) boxInnerWidth(totalWidth int) int {
	innerWidth := totalWidth - 2
	if innerWidth < 1 {
		return 1
	}
	return innerWidth
}

func (m Model) sidebarEnabled() bool {
	return m.sidebarVisible && m.sidebarAvailableByWidth()
}

func (m Model) topHeaderEnabled() bool {
	return m.sidebarVisible && !m.sidebarEnabled()
}

func (m Model) sidebarAvailableByWidth() bool {
	required := 72 + m.sidebarWidth() + 1 + m.terminalRightMargin()
	return m.viewWidth() >= required
}

func (m Model) mainColumnWidth() int {
	if !m.sidebarEnabled() {
		width := m.viewWidth() - m.terminalRightMargin()
		if width < 1 {
			width = 1
		}
		return width
	}
	width := m.viewWidth() - m.sidebarWidth() - 1 - m.terminalRightMargin()
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
