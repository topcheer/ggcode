package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

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

	// Truncate activity to 30 rendered characters for status bar
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
			// Truncate tool label to 30 rendered characters
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

	// Show background compression indicator if active
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

func (m Model) sidebarActivity() string {
	if m.activeTodo != nil {
		return util.Truncate(localizeTodoFocus(m.currentLanguage(), m.activeTodo.Content), m.sidebarWidth()-12)
	}
	if m.statusActivity != "" {
		return m.statusActivity
	}
	return m.t("activity.idle")
}
func (m Model) renderComposerPanel() string {
	accent := m.modeColor()
	hints := []string{
		m.t("hint.mode") + " " + m.renderModeBadge(),
	}
	// Context window usage indicator — show when agent exists.
	if ctxLabel := m.contextUsageHint(); ctxLabel != "" {
		hints = append(hints, ctxLabel)
	}
	hints = append(hints, m.t("hint.help"))
	// Iteration 6: Word count for current input
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
	if count := m.pendingSubmissionCount(); count > 0 {
		hints = append(hints, m.t("queued.count", count))
	}
	if n := m.pendingImageCount(); n > 0 {
		hints = append(hints, fmt.Sprintf(m.t("hint.image_attached_count"), n))
	}
	// Stream status: show platform-resolution@fps when streaming
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

	// Show loop timer as last hint when agent is running
	if m.loading {
		elapsed := time.Since(m.loopStart).Truncate(time.Second)
		var timerLabel string
		if m.currentLanguage() == LangZhCN {
			timerLabel = "酿造 " + formatDuration(elapsed)
		} else {
			timerLabel = "brewing " + formatDuration(elapsed)
		}
		hints = append(hints, lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(timerLabel))
	}

	// Ctrl+N follow/unfollow hint when subagent/teammate slots exist
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

	// Sub-agent follow strip and disabled input
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

// renderAttachmentBar renders a compact one-line summary of attached images.
// Format: `[img] screenshot.png · diagram.png  (Ctrl+Backspace to remove last)`
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

// contextUsageHint returns a colored context window usage string for the composer hints.
// Shows "ctx 12.3K/128K (10%)" with color coding: green <50%, yellow <80%, red >=80%.
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

	// Color based on usage level.
	var color string
	var bar string
	switch {
	case ratio >= 0.80:
		color = "196" // red
		bar = "█"
	case ratio >= 0.50:
		color = "214" // orange/yellow
		bar = "▓"
	default:
		color = "46" // green
		bar = "░"
	}

	label := fmt.Sprintf("%s ctx %s/%s (%.0f%%)",
		bar,
		humanizeTokenCount(tokens),
		humanizeTokenCount(cw),
		ratio*100,
	)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(label)
}
