package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/util"
)

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
		sessionLine = fmt.Sprintf("%s %s", m.t("label.session"), util.Truncate(m.session.ID, 18))
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

	if lipgloss.Width(activity) > 30 {
		runes := []rune(activity)
		for i := len(runes); i > 0; i-- {
			if lipgloss.Width(string(runes[:i])) <= 29 {
				activity = string(runes[:i]) + "…"
				break
			}
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
			sb.WriteString(util.Truncate(compactSingleLine(m.activeTodo.Content), 56))
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
			toolLabel := m.statusToolName
			if m.statusToolArg != "" {
				toolLabel = fmt.Sprintf("%s: %s", toolLabel, m.statusToolArg)
			}

			if lipgloss.Width(toolLabel) > 30 {
				runes := []rune(toolLabel)
				for i := len(runes); i > 0; i-- {
					if lipgloss.Width(string(runes[:i])) <= 29 {
						toolLabel = string(runes[:i]) + "…"
						break
					}
				}
			}
			sb.WriteString(toolLabel)
		}
	}
	if tmuxLabel := m.tmuxLabel(); tmuxLabel != "" {
		if sb.Len() > 0 {
			sb.WriteString(" │ ")
		}
		sb.WriteString(tmuxLabel)
	}

	if m.agent != nil {
		if pc := m.agent.PreCompactStatus(); pc.Running {
			if sb.Len() > 0 {
				sb.WriteString(" │ ")
			}
			sb.WriteString(fmt.Sprintf("🗜 ctx %dK→…", pc.StartTok/1000))
		}
	}

	return m.renderContextBoxAuto("", sb.String(), lipgloss.Color("6"))
}

func (m Model) staticHintsSuppressed() bool {
	return !m.hintsShownAt.IsZero() && time.Since(m.hintsShownAt) > 5*time.Minute
}

func (m Model) renderComposerPanel() string {
	accent := m.modeColor()
	hints := []string{
		m.t("hint.mode") + " " + m.renderModeBadge(),
	}

	if ctxLabel := m.contextUsageHint(); ctxLabel != "" {
		hints = append(hints, ctxLabel)
	}

	if costLabel := m.sessionCostHint(); costLabel != "" {
		hints = append(hints, costLabel)
	}

	if !m.staticHintsSuppressed() {
		hints = append(hints, m.t("hint.help"))

		if text := strings.TrimSpace(m.input.Value()); text != "" {
			if wc := len(strings.Fields(text)); wc > 0 {
				hints = append(hints, fmt.Sprintf("%d words", wc))
			}
		}
		if m.sidebarAvailableByWidth() && !m.sidebarEnabled() {
			hints = append(hints, m.t("hint.ctrlr_sidebar"))
		}
		if !m.autoCompleteActive {
			hints = append(hints, m.t("hint.shift_tab_mode"))
		}
		if m.loading {
			hints = append(hints, m.t("hint.ctrlc_cancel"))
		} else {
			hints = append(hints, m.t("hint.ctrlc_exit"))
		}
	}
	if count := m.pendingSubmissionCount(); count > 0 {
		hints = append(hints, m.t("queued.count", count))
	}
	if n := m.pendingImageCount(); n > 0 {
		hints = append(hints, fmt.Sprintf(m.t("hint.image_attached_count"), n))
	}

	if m.streamManager != nil && m.streamManager.IsRunning() {
		statuses := m.streamManager.Status()
		if len(statuses) > 0 {
			ew, eh := m.streamManager.EncoderSize()
			res := fmt.Sprintf("%dx%d", ew, eh)
			if ew == 0 || eh == 0 {
				res = "..."
			}
			names := make([]string, 0, len(statuses))
			for _, s := range statuses {
				names = append(names, s.Name)
			}
			fps := m.streamManager.FPS()
			if fps == 0 {
				fps = 15
			}
			streamDot := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("\u25cf")
			hints = append(hints, fmt.Sprintf("%s %s %s@%dfps", streamDot, strings.Join(names, ","), res, fps))
		}
	}

	if m.loading {
		elapsed := time.Since(m.loopStart).Truncate(time.Second)
		var timerLabel string
		if m.currentLanguage() == LangZhCN {
			timerLabel = "酿造 " + formatDuration(elapsed)
		} else {
			timerLabel = "brewing " + formatDuration(elapsed)
		}
		hints = append(hints, lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(timerLabel))

		if m.streamChars > 0 && !m.streamTextStart.IsZero() {
			streamElapsed := time.Since(m.streamTextStart)
			if streamElapsed >= 500*time.Millisecond {

				estTokens := m.streamChars / 4
				tps := float64(estTokens) / streamElapsed.Seconds()
				if tps > 0 {
					speedLabel := fmt.Sprintf("%.0ftok/s", tps)
					hints = append(hints, lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(speedLabel))
				}
			}
		}
	}

	if len(m.subAgentFollow.slots) > 0 {
		key := "hint.follow_panel"
		if m.subAgentFollow.isActive() {
			key = "hint.unfollow_panel"
		}
		followHint := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00FFFF")).
			Background(lipgloss.Color("#333333")).
			Bold(true).
			Padding(0, 1).
			Render(m.t(key))
		hints = append(hints, followHint)
	}

	hintLine := strings.Join(hints, " • ")

	var followStrip string
	if m.subAgentFollow.slots != nil && len(m.subAgentFollow.slots) > 0 {
		followStrip = m.renderSubAgentFollowStrip() + "\n"
	}

	var inputPart string
	if m.tmuxMenuOpen {
		inputPart = m.renderTmuxMenu()
	} else if m.subAgentFollow.isActive() {
		if strings.HasPrefix(m.subAgentFollow.activeID, "tm-") {
			inputPart = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
				fmt.Sprintf(m.t("follow.active_teammate"), shortID(m.subAgentFollow.activeID)))
		} else {
			inputPart = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
				fmt.Sprintf(m.t("follow.active_agent"), shortID(m.subAgentFollow.activeID)))
		}
	} else {
		inputPart = m.renderComposerInput()
	}
	body := followStrip + inputPart + "\n" + hintLine

	width := m.boxInnerWidth(m.mainColumnWidth())
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(0, 1).
		Width(width).
		Render(body)
}

func (m Model) renderComposerInput() string {
	var top string
	if n := m.pendingImageCount(); n > 0 {
		top = m.renderAttachmentBar() + "\n"
	}
	v := m.input.View()
	if m.inputHint != "" && !m.loading {
		hint := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.inputHint)
		v += hint
	}
	return top + v
}

func (m Model) renderAttachmentBar() string {
	var names []string
	for _, img := range m.pendingImages {
		name := img.filename
		if name == "" {
			name = "image"
		}
		names = append(names, name)
	}
	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color("3")).
		Bold(true).
		Render("[img]")
	body := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7")).
		Render(strings.Join(names, " · "))
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Render("Ctrl+Backspace to remove · /image clear")
	return fmt.Sprintf("%s %s  %s", label, body, hint)
}

func (m Model) contextUsageHint() string {
	if m.agent == nil {
		return ""
	}
	cm := m.agent.ContextManager()
	if cm == nil {
		return ""
	}
	cw := cm.ContextWindow()
	if cw <= 0 {
		return ""
	}
	tokens := cm.TokenCount()
	ratio := float64(tokens) / float64(cw)

	var color string
	switch {
	case ratio >= 0.80:
		color = "196"
	case ratio >= 0.50:
		color = "214"
	default:
		color = "46"
	}

	const barWidth = 8
	filled := int(ratio * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	var barBuilder strings.Builder
	for i := 0; i < barWidth; i++ {
		if i < filled {
			barBuilder.WriteRune('█')
		} else {
			barBuilder.WriteRune('░')
		}
	}

	label := fmt.Sprintf("[%s] ctx %s/%s (%.0f%%)",
		barBuilder.String(),
		humanizeTokenCount(tokens),
		humanizeTokenCount(cw),
		ratio*100,
	)

	threshold := cm.AutoCompactThreshold()
	if threshold > 0 && tokens >= threshold {
		label += " ⚡ compact"
	} else if threshold > 0 {
		compactRatio := float64(tokens) / float64(threshold)
		if compactRatio >= 0.85 {
			label += " ⚡ soon"
		}
	}

	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(label)
}

type sessionCostCache struct {
	mu    sync.Mutex
	sid   string
	n     int
	cost  float64
	usage provider.TokenUsage
}

func (m *Model) sessionCostSnapshot() (float64, provider.TokenUsage) {
	if m.session == nil {
		return 0, provider.TokenUsage{}
	}
	if m.costCache == nil {
		m.costCache = &sessionCostCache{}
	}
	c := m.costCache
	c.mu.Lock()
	defer c.mu.Unlock()

	history := m.session.UsageHistorySnapshot()
	if c.sid == m.session.ID && c.n == len(history) {
		return c.cost, c.usage
	}
	var total float64
	var agg provider.TokenUsage
	for _, entry := range history {
		agg = agg.Add(entry.Usage)
		if entry.Usage.Total() == 0 {
			continue
		}
		rate := resolveRate(entry.Vendor, entry.Endpoint, entry.Model)
		if !rate.IsKnown() || !rate.IsMetered() {
			continue
		}
		u := entry.Usage

		total += float64(u.DisplayInputTokens())*rate.InputPerM/1e6 +
			float64(u.OutputTokens)*rate.OutputPerM/1e6 +
			float64(u.CacheRead)*rate.CacheReadPerM/1e6 +
			float64(u.CacheWrite)*rate.CacheWritePerM/1e6
	}
	c.sid = m.session.ID
	c.n = len(history)
	c.cost = total
	c.usage = agg
	return total, agg
}

func (m Model) sessionCostHint() string {
	if m.session == nil {
		return ""
	}
	usage := m.session.TokenUsage
	if usage.Total() == 0 {
		usage = m.sidebarSessionUsage()
	}

	if usage.Total() == 0 && len(m.session.UsageHistorySnapshot()) > 0 {
		_, usage = m.sessionCostSnapshot()
	}
	totalTokens := usage.Total()
	if totalTokens == 0 {
		return ""
	}

	totalCost := m.estimateSessionCost()

	var label string
	if totalCost > 0 {
		label = cost.FormatCost(totalCost)
	} else {
		label = humanizeTokenCount(totalTokens) + " tok"
	}

	var color string
	switch {
	case totalCost >= 2.0:
		color = "196"
	case totalCost >= 0.50:
		color = "214"
	default:
		color = "245"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(label)
}

func (m Model) estimateSessionCost() float64 {
	if m.session == nil {
		return 0
	}
	total, _ := m.sessionCostSnapshot()

	if total == 0 && m.session.TokenUsage.Total() > 0 {
		rate := resolveRate(m.session.Vendor, m.session.Endpoint, m.session.Model)
		if rate.IsKnown() && rate.IsMetered() {
			u := m.session.TokenUsage

			total = float64(u.DisplayInputTokens())*rate.InputPerM/1e6 +
				float64(u.OutputTokens)*rate.OutputPerM/1e6 +
				float64(u.CacheRead)*rate.CacheReadPerM/1e6 +
				float64(u.CacheWrite)*rate.CacheWritePerM/1e6
		}
	}
	return total
}
