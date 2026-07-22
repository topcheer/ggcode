package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/metrics"
)

type statsPanelState struct {
	viewport ViewportModel
}

func (m *Model) openStatsPanel() {
	panel := &statsPanelState{viewport: newViewport()}
	m.statsPanel = panel
	m.syncStatsPanelViewport(true)
}

func newViewport() ViewportModel {
	vp := NewViewportModel(1, 1)
	vp.autoFollow = false
	return vp
}

func (m Model) panelContentWidth() int {
	width := m.viewWidth() - m.terminalRightMargin() - 4
	if width < 1 {
		return 1
	}
	return width
}

func (m Model) panelContentHeight() int {
	height := m.viewHeight() - 5
	if height < 3 {
		return 3
	}
	return height
}

func (m *Model) closeStatsPanel() {
	m.statsPanel = nil
}

func (m *Model) syncStatsPanelViewport(initial bool) {
	if m.statsPanel == nil {
		return
	}
	width := m.panelContentWidth()
	height := m.panelContentHeight()
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	oldOffset := m.statsPanel.viewport.YOffset()
	m.statsPanel.viewport.autoFollow = false
	m.statsPanel.viewport.SetSize(width, height)
	m.statsPanel.viewport.SetContent(m.statsPanelBody(width))
	maxOffset := max(0, m.statsPanel.viewport.TotalLineCount()-m.statsPanel.viewport.VisibleLineCount())
	if initial {
		m.statsPanel.viewport.vp.SetYOffset(0)
		return
	}
	m.statsPanel.viewport.vp.SetYOffset(min(maxOffset, oldOffset))
}

func (m Model) renderStatsPanel() string {
	if m.statsPanel == nil {
		return ""
	}
	width := m.panelContentWidth()
	contentWidth := max(12, width)
	meta := statsPanelText(m.currentLanguage(), "session")
	if scroll := m.statsPanel.viewport.ScrollIndicatorStyle(); scroll != "" {
		meta += "  •  " + scroll
	}
	content := strings.Join([]string{
		lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true).Render(statsPanelText(m.currentLanguage(), "title")),
		truncateDisplayWidth(meta, contentWidth),
		m.statsPanel.viewport.View().Content,
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(statsPanelText(m.currentLanguage(), "hint")),
	}, "\n")
	return m.renderContextBox(statsPanelText(m.currentLanguage(), "title"), content, lipgloss.Color("12"))
}

func (m Model) statsPanelBody(width int) string {
	summary := metrics.Summarize(m.sidebarSessionMetrics())
	if !summary.HasData() {
		return statsPanelText(m.currentLanguage(), "empty")
	}

	rows := []string{
		lipgloss.NewStyle().Bold(true).Render(statsPanelText(m.currentLanguage(), "overview")),
		m.renderSidebarDetailRowWithLabelWidth(statsPanelText(m.currentLanguage(), "turns"), fmt.Sprintf("%d", summary.TurnCount), width, 14),
		m.renderSidebarDetailRowWithLabelWidth(statsPanelText(m.currentLanguage(), "avg_ttft"), metrics.FormatDuration(summary.AvgTTFT), width, 14),
		m.renderSidebarDetailRowWithLabelWidth(statsPanelText(m.currentLanguage(), "p95_ttft"), metrics.FormatDuration(summary.P95TTFT), width, 14),
		m.renderSidebarDetailRowWithLabelWidth(statsPanelText(m.currentLanguage(), "avg_duration"), metrics.FormatDuration(summary.AvgDuration), width, 14),
		m.renderSidebarDetailRowWithLabelWidth(statsPanelText(m.currentLanguage(), "p95_duration"), metrics.FormatDuration(summary.P95Duration), width, 14),
		m.renderSidebarDetailRowWithLabelWidth(statsPanelText(m.currentLanguage(), "avg_think"), metrics.FormatDuration(summary.AvgThink), width, 14),
		m.renderSidebarDetailRowWithLabelWidth(statsPanelText(m.currentLanguage(), "tools"), fmt.Sprintf("%d", summary.ToolCallCount), width, 14),
		m.renderSidebarDetailRowWithLabelWidth(statsPanelText(m.currentLanguage(), "fail_rate"), fmt.Sprintf("%d%%", summary.ToolFailureRate()), width, 14),
	}
	if slow := sidebarSlowTools(summary.SlowTools); slow != "" {
		rows = append(rows, m.renderSidebarDetailRowWithLabelWidth(statsPanelText(m.currentLanguage(), "slow_tools"), slow, width, 14))
	}

	rows = append(rows, "", lipgloss.NewStyle().Bold(true).Render(statsPanelText(m.currentLanguage(), "recent_turns")))
	for i := len(summary.Turns) - 1; i >= 0; i-- {
		turn := summary.Turns[i]
		line := fmt.Sprintf("#%d  %s  %s  %s  %dt", turn.TurnIndex, metrics.FormatDuration(turn.TTFT), metrics.FormatDuration(turn.Duration), metrics.FormatDuration(turn.ThinkTime), turn.ToolCallCount)
		if turn.ToolFailureCount > 0 {
			line += "  !"
		}
		rows = append(rows, truncateDisplayWidth(line, width))
		if turn.SlowestTool != "" {
			rows = append(rows, truncateDisplayWidth("    "+statsPanelText(m.currentLanguage(), "slowest")+": "+turn.SlowestTool+" "+metrics.FormatDuration(turn.SlowestToolDuration), width))
		}
	}

	return strings.Join(rows, "\n")
}

func (m *Model) handleStatsPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.statsPanel == nil {
		return *m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeStatsPanel()
	case "up", "k":
		m.statsPanel.viewport.ScrollUp(1)
	case "down", "j":
		m.statsPanel.viewport.ScrollDown(1)
	case "pgup":
		m.statsPanel.viewport.ScrollUp(max(1, m.statsPanel.viewport.VisibleLineCount()/2))
	case "pgdown":
		m.statsPanel.viewport.ScrollDown(max(1, m.statsPanel.viewport.VisibleLineCount()/2))
	}
	return *m, nil
}

func statsPanelText(lang Language, key string) string {
	switch lang {
	case LangZhCN:
		switch key {
		case "title":
			return "会话指标"
		case "session":
			return "当前会话"
		case "hint":
			return "j/k 或 PgUp/PgDn 滚动 • Esc 关闭"
		case "empty":
			return "当前会话还没有指标数据。"
		case "overview":
			return "总览"
		case "recent_turns":
			return "最近轮次"
		case "turns":
			return "轮次"
		case "avg_ttft":
			return "平均首字"
		case "p95_ttft":
			return "P95 首字"
		case "avg_duration":
			return "平均时长"
		case "p95_duration":
			return "P95 时长"
		case "avg_think":
			return "平均思考"
		case "tools":
			return "工具调用"
		case "fail_rate":
			return "失败率"
		case "slow_tools":
			return "慢工具"
		case "slowest":
			return "最慢工具"
		}
	default:
		switch key {
		case "title":
			return "Session stats"
		case "session":
			return "current session"
		case "hint":
			return "j/k or PgUp/PgDn scroll • Esc closes"
		case "empty":
			return "No metrics recorded for this session yet."
		case "overview":
			return "Overview"
		case "recent_turns":
			return "Recent turns"
		case "turns":
			return "Turns"
		case "avg_ttft":
			return "Avg TTFT"
		case "p95_ttft":
			return "P95 TTFT"
		case "avg_duration":
			return "Avg Dur"
		case "p95_duration":
			return "P95 Dur"
		case "avg_think":
			return "Avg Think"
		case "tools":
			return "Tools"
		case "fail_rate":
			return "Fail Rate"
		case "slow_tools":
			return "Slow Tools"
		case "slowest":
			return "Slowest"
		}
	}
	return key
}
