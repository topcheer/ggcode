package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/permission"
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	header := ""
	if !m.sidebarEnabled() {
		header = m.renderHeader()
	}
	actionPanel := m.renderContextPanel()
	statusBar := m.renderStatusBar()
	composer := m.renderComposerPanel()

	availableHeight := m.viewHeight() - lipgloss.Height(header) - lipgloss.Height(composer)
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

	sections := []string{header, conversation}
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
	if !m.sidebarEnabled() {
		header = m.renderHeader()
	}
	actionPanel := m.renderContextPanel()
	statusBar := m.renderStatusBar()
	composer := m.renderComposerPanel()

	availableHeight := m.viewHeight() - lipgloss.Height(header) - lipgloss.Height(composer)
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
	logoCard := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("12")).
		Padding(0, 1).
		Render(
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(asciiLogo()) +
				lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.t("header.terminal_native")),
		)

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
		lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(asciiLogo()),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.t("workspace.tagline")),
		"",
		m.styles.title.Render("ggcode"),
		fmt.Sprintf("%-9s %s", m.t("label.vendor"), vendor),
		fmt.Sprintf("%-9s %s", m.t("label.endpoint"), truncateString(endpoint, m.sidebarWidth()-12)),
		fmt.Sprintf("%-9s %s", m.t("label.model"), truncateString(model, m.sidebarWidth()-12)),
		fmt.Sprintf("%-9s %s", m.t("label.mode"), m.renderModeBadge()),
		fmt.Sprintf("%-9s %s", m.t("label.session"), sessionLine),
		fmt.Sprintf("%-9s %s", m.t("label.agents"), agentLine),
		fmt.Sprintf("%-9s %s", m.t("label.activity"), truncateString(activity, m.sidebarWidth()-12)),
		"",
		m.renderSidebarModeSection(),
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
		m.styles.title.Render(m.t("panel.mode_policy")),
		formatSidebarDetailRow(m.t("label.approval_policy"), m.t(sidebarModeApprovalKey(m.mode)), width),
		formatSidebarDetailRow(m.t("label.tool_policy"), m.t(sidebarModeToolsKey(m.mode)), width),
		formatSidebarDetailRow(m.t("label.agent_policy"), m.t(sidebarModeAgentKey(m.mode)), width),
	}
	return strings.Join(rows, "\n")
}

func formatSidebarDetailRow(label, value string, width int) string {
	return truncateString(fmt.Sprintf("%-8s %s", label, value), width)
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

func (m Model) renderConversationPanel(panelHeight int) string {
	vp := m.conversationViewport()
	content := vp.View()
	body := lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.conversation")) + "\n" + content
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
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
		spinnerChar = string(spinnerChars[frame%len(spinnerChars)])
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
	case m.autoCompleteActive && len(m.autoCompleteItems) > 0:
		return m.renderAutoComplete()
	default:
		return ""
	}
}

func (m Model) renderComposerPanel() string {
	title := " " + m.t("panel.composer")
	if m.pendingApproval != nil || m.pendingDiffConfirm != nil || m.providerPanel != nil {
		title = " " + m.t("panel.composer_locked")
	}

	hints := []string{
		m.t("hint.mode") + " " + m.renderModeBadge(),
		m.t("hint.enter_send"),
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
		BorderForeground(lipgloss.Color("12")).
		Padding(0, 1).
		Width(m.mainColumnWidth()).
		Render(lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(title) + "\n" + body)
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
