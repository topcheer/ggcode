package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/commands"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/permission"
)

func (m Model) View() string {
	if m.quitting {
		return ""
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

	sections := []string{header}
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
	if !m.sidebarEnabled() {
		return left
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", m.renderSidebar())
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
	if output == "" && !m.loading && m.pendingApproval == nil && m.pendingDiffConfirm == nil {
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
	staticPrefix := bulletStyle.Render("● ")
	if !strings.HasPrefix(output[m.streamStartPos:], staticPrefix) {
		return output
	}
	animatedPrefix := bulletStyle.Render(streamingBulletFrame(m.spinner.CurrentFrame()) + " ")
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
		BorderForeground(lipgloss.Color("8")).
		Padding(0, 1).
		Render(strings.Join([]string{
			m.styles.title.Render("ggcode"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.t("workspace.tagline")),
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
	vendor, endpoint, model := m.currentSelection()
	sessionLine := m.t("session.ephemeral")
	if m.session != nil && m.session.ID != "" {
		sessionLine = truncateString(m.session.ID, 18)
	}
	agentLine := m.t("agents.idle")
	activity := m.sidebarActivity()
	if len(m.pendingSubmissions) > 0 {
		activity = fmt.Sprintf("%s • %s", activity, m.t("queued.count", len(m.pendingSubmissions)))
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
		"",
		m.renderSidebarMCPSection(),
	}, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(0, 1).
		Width(m.sidebarWidth()).
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
		Render(fmt.Sprintf("%-*s", labelWidth, truncateString(label, labelWidth)))
	valueWidth := max(1, width-labelWidth-1)
	return key + " " + truncateString(value, valueWidth)
}

func (m Model) renderSidebarBadgeRow(label, badge string) string {
	const labelWidth = 9
	key := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Render(fmt.Sprintf("%-*s", labelWidth, truncateString(label, labelWidth)))
	return key + " " + badge
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
	threshold := ctxpkg.AutoCompactThresholdTokens(maxTokens)
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
	content := vp.View()
	body := lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true).Render(" "+m.t("panel.conversation")) + "\n" + content
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("208")).
		Padding(0, 1).
		Width(m.mainColumnWidth()).
		Height(panelHeight).
		Render(body)
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

		row := fmt.Sprintf(" %d. %-*s", realIdx+1, maxWidth, label)
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

	spinnerChar := "⏳"
	if m.spinner.IsActive() {
		frame := m.spinner.CurrentFrame()
		spinnerChar = spinnerFrameGlyph(frame)
	}

	line1 := fmt.Sprintf(" %s %s", spinnerChar, activity)
	if len(m.pendingSubmissions) > 0 {
		line1 += fmt.Sprintf(" │ 📨 %s", m.t("queued.count", len(m.pendingSubmissions)))
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

	return m.renderContextBox(m.t("panel.agent_status"), sb.String(), lipgloss.Color("6"))
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
	case m.mcpPanel != nil:
		return m.renderMCPPanel()
	case m.skillsPanel != nil:
		return m.renderSkillsPanel()
	case m.providerPanel != nil:
		return m.renderProviderPanel()
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

func (m Model) renderComposerPanel() string {
	accent := m.modeColor()
	title := " " + m.t("panel.composer")
	if m.pendingApproval != nil || m.pendingDiffConfirm != nil || m.providerPanel != nil || m.mcpPanel != nil || len(m.langOptions) > 0 {
		title = " " + m.t("panel.composer_locked")
	}

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
	if len(m.pendingSubmissions) > 0 {
		hints = append(hints, m.t("queued.count", len(m.pendingSubmissions)))
	}
	if m.pendingImage != nil {
		hints = append(hints, m.t("hint.image_attached"))
	}

	body := lipgloss.NewStyle().Bold(true).Render(m.input.View()) + "\n" +
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(strings.Join(hints, " • "))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(0, 1).
		Width(m.mainColumnWidth()).
		Render(lipgloss.NewStyle().Foreground(accent).Bold(true).Render(title) + "\n" + body)
}

func (m Model) renderContextBox(title, body string, accent lipgloss.Color) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(0, 1).
		Width(m.mainColumnWidth()).
		Render(lipgloss.NewStyle().Foreground(accent).Bold(true).Render(" "+title) + "\n" + body)
}

func (m Model) currentSelection() (string, string, string) {
	vendor := m.startupVendor
	endpoint := m.startupEndpoint
	model := m.startupModel
	if m.config != nil {
		if m.config.Vendor != "" {
			vendor = m.config.Vendor
		}
		if m.config.Endpoint != "" {
			endpoint = m.config.Endpoint
		}
		if m.config.Model != "" {
			model = m.config.Model
		}
		if resolved, err := m.config.ResolveActiveEndpoint(); err == nil {
			vendor = resolved.VendorName
			endpoint = resolved.EndpointName
			model = resolved.Model
		}
	}
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

func (m Model) sidebarEnabled() bool {
	return m.sidebarVisible && m.sidebarAvailableByWidth()
}

func (m Model) topHeaderEnabled() bool {
	return m.sidebarVisible && !m.sidebarEnabled()
}

func (m Model) sidebarAvailableByWidth() bool {
	required := 72 + m.sidebarWidth() + 1
	return m.viewWidth() >= required
}

func (m Model) mainColumnWidth() int {
	if !m.sidebarEnabled() {
		return m.viewWidth()
	}
	width := m.viewWidth() - m.sidebarWidth() - 1
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
