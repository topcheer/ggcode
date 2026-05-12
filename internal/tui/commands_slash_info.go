package tui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/version"
)

func (m *Model) listSessions() tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg(m.t("session.store_missing"))
		}
		sessions, err := m.sessionStore.List()
		if err != nil {
			return streamMsg(m.t("session.list_failed", err))
		}
		if len(sessions) == 0 {
			return streamMsg(m.t("session.none"))
		}
		var b strings.Builder
		b.WriteString(m.t("session.list.title"))
		for i, s := range sessions {
			title := s.Title
			if title == "" {
				title = m.t("session.untitled")
			}
			updated := s.UpdatedAt.Format(time.RFC3339)
			b.WriteString(m.t("session.list.item", i+1, s.ID, title, updated))
		}
		b.WriteString(m.t("session.list.hint"))
		return streamMsg(b.String())
	}
}

func (m *Model) resumeSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg(m.t("session.store_missing"))
		}
		ses, err := m.sessionStore.Load(id)
		if err != nil {
			return streamMsg(m.t("session.resume_failed", id, err))
		}
		// Restore messages into agent
		for _, msg := range ses.Messages {
			m.agent.AddMessage(msg)
		}
		m.SetSession(ses, m.sessionStore)
		m.rebuildConversationFromMessages(ses.Messages)
		m.restoreHistoryFromMessages(ses.Messages)
		title := ses.Title
		if title == "" {
			title = m.t("session.untitled")
		}
		return streamMsg(m.t("session.resume", ses.ID, title, len(ses.Messages)))
	}
}

func (m *Model) exportSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg(m.t("session.store_missing"))
		}
		md, err := m.sessionStore.ExportMarkdown(id)
		if err != nil {
			return streamMsg(m.t("session.export_failed", err))
		}
		filename := fmt.Sprintf("session-%s.md", id)
		if err := os.WriteFile(filename, []byte(md), 0644); err != nil {
			return streamMsg(m.t("session.write_failed", err))
		}
		return streamMsg(m.t("session.exported", id, filename))
	}
}

func (m *Model) handleCheckpointsCommand() tea.Cmd {
	m.openInspectorPanel(inspectorPanelCheckpoints)
	return nil
}

func (m *Model) handleMemoryCommand(parts []string) tea.Cmd {
	sub := ""
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "list":
		m.openInspectorPanel(inspectorPanelMemory)
	case "clear":
		if m.autoMem == nil {
			m.chatWriteSystem(nextSystemID(), m.t("memory.auto_unavailable"))
			return nil
		}
		if err := m.autoMem.Clear(); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("memory.clear_failed", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("memory.cleared"))
	default:
		m.openInspectorPanel(inspectorPanelMemory)
	}
	return nil
}

func (m *Model) handleBugCommand() tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		b.WriteString(m.t("bug.title"))

		// Version info
		b.WriteString(m.t("bug.version", version.Display()))
		b.WriteString(m.t("bug.os", runtime.GOOS, runtime.GOARCH))
		b.WriteString(m.t("bug.go", runtime.Version()))

		// Config info
		if m.config != nil {
			b.WriteString(m.t("bug.provider", m.config.Vendor))
			b.WriteString(m.t("bug.model", m.config.Model))
		}

		// Session info
		if m.session != nil {
			b.WriteString(m.t("bug.session", m.session.ID, len(m.session.Messages)))
		}

		// MCP info
		if len(m.mcpServers) > 0 {
			b.WriteString(m.t("bug.mcp", len(m.mcpServers)))
		}

		// Recent errors from chatList items
		if m.chatList != nil {
			for i := m.chatList.Len() - 1; i >= 0; i-- {
				item := m.chatList.ItemAt(i)
				text := stripAnsiForChat(item.Render(200))
				if idx := strings.LastIndex(text, "Error:"); idx >= 0 {
					end := idx + 500
					if end > len(text) {
						end = len(text)
					}
					b.WriteString(m.t("bug.last_error", text[idx:end]))
					break
				}
			}
		}

		b.WriteString(m.t("bug.hint"))
		return streamMsg(b.String())
	}
}

func (m *Model) handleStatusCommand() tea.Cmd {
	m.openInspectorPanel(inspectorPanelStatus)
	return nil
}

func (m *Model) handlePluginsCommand() tea.Cmd {
	m.openInspectorPanel(inspectorPanelPlugins)
	return nil
}

func (m *Model) handleMCPCommand() tea.Cmd {
	if len(m.mcpServers) == 0 {
		m.chatWriteSystem(nextSystemID(), m.t("mcp.none"))
		return nil
	}
	m.openMCPPanel()
	return nil
}
