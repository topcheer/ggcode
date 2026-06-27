package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/debug"
)

func (m Model) renderConversationPanel(panelHeight int) string {
	innerW := m.conversationInnerWidth()
	innerH := conversationInnerHeight(panelHeight)

	// Follow mode: render followed agent/teammate view instead of main conversation.
	// IMPORTANT: We only render the cached entry.list here — the actual list rebuild
	// happens in Update() via rebuildActiveView(). This avoids rebuilding on every
	// View() frame and prevents the "fall through to main view" path that causes
	// rendering artifacts when the snapshot is temporarily unavailable.
	if m.subAgentFollow.isActive() {
		width := m.boxInnerWidth(m.mainColumnWidth())
		style := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("208")).
			Padding(0, 1).
			Width(width).
			Height(panelHeight).
			MaxHeight(panelHeight)

		entry := m.subAgentFollow.getOrCreateView(m.subAgentFollow.activeID, innerW, innerH)
		entry.list.SetSize(innerW, innerH)

		// If the entry list has been populated (by rebuildActiveView in Update()),
		// render it. Otherwise show a loading placeholder.
		if entry.list.Len() > 0 {
			return style.Render(entry.list.Render())
		}

		// List hasn't been built yet (first frame before Update fires) —
		// show a placeholder instead of falling through to the main chat list.
		// Falling through causes rendering artifacts because the main chat content
		// has different line widths than the follow view content.
		placeholder := lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Render("  Loading follow view...")
		return style.Render(placeholder)
	}

	if debug.IsVerbose("layout") {
		debug.Log("layout", "panel ph=%d iw=%d ih=%d n=%d",
			panelHeight, innerW, innerH, m.chatList.Len())
	}

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
		} else if m.autoCompleteKind == "lanchat" {
			if strings.EqualFold(item, "All") || strings.EqualFold(item, "所有人") {
				label = "📢 " + item
			} else {
				label = "💬 " + item
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
	if m.autoCompleteKind == "lanchat" {
		if m.currentLanguage() == LangZhCN {
			title = "LAN 聊天用户"
		} else {
			title = "LAN Chat Users"
		}
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
		} else if m.autoCompleteKind == "lanchat" {
			if strings.EqualFold(item, "All") || strings.EqualFold(item, "所有人") {
				label = "📢 " + item
				if m.currentLanguage() == LangZhCN {
					desc = "广播"
				} else {
					desc = "broadcast"
				}
			} else {
				label = "💬 " + item
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
