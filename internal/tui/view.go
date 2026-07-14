package tui

import (
	"fmt"
	"image/color"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/util"
)

var (
	chromeBorderColor = compat.AdaptiveColor{Light: lipgloss.Color("240"), Dark: lipgloss.Color("250")}
	mutedTextColor    = compat.AdaptiveColor{Light: lipgloss.Color("240"), Dark: lipgloss.Color("248")}
)

func (m Model) View() tea.View {
	m.syncAsyncStateCaches()
	if m.quitting {
		return tea.NewView("")
	}
	header := ""
	if m.topHeaderEnabled() {
		header = m.renderHeader()
	}
	actionPanel := m.renderContextPanel()
	statusBar := m.renderStatusBar()
	deviceBanner := m.renderDeviceCodeBanner()
	composer := m.renderComposerPanel()

	availableHeight := m.viewHeight() - lipgloss.Height(header) - lipgloss.Height(composer)
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

	lanChatBar := m.renderLanChatNotice()
	if lanChatBar != "" {
		availableHeight -= lipgloss.Height(lanChatBar)
	}

	if debug.IsVerbose("layout") {
		debug.Log("layout", "vh=%d h=%d c=%d a=%d sb=%d d=%d",
			m.viewHeight(),
			lipgloss.Height(header), lipgloss.Height(composer),
			lipgloss.Height(actionPanel), lipgloss.Height(statusBar), lipgloss.Height(deviceBanner))
		debug.Log("layout", "avail=%d", availableHeight)
	}

	conversation := m.renderConversationPanel(availableHeight)

	sections := make([]string, 0, 8)
	if lanChatBar != "" {
		sections = append(sections, lanChatBar)
	}
	if header != "" {
		sections = append(sections, header)
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

	// Pet row: rendered right-aligned on its own line below the composer.
	pet := m.renderPet()
	if pet != "" {
		leftWidth := m.mainColumnWidth()
		sections = append(sections, lipgloss.PlaceHorizontal(leftWidth, lipgloss.Right, pet))
	}

	left := lipgloss.JoinVertical(lipgloss.Left, sections...)
	right := m.renderAuxColumn()
	var content string
	if right == "" {
		content = left
	} else {
		content = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	}

	// Cache view snapshot for streaming goroutine (thread-safe via pointer)
	if m.streamViewState != nil {
		m.streamViewState.setSnapshot(content)
	}
	v := tea.NewView(content)
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
	actionPanel := m.renderContextPanel()
	statusBar := m.renderStatusBar()
	deviceBanner := m.renderDeviceCodeBanner()
	composer := m.renderComposerPanel()

	availableHeight := m.viewHeight() - lipgloss.Height(header) - lipgloss.Height(composer)
	if actionPanel != "" {
		availableHeight -= lipgloss.Height(actionPanel)
	}
	if statusBar != "" {
		availableHeight -= lipgloss.Height(statusBar)
	}
	if deviceBanner != "" {
		availableHeight -= lipgloss.Height(deviceBanner)
	}

	lanChatBar := m.renderLanChatNotice()
	if lanChatBar != "" {
		availableHeight -= lipgloss.Height(lanChatBar)
	}

	if availableHeight < 8 {
		availableHeight = 8
	}

	return availableHeight
}
func (m Model) renderContextBox(title, body string, accent color.Color) string {
	width := m.boxInnerWidth(m.mainColumnWidth())
	content := body
	if strings.TrimSpace(title) != "" {
		content = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(" "+title) + "\n" + body
	}
	// Add scope indicator if any panel is open
	scopeLine := ""
	if m.config != nil && m.config.HasInstanceConfigAttached() && m.isAnyPanelOpen() {
		scopeLabel := m.configSaveScopeLabel()
		scopeColor := lipgloss.Color("8") // gray for global
		if m.configSaveScope == "instance" {
			scopeColor = lipgloss.Color("14") // cyan for instance
		}
		hint := ""
		if m.configSaveScope == "instance" {
			if _, err := os.Stat(m.config.InstanceDirPath()); os.IsNotExist(err) {
				hint = m.t("config.save_target_new_hint")
			}
		}
		scopeLine = "\n" + lipgloss.NewStyle().Foreground(scopeColor).Render(
			fmt.Sprintf(m.t("config.save_target_line"), scopeLabel, hint))
	}
	content += scopeLine
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Width(width).
		Render(content)
}
func (m Model) isAnyPanelOpen() bool {
	return m.modelPanel != nil || m.providerPanel != nil ||
		m.qqPanel != nil || m.tgPanel != nil || m.pcPanel != nil ||
		m.discordPanel != nil || m.feishuPanel != nil || m.slackPanel != nil ||
		m.dingtalkPanel != nil || m.imPanel != nil || m.mcpPanel != nil ||
		m.impersonatePanel != nil ||
		m.wechatPanel != nil || m.wecomPanel != nil ||
		m.matrixPanel != nil || m.mattermostPanel != nil ||
		m.signalPanel != nil || m.ircPanel != nil ||
		m.nostrPanel != nil || m.twitchPanel != nil ||
		m.whatsappPanel != nil || m.streamPanel != nil ||
		m.inspectorPanel != nil || m.harnessContextPrompt != nil ||
		m.harnessPanel != nil || m.lanChatPanel != nil ||
		m.skillsPanel != nil || m.hooksPanel != nil
}

func (m Model) currentSelection() (string, string, string) {
	vendor := util.FirstNonEmpty(m.activeVendor, m.startupVendor)
	endpoint := util.FirstNonEmpty(m.activeEndpoint, m.startupEndpoint)
	model := util.FirstNonEmpty(m.activeModel, m.startupModel)
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
	if m.viewWidth() >= 180 {
		return 38
	}
	if m.viewWidth() >= 140 {
		return 36
	}
	return 34
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

// formatDuration renders a duration in a compact form: 5s, 1m12s, 2h03m
func formatDuration(d time.Duration) string {
	d = d.Truncate(time.Second)
	if d < time.Minute {
		return d.String()
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%02dm", h, m)
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
