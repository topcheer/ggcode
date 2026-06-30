package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/session"
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
		return sessionResumeLoadedMsg{
			requestedID: id,
			session:     ses,
			err:         err,
		}
	}
}

func (m *Model) applyResumedSession(ses *session.Session) {
	if ses == nil {
		return
	}
	if m.agent != nil {
		m.agent.Clear()
		for _, msg := range ses.Messages {
			m.agent.AddMessage(msg)
		}
	}
	m.SetSession(ses, m.sessionStore)
	m.rebuildConversationFromMessages(ses.Messages)
	m.restoreHistoryFromMessages(ses.Messages)
}

func publishCurrentSessionCmd(reset bool) tea.Cmd {
	return func() tea.Msg {
		return tunnelPublishCurrentSessionMsg{reset: reset}
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

// handleInspectorCommand opens the inspector panel for the requested sub-panel.
// With no argument, defaults to status.
func (m *Model) handleInspectorCommand(parts []string) tea.Cmd {
	kind := inspectorPanelStatus // default
	if len(parts) > 1 {
		switch strings.ToLower(parts[1]) {
		case "sessions", "session", "s":
			kind = inspectorPanelSessions
		case "checkpoints", "checkpoint", "c":
			kind = inspectorPanelCheckpoints
		case "memory", "mem":
			kind = inspectorPanelMemory
		case "todos", "todo", "t":
			kind = inspectorPanelTodos
		case "plugins", "plugin", "p":
			kind = inspectorPanelPlugins
		case "config", "cfg":
			kind = inspectorPanelConfig
		case "status", "stat":
			kind = inspectorPanelStatus
		}
	}
	m.openInspectorPanel(kind)
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

// handleDiffCommand runs `git diff` and displays the output in the chat.
// Supports: /diff, /diff --cached, /diff <file>, /diff --stat
func (m *Model) handleDiffCommand(parts []string) tea.Cmd {
	args := []string{"diff"}
	if len(parts) > 1 {
		args = append(args, parts[1:]...)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = workingDirFromModel(m)
	output, err := cmd.CombinedOutput()
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("git diff error: %v", err))
		return nil
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		m.chatWriteSystem(nextSystemID(), "No changes detected (working tree clean).")
		return nil
	}

	// Truncate very large diffs
	maxLines := 200
	lines := strings.Split(result, "\n")
	if len(lines) > maxLines {
		result = strings.Join(lines[:maxLines], "\n") +
			fmt.Sprintf("\n\n... (%d more lines, run /diff <file> to narrow)", len(lines)-maxLines)
	}

	m.chatWriteSystem(nextSystemID(), "```\n"+result+"\n```")
	return nil
}

// handleHooksCommand displays the current hook configuration in the chat.
func (m *Model) handleHooksCommand() tea.Cmd {
	cfg := hooks.HookConfig{}
	if m.agent != nil {
		cfg = m.agent.GetHookConfig()
	}

	var sb strings.Builder
	sb.WriteString("Hooks:\n\n")

	events := []struct {
		name  string
		hooks []hooks.Hook
	}{
		{"on_user_message", cfg.OnUserMessage},
		{"pre_tool_use", cfg.PreToolUse},
		{"post_tool_use", cfg.PostToolUse},
		{"on_agent_stop", cfg.OnAgentStop},
		{"on_stream_stop", cfg.OnStreamStop},
	}

	total := 0
	for _, ev := range events {
		if len(ev.hooks) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("%s (%d):\n", ev.name, len(ev.hooks)))
		for i, h := range ev.hooks {
			hookType := h.HasType()
			detail := ""
			switch hookType {
			case hooks.HookTypeHTTP:
				detail = fmt.Sprintf("url=%s", h.URL)
			default:
				detail = fmt.Sprintf("cmd=%s", h.Command)
			}
			inject := ""
			if h.InjectOutput {
				inject = " [inject]"
			}
			sb.WriteString(fmt.Sprintf("  [%d] %s | %s | match=%q%s\n", i, hookType, detail, h.Match, inject))
			total++
		}
		sb.WriteString("\n")
	}

	if total == 0 {
		sb.WriteString("(no hooks configured — see ggcode.example.yaml for examples)")
	}

	// Show validation errors if any
	if errs := hooks.ValidateHooks(cfg); len(errs) > 0 {
		sb.WriteString("\n⚠ Validation errors:\n")
		for _, e := range errs {
			sb.WriteString("  - " + e + "\n")
		}
	}

	m.chatWriteSystem(nextSystemID(), sb.String())
	return nil
}
