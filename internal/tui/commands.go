package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"bytes"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"runtime"
)

func (m *Model) updateAutoComplete() {
	// Check for slash command
	if active, prefix := DetectSlashCommand(m.input); active {
		matches := CompleteSlashCommand("/" + prefix)
		if len(matches) > 0 {
			m.autoCompleteActive = true
			m.autoCompleteKind = "slash"
			m.autoCompleteItems = matches
			// Reset index if the filtered list changed
			if m.autoCompleteIndex >= len(matches) {
				m.autoCompleteIndex = 0
			}
			return
		}
	}

	// Check for @mention
	if active, prefix := DetectMention(m.input); active {
		workDir, _ := os.Getwd()
		matches := CompleteMention(prefix, workDir)
		if len(matches) > 0 {
			m.autoCompleteActive = true
			m.autoCompleteKind = "mention"
			m.autoCompleteWorkDir = workDir
			m.autoCompleteItems = matches
			if m.autoCompleteIndex >= len(matches) {
				m.autoCompleteIndex = 0
			}
			return
		}
	}

	// No autocomplete active
	m.autoCompleteActive = false
	m.autoCompleteItems = nil
}

func (m *Model) applyAutoComplete() tea.Cmd {
	if m.autoCompleteIndex >= len(m.autoCompleteItems) {
		return nil
	}
	selected := m.autoCompleteItems[m.autoCompleteIndex]

	value := m.input.Value()
	cursor := m.input.Position()

	var replacement string
	if m.autoCompleteKind == "slash" {
		if m.loading {
			m.input.SetValue(selected)
			m.autoCompleteActive = false
			m.autoCompleteItems = nil
			m.autoCompleteIndex = 0
			return nil
		}
		m.input.SetValue("")
		m.autoCompleteActive = false
		m.autoCompleteItems = nil
		m.history = append(m.history, selected)
		m.historyIdx = len(m.history)
		return m.handleCommand(selected)
	}

	if m.autoCompleteKind == "mention" {
		// Replace from the "@" to cursor with the selected path
		atPos := cursor - 1
		for atPos >= 0 && value[atPos] != '@' {
			atPos--
		}
		replacement = "@" + selected + " "
		value = value[:atPos] + replacement + value[cursor:]
	}

	m.input.SetValue(value)
	m.autoCompleteActive = false
	m.autoCompleteItems = nil
	m.autoCompleteIndex = 0
	return nil
}

func (m *Model) submitText(text string, addToHistory bool) tea.Cmd {
	if addToHistory {
		m.history = append(m.history, text)
		m.historyIdx = len(m.history)
	}
	debug.Log("tui", "handleCommand: %s", text)
	return m.handleCommand(text)
}

func (m *Model) handleCommand(text string) tea.Cmd {
	// Slash commands
	if strings.HasPrefix(text, "/") {
		parts := strings.Fields(text)
		cmd := strings.ToLower(parts[0])
		switch cmd {
		case "/exit", "/quit":
			m.quitting = true
			return tea.Quit
		case "/clear":
			m.resetConversationView()
			return nil
		case "/help", "/?":
			m.output.WriteString(m.styles.assistant.Render(m.helpText()))
			m.output.WriteString("\n\n")
			return nil
		case "/model":
			if len(parts) > 1 {
				if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, parts[1]); err == nil {
					if err := m.reloadActiveProvider(); err == nil {
						m.output.WriteString(m.t("command.model_switched", parts[1], m.config.Vendor))
					} else {
						m.output.WriteString(m.styles.error.Render(m.t("command.model_failed", err)))
					}
				} else {
					m.output.WriteString(m.styles.error.Render(m.t("command.model_failed", err)))
				}
			} else {
				resolved, err := m.config.ResolveActiveEndpoint()
				if err != nil {
					m.output.WriteString(m.styles.error.Render(m.t("command.model_failed", err)))
				} else {
					m.output.WriteString(m.t("command.model_current", resolved.Model, resolved.VendorName))
				}
			}
			return nil
		case "/provider":
			if len(parts) > 1 {
				newVendor := parts[1]
				endpoints := m.config.EndpointNames(newVendor)
				if len(endpoints) == 0 {
					m.output.WriteString(m.styles.error.Render(m.t("command.provider_unknown", newVendor, m.vendorNames())))
					return nil
				}
				if err := m.config.SetActiveSelection(newVendor, endpoints[0], ""); err == nil {
					if err := m.reloadActiveProvider(); err == nil {
						m.output.WriteString(m.t("command.provider_switched", newVendor, m.config.Model))
					} else {
						m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
					}
				} else {
					m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
				}
			} else {
				m.openProviderPanel()
			}
			return nil
		case "/allow":
			if len(parts) > 1 {
				if m.policy != nil {
					m.policy.SetOverride(parts[1], permission.Allow)
					m.output.WriteString(m.t("command.allow_set", parts[1]))
				}
			} else {
				m.output.WriteString(m.t("command.usage.allow"))
			}
			return nil
		case "/sessions":
			return m.listSessions()
		case "/resume":
			if len(parts) > 1 {
				return m.resumeSession(parts[1])
			}
			m.output.WriteString(m.t("command.usage.resume"))
			return nil
		case "/export":
			if len(parts) > 1 {
				return m.exportSession(parts[1])
			}
			m.output.WriteString(m.t("command.usage.export"))
			return nil
		case "/plugins":
			return m.handlePluginsCommand()
		case "/image":
			return m.handleImageCommand(parts)
		case "/fullscreen":
			return m.handleFullscreenCommand()
		case "/mcp":
			return m.handleMCPCommand()
		case "/mode":
			return m.handleModeCommand(parts)
		case "/lang":
			return m.handleLangCommand(parts)
		case "/memory":
			return m.handleMemoryCommand(parts)
		case "/undo":
			return m.handleUndoCommand()
		case "/checkpoints":
			return m.handleCheckpointsCommand()
		case "/agents":
			return m.handleAgentsCommand(parts)
		case "/agent":
			return m.handleAgentDetailCommand(parts)
		case "/compact":
			return m.handleCompactCommand()
		case "/todo":
			return m.handleTodoCommand(parts)
		case "/bug":
			return m.handleBugCommand()
		case "/config":
			return m.handleConfigCommand(parts)
		case "/status":
			return m.handleStatusCommand()
		default:
			// Check custom commands
			if cmdName := strings.TrimPrefix(cmd, "/"); cmdName != "" {
				if custom, ok := m.customCmds[cmdName]; ok {
					vars := map[string]string{
						"DIR": workingDirFromModel(m),
					}
					expanded := custom.Expand(vars)
					m.output.WriteString(m.styles.user.Render(m.t("command.custom", cmdName)))
					m.output.WriteString(expanded)
					m.output.WriteString("\n\n")
					m.loading = true
					// Reset status bar state
					m.statusActivity = m.t("status.thinking")
					m.statusToolName = ""
					m.statusToolArg = ""
					m.statusToolCount = 0
					m.resetActivityGroups()
					return m.startAgent(expanded)
				}
			}
			m.output.WriteString(m.styles.error.Render(m.t("command.unknown", text)))
			m.output.WriteString(m.styles.prompt.Render(m.t("command.help_hint")))
			return nil
		}
	}

	// Regular message → start agent
	// Expand @mentions
	workDir, _ := os.Getwd()
	expandedMsg, expandErr := ExpandMentions(text, workDir)
	if expandErr != nil {
		m.output.WriteString(m.styles.error.Render(m.t("command.mention_error", expandErr)))
		m.output.WriteString("\n\n")
	}

	m.output.WriteString(m.styles.user.Render("❯ "))
	m.output.WriteString(text)
	m.output.WriteString("\n")

	// Save original user message to session
	m.appendUserMessage(text)

	m.streamBuffer = &bytes.Buffer{}
	m.streamStartPos = m.output.Len()
	m.streamPrefixWritten = false
	m.loading = true
	// Reset status bar state
	m.statusActivity = m.t("status.thinking")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.resetActivityGroups()
	return m.startAgent(expandedMsg)
}

func (m *Model) resetConversationView() {
	m.output.Reset()
	m.streamBuffer = nil
	m.streamStartPos = 0
	m.streamPrefixWritten = false
	m.loading = false
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.resetActivityGroups()
	m.autoCompleteActive = false
	m.autoCompleteItems = nil
	m.autoCompleteIndex = 0
	m.exitConfirmPending = false
	m.pendingSubmissions = nil
	m.runCanceled = false
	m.runFailed = false
	m.spinner.Stop()
	m.viewport.SetContent("")
	m.viewport.GotoBottom()
}

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
		m.session = ses
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

func (m *Model) handleApproval(d permission.Decision) tea.Cmd {
	pa := m.pendingApproval
	m.pendingApproval = nil
	if pa == nil || pa.Response == nil {
		return nil
	}
	go func() {
		pa.Response <- d
	}()
	return nil
}

func (m *Model) handleApprovalAllowAlways() tea.Cmd {
	pa := m.pendingApproval
	m.pendingApproval = nil
	if pa != nil && m.policy != nil {
		m.policy.SetOverride(pa.ToolName, permission.Allow)
		present := describeTool(m.currentLanguage(), pa.ToolName, pa.Input)
		toolLine := formatToolInline(present.DisplayName, present.Detail)
		if m.currentLanguage() == LangZhCN {
			m.output.WriteString(fmt.Sprintf("\u2713 已总是允许：%s\n\n", toolLine))
		} else {
			m.output.WriteString(fmt.Sprintf("\u2713 Always allow: %s\n\n", toolLine))
		}
	}
	if pa != nil && pa.Response != nil {
		go func() {
			pa.Response <- permission.Allow
		}()
	}
	return nil
}

func (m *Model) handleDiffConfirm(approved bool) tea.Cmd {
	pd := m.pendingDiffConfirm
	m.pendingDiffConfirm = nil
	if pd == nil || pd.Response == nil {
		return nil
	}
	go func() {
		pd.Response <- approved
	}()
	if !approved {
		m.output.WriteString(m.styles.error.Render(m.t("approval.rejected")))
	}
	return nil
}

func (m Model) handleHistoryUp() (tea.Model, tea.Cmd) {
	if m.historyIdx > 0 {
		m.historyIdx--
		m.input.SetValue(m.history[m.historyIdx])
	}
	return m, nil
}

func (m Model) handleHistoryDown() (tea.Model, tea.Cmd) {
	if m.historyIdx < len(m.history)-1 {
		m.historyIdx++
		m.input.SetValue(m.history[m.historyIdx])
	} else {
		m.historyIdx = len(m.history)
		m.input.SetValue("")
	}
	return m, nil
}

func (m Model) handleModeSwitch() (tea.Model, tea.Cmd) {
	m.mode = m.mode.Next()
	// Update policy mode
	if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(m.mode)
	}
	return m, nil
}

func (m *Model) handleModeCommand(parts []string) tea.Cmd {
	if len(parts) > 1 {
		newMode := permission.ParsePermissionMode(parts[1])
		m.mode = newMode
		if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
			cp.SetMode(newMode)
		}
	} else {
		m.output.WriteString(m.t("mode.current", m.mode))
	}
	return nil
}

func (m *Model) handleLangCommand(parts []string) tea.Cmd {
	if len(parts) == 1 {
		m.output.WriteString(m.t("lang.current", m.languageLabel(), supportedLanguageUsage(m.currentLanguage())))
		return nil
	}
	raw := strings.TrimSpace(parts[1])
	lang := normalizeLanguage(raw)
	if lang == LangEnglish && !strings.EqualFold(raw, "en") && !strings.EqualFold(raw, "english") {
		m.output.WriteString(m.styles.error.Render(m.t("lang.invalid", raw, supportedLanguageUsage(m.currentLanguage()))))
		return nil
	}
	m.setLanguage(string(lang))
	m.output.WriteString(m.t("lang.switch", m.languageLabel()))
	return nil
}

func (m *Model) handleUndoCommand() tea.Cmd {
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg(m.t("checkpoint.disabled"))
		}
		cp, err := cpMgr.Undo()
		if err != nil {
			return streamMsg(m.t("checkpoint.undo_failed", err))
		}
		// Show diff (new -> old)
		diffText := diff.UnifiedDiff(cp.NewContent, cp.OldContent, 3)
		var b strings.Builder
		b.WriteString(m.t("checkpoint.undid", cp.ToolCall, displayToolFileTarget(cp.FilePath), cp.ID))
		b.WriteString(FormatDiff(diffText))
		b.WriteString("\n")
		return streamMsg(b.String())
	}
}

func (m *Model) handleCheckpointsCommand() tea.Cmd {
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg(m.t("checkpoint.disabled"))
		}
		ps := cpMgr.List()
		if len(ps) == 0 {
			return streamMsg(m.t("checkpoint.none"))
		}
		var b strings.Builder
		b.WriteString(m.t("checkpoint.list.title", len(ps)))
		for i, cp := range ps {
			b.WriteString(m.t("checkpoint.list.item", i+1, cp.ID, displayToolFileTarget(cp.FilePath), cp.ToolCall, cp.Timestamp.Format("15:04:05")))
		}
		b.WriteString(m.t("checkpoint.list.hint"))
		return streamMsg(b.String())
	}
}

func workingDirFromModel(m *Model) string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func (m *Model) handleMemoryCommand(parts []string) tea.Cmd {
	sub := ""
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "list":
		if m.autoMem == nil {
			m.output.WriteString(m.styles.prompt.Render(m.t("memory.auto_unavailable")))
			return nil
		}
		keys, err := m.autoMem.List()
		if err != nil {
			m.output.WriteString(m.styles.error.Render(m.t("memory.list_failed", err)))
			return nil
		}
		if len(keys) == 0 {
			m.output.WriteString(m.styles.prompt.Render(m.t("memory.none")))
			return nil
		}
		m.output.WriteString(m.styles.title.Render(m.t("memory.auto_title")))
		for _, k := range keys {
			m.output.WriteString(fmt.Sprintf("  - %s\n", k))
		}
		m.output.WriteString("\n")
	case "clear":
		if m.autoMem == nil {
			m.output.WriteString(m.styles.prompt.Render(m.t("memory.auto_unavailable")))
			return nil
		}
		if err := m.autoMem.Clear(); err != nil {
			m.output.WriteString(m.styles.error.Render(m.t("memory.clear_failed", err)))
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(m.t("memory.cleared")))
	default:
		m.output.WriteString(m.styles.title.Render(m.t("memory.title")))
		if len(m.projMemFiles) > 0 {
			m.output.WriteString(m.styles.assistant.Render(m.t("memory.project")))
			for _, f := range m.projMemFiles {
				m.output.WriteString(fmt.Sprintf("  %s\n", f))
			}
			m.output.WriteString("\n")
		} else {
			m.output.WriteString(m.styles.prompt.Render(m.t("memory.project_none")))
		}
		if len(m.autoMemFiles) > 0 {
			m.output.WriteString(m.styles.assistant.Render(m.t("memory.auto")))
			for _, f := range m.autoMemFiles {
				m.output.WriteString(fmt.Sprintf("  %s\n", f))
			}
			m.output.WriteString("\n")
		} else {
			m.output.WriteString(m.styles.prompt.Render(m.t("memory.auto_none")))
		}
		m.output.WriteString(m.styles.prompt.Render(m.t("memory.usage")))
	}
	return nil
}

func (m *Model) handleCompactCommand() tea.Cmd {
	return func() tea.Msg {
		cm := m.agent.ContextManager()
		if cm == nil {
			return streamMsg(m.t("compact.unavailable"))
		}
		if err := cm.Summarize(context.Background(), m.agent.Provider()); err != nil {
			return streamMsg(m.t("compact.failed", err))
		}
		return streamMsg(m.t("compact.done"))
	}
}

func (m *Model) handleTodoCommand(parts []string) tea.Cmd {
	if len(parts) > 1 && strings.ToLower(parts[1]) == "clear" {
		// Clear todos
		todopath := func() string { d, _ := os.UserHomeDir(); return filepath.Join(d, ".ggcode", "todos.json") }()
		if err := os.WriteFile(todopath, []byte("[]\n"), 0644); err != nil {
			return func() tea.Msg {
				return streamMsg(m.t("todo.clear_failed", err))
			}
		}
		m.output.WriteString(m.styles.assistant.Render(m.t("todo.cleared")))
		return nil
	}
	return func() tea.Msg {
		todopath := func() string { d, _ := os.UserHomeDir(); return filepath.Join(d, ".ggcode", "todos.json") }()
		data, err := os.ReadFile(todopath)
		if err != nil {
			if os.IsNotExist(err) {
				return streamMsg(m.t("todo.none"))
			}
			return streamMsg(m.t("todo.read_failed", err))
		}
		// Pretty print JSON
		var raw interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			return streamMsg(m.t("todo.parse_failed", err))
		}
		pretty, _ := json.MarshalIndent(raw, "", "  ")
		return streamMsg(m.t("todo.title", string(pretty)))
	}
}

func (m *Model) handleBugCommand() tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		b.WriteString(m.t("bug.title"))

		// Version info
		b.WriteString(m.t("bug.version"))
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

		// Recent errors from output
		output := m.output.String()
		if idx := strings.LastIndex(output, "Error:"); idx >= 0 {
			end := idx + 500
			if end > len(output) {
				end = len(output)
			}
			b.WriteString(m.t("bug.last_error", output[idx:end]))
		}

		b.WriteString(m.t("bug.hint"))
		return streamMsg(b.String())
	}
}

func (m *Model) handleConfigCommand(parts []string) tea.Cmd {
	if len(parts) > 1 && strings.ToLower(parts[1]) == "set" {
		if len(parts) < 4 {
			m.output.WriteString(m.styles.error.Render(m.t("config.usage")))
			return nil
		}
		key := parts[2]
		value := parts[3]
		if m.config == nil {
			m.output.WriteString(m.styles.error.Render(m.t("config.not_loaded")))
			return nil
		}
		switch key {
		case "model":
			if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, value); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.model_failed", err)))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.model_failed", err)))
				return nil
			}
			m.output.WriteString(m.t("config.model_set", value))
		case "vendor":
			endpoints := m.config.EndpointNames(value)
			if len(endpoints) == 0 {
				m.output.WriteString(m.styles.error.Render(m.t("command.provider_unknown", value, m.vendorNames())))
				return nil
			}
			if err := m.config.SetActiveSelection(value, endpoints[0], ""); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			m.output.WriteString(m.t("config.provider_set", value))
		case "endpoint":
			if err := m.config.SetActiveSelection(m.config.Vendor, value, ""); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			m.output.WriteString(m.t("config.provider_set", value))
		case "language":
			m.setLanguage(value)
			m.output.WriteString(m.t("config.language_set", m.languageLabel()))
		default:
			m.output.WriteString(m.styles.error.Render(m.t("config.unknown_key", key)))
		}
		return nil
	}
	// Show current config
	var b strings.Builder
	b.WriteString(m.styles.title.Render(m.t("config.title")))
	if m.config != nil {
		b.WriteString(fmt.Sprintf("  Vendor:      %s\n", m.config.Vendor))
		b.WriteString(fmt.Sprintf("  Endpoint:    %s\n", m.config.Endpoint))
		b.WriteString(fmt.Sprintf("  Model:       %s\n", m.config.Model))
		b.WriteString(fmt.Sprintf("  Language:    %s\n", m.languageLabel()))
		if resolved, err := m.config.ResolveActiveEndpoint(); err == nil && resolved.MaxTokens > 0 {
			b.WriteString(fmt.Sprintf("  MaxTokens:   %d\n", resolved.MaxTokens))
		}
		if len(m.config.Vendors) > 0 {
			b.WriteString(fmt.Sprintf("  Vendors:     %v\n", m.vendorNames()))
		}
		b.WriteString(fmt.Sprintf("  MCP Servers: %d\n", len(m.config.MCPServers)))
	}
	b.WriteString(m.styles.prompt.Render(m.t("config.usage")))
	m.output.WriteString(b.String())
	return nil
}

func (m *Model) handleStatusCommand() tea.Cmd {
	var b strings.Builder
	b.WriteString(m.styles.title.Render(m.t("status.title")))
	b.WriteString(fmt.Sprintf("  Vendor:      %s\n", m.config.Vendor))
	b.WriteString(fmt.Sprintf("  Endpoint:    %s\n", m.config.Endpoint))
	b.WriteString(fmt.Sprintf("  Model:       %s\n", m.config.Model))
	b.WriteString(fmt.Sprintf("  Language:    %s\n", m.languageLabel()))
	b.WriteString(fmt.Sprintf("  Mode:        %s\n", m.mode))
	b.WriteString(fmt.Sprintf("  Fullscreen:  %v\n", m.fullscreen))

	if m.session != nil {
		b.WriteString(fmt.Sprintf("  Session:     %s\n", m.session.ID))
		b.WriteString(fmt.Sprintf("  Messages:    %d\n", len(m.session.Messages)))
	}

	if m.subAgentMgr != nil {
		n := m.subAgentMgr.RunningCount()
		b.WriteString(fmt.Sprintf("  Agents:      %d running\n", n))
	}

	b.WriteString(fmt.Sprintf("  MCP Servers: %d connected\n", len(m.mcpServers)))
	b.WriteString("\n")
	m.output.WriteString(b.String())
	return nil
}

func (m *Model) reloadActiveProvider() error {
	if err := m.config.Save(); err != nil {
		return err
	}
	if err := m.tryActivateCurrentSelection(); err != nil {
		return err
	}
	m.syncSessionSelection()
	return nil
}

func (m *Model) tryActivateCurrentSelection() error {
	if m.config == nil {
		return fmt.Errorf("config not loaded")
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil {
		return err
	}
	if resolved.APIKey == "" {
		return fmt.Errorf("no api key configured for vendor %q endpoint %q", resolved.VendorID, resolved.EndpointID)
	}
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return err
	}
	if m.agent != nil {
		m.agent.SetProvider(prov)
	}
	return nil
}

func (m *Model) syncSessionSelection() {
	if m.session == nil || m.config == nil {
		return
	}
	m.session.Vendor = m.config.Vendor
	m.session.Endpoint = m.config.Endpoint
	m.session.Model = m.config.Model
	if m.sessionStore != nil {
		_ = m.sessionStore.Save(m.session)
	}
}

func (m *Model) handlePluginsCommand() tea.Cmd {
	if m.pluginMgr == nil {
		m.output.WriteString(m.styles.prompt.Render(m.t("plugins.unavailable")))
		return nil
	}
	results := m.pluginMgr.Results()
	if len(results) == 0 {
		m.output.WriteString(m.styles.prompt.Render(m.t("plugins.none")))
		return nil
	}
	m.output.WriteString(m.styles.title.Render(m.t("plugins.title")))
	for _, r := range results {
		status := "\u2713"
		style := m.styles.assistant
		if !r.Success {
			status = "\u2717"
			style = m.styles.error
		}
		m.output.WriteString(style.Render(fmt.Sprintf("  %s %s", status, r.Name)))
		if r.Error != nil {
			m.output.WriteString(style.Render(fmt.Sprintf(" - %v", r.Error)))
		}
		m.output.WriteString("\n")
		for _, tn := range r.Tools {
			m.output.WriteString(fmt.Sprintf("    - %s\n", tn))
		}
	}
	m.output.WriteString("\n")
	return nil
}

func (m *Model) handleMCPCommand() tea.Cmd {
	if len(m.mcpServers) == 0 {
		m.output.WriteString(m.styles.prompt.Render(m.t("mcp.none")))
		return nil
	}
	m.output.WriteString(m.styles.title.Render(m.t("mcp.title")))
	for _, srv := range m.mcpServers {
		status := "\u2713"
		if !srv.Connected {
			status = "\u2717"
		}
		m.output.WriteString(fmt.Sprintf("  %s %s (%d tools)\n", status, srv.Name, len(srv.ToolNames)))
		for _, tn := range srv.ToolNames {
			m.output.WriteString(fmt.Sprintf("    - %s\n", tn))
		}
	}
	m.output.WriteString("\n")
	return nil
}

func (m *Model) handleImageCommand(parts []string) tea.Cmd {
	if len(parts) < 2 {
		m.output.WriteString(m.styles.error.Render(m.t("image.usage")))
		m.output.WriteString(m.styles.prompt.Render(m.t("image.formats")))
		return nil
	}
	path := parts[1]
	return func() tea.Msg {
		img, err := image.ReadFile(path)
		if err != nil {
			return errMsg{err: fmt.Errorf("reading image: %w", err)}
		}
		placeholder := image.Placeholder(path, img)
		return imageAttachedMsg{
			placeholder: placeholder,
			img:         img,
			filename:    path,
		}
	}
}

func (m *Model) handleFullscreenCommand() tea.Cmd {
	m.fullscreen = !m.fullscreen
	state := "off"
	if m.fullscreen {
		state = m.t("fullscreen.on")
	} else {
		state = m.t("fullscreen.off")
	}
	m.output.WriteString(m.t("fullscreen.state", state))
	return nil
}

func (m *Model) handleAgentsCommand(parts []string) tea.Cmd {
	if m.subAgentMgr == nil {
		m.output.WriteString(m.styles.error.Render(m.t("agents.unavailable")))
		return nil
	}
	agents := m.subAgentMgr.List()
	if len(agents) == 0 {
		m.output.WriteString(m.t("agents.none"))
		return nil
	}
	m.output.WriteString(m.t("agents.title", len(agents)))
	for _, sa := range agents {
		duration := ""
		if !sa.EndedAt.IsZero() && !sa.StartedAt.IsZero() {
			duration = fmt.Sprintf(" (%v)", sa.EndedAt.Sub(sa.StartedAt).Round(1e9))
		}
		m.output.WriteString(m.t("agents.item", sa.ID, sa.Status, duration, truncateStr(sa.Task, 60)))
	}
	m.output.WriteString(m.t("agents.hint"))
	return nil
}

func (m *Model) handleAgentDetailCommand(parts []string) tea.Cmd {
	if m.subAgentMgr == nil {
		m.output.WriteString(m.styles.error.Render(m.t("agents.unavailable")))
		return nil
	}
	if len(parts) < 2 {
		m.output.WriteString(m.t("agent.usage"))
		return nil
	}
	if parts[1] == "cancel" && len(parts) >= 3 {
		if m.subAgentMgr.Cancel(parts[2]) {
			m.output.WriteString(m.t("agent.cancelled", parts[2]))
		} else {
			m.output.WriteString(m.styles.error.Render(m.t("agent.cancel_failed", parts[2])))
		}
		return nil
	}
	sa, ok := m.subAgentMgr.Get(parts[1])
	if !ok {
		m.output.WriteString(m.styles.error.Render(m.t("agent.not_found", parts[1])))
		return nil
	}
	m.output.WriteString(m.t("agent.title", sa.ID, sa.Status, sa.Task))
	if sa.Result != "" {
		m.output.WriteString(m.t("agent.result", sa.Result))
	}
	if sa.Error != nil {
		m.output.WriteString(m.t("agent.error", sa.Error))
	}
	m.output.WriteString("\n")
	return nil
}
