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

	"github.com/topcheer/ggcode/internal/cost"
	"bytes"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/image"
	"runtime"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
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

func (m *Model) applyAutoComplete() {
	if m.autoCompleteIndex >= len(m.autoCompleteItems) {
		return
	}
	selected := m.autoCompleteItems[m.autoCompleteIndex]

	value := m.input.Value()
	cursor := m.input.Position()

	var replacement string
	if m.autoCompleteKind == "slash" {
		// Replace from the "/" to cursor with the selected command
		wordStart := cursor
		for wordStart > 0 && value[wordStart-1] != ' ' && value[wordStart-1] != '\t' {
			wordStart--
		}
		replacement = selected + " "
		value = value[:wordStart] + replacement + value[cursor:]
	} else if m.autoCompleteKind == "mention" {
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
			m.output.Reset()
			return nil
		case "/help":
			m.output.WriteString(m.styles.assistant.Render(helpText()))
			m.output.WriteString("\n\n")
			return nil
		case "/model":
			if len(parts) > 1 {
				m.config.Model = parts[1]
				m.costModel = parts[1]
				// Recreate provider with new model
				if prov, err := provider.NewProvider(m.config); err == nil {
					m.agent.SetProvider(prov)
					m.output.WriteString(fmt.Sprintf("Switched model to: %s (provider: %s)\n\n", parts[1], m.config.Provider))
				} else {
					m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Failed to switch model: %v\n\n", err)))
				}
			} else {
				m.output.WriteString(fmt.Sprintf("Current model: %s (provider: %s)\nUsage: /model <model-name>\n\n", m.config.Model, m.config.Provider))
			}
			return nil
		case "/provider":
			if len(parts) > 1 {
				newProvider := parts[1]
				if _, ok := m.config.Providers[newProvider]; !ok {
					m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Unknown provider: %s (available: %v)\n\n", newProvider, m.providerNames())))
					return nil
				}
				m.config.Provider = newProvider
				m.costProvider = newProvider
				if prov, err := provider.NewProvider(m.config); err == nil {
					m.agent.SetProvider(prov)
					m.output.WriteString(fmt.Sprintf("Switched provider to: %s (model: %s)\n\n", newProvider, m.config.Model))
				} else {
					m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Failed to switch provider: %v\n\n", err)))
				}
			} else {
				m.output.WriteString(fmt.Sprintf("Current provider: %s (model: %s)\nAvailable: %s\nUsage: /provider <name>\n\n", m.config.Provider, m.config.Model, m.providerNames()))
			}
			return nil
		case "/allow":
			if len(parts) > 1 {
				if m.policy != nil {
					m.policy.SetOverride(parts[1], permission.Allow)
					m.output.WriteString(fmt.Sprintf("\u2713 %s is now always allowed\n\n", parts[1]))
				}
			} else {
				m.output.WriteString("Usage: /allow <tool-name>\n\n")
			}
			return nil
		case "/cost":
			return m.handleCostCommand(parts)
		case "/sessions":
			return m.listSessions()
		case "/resume":
			if len(parts) > 1 {
				return m.resumeSession(parts[1])
			}
			m.output.WriteString("Usage: /resume <session-id>\n\n")
			return nil
		case "/export":
			if len(parts) > 1 {
				return m.exportSession(parts[1])
			}
			m.output.WriteString("Usage: /export <session-id>\n\n")
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
					m.output.WriteString(m.styles.user.Render(fmt.Sprintf("Custom command /%s:\n", cmdName)))
					m.output.WriteString(expanded)
					m.output.WriteString("\n\n")
					m.loading = true
					// Reset status bar state
					m.statusActivity = "Thinking..."
					m.statusToolName = ""
					m.statusToolArg = ""
					m.statusToolCount = 0
					return m.startAgent(expanded)
				}
			}
			m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Unknown command: %s\n", text)))
			m.output.WriteString(m.styles.prompt.Render("Type /help for available commands\n\n"))
			return nil
		}
	}

	// Regular message → start agent
	// Expand @mentions
	workDir, _ := os.Getwd()
	expandedMsg, expandErr := ExpandMentions(text, workDir)
	if expandErr != nil {
		m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Mention expansion error: %v", expandErr)))
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
	m.statusActivity = "Thinking..."
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	return m.startAgent(expandedMsg)
}

func (m *Model) listSessions() tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg("Session store not configured.\n\n")
		}
		sessions, err := m.sessionStore.List()
		if err != nil {
			return streamMsg(fmt.Sprintf("Error listing sessions: %v\n\n", err))
		}
		if len(sessions) == 0 {
			return streamMsg("No sessions found.\n\n")
		}
		var b strings.Builder
		b.WriteString("Sessions:\n\n")
		for i, s := range sessions {
			title := s.Title
			if title == "" {
				title = "untitled"
			}
			updated := s.UpdatedAt.Format(time.RFC3339)
			b.WriteString(fmt.Sprintf("  %d. %s  %s  (%s)\n", i+1, s.ID, title, updated))
		}
		b.WriteString("\nUse /resume <id> to continue a session\n\n")
		return streamMsg(b.String())
	}
}

func (m *Model) resumeSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg("Session store not configured.\n\n")
		}
		ses, err := m.sessionStore.Load(id)
		if err != nil {
			return streamMsg(fmt.Sprintf("Failed to resume session %s: %v\n\n", id, err))
		}
		// Restore messages into agent
		for _, msg := range ses.Messages {
			m.agent.AddMessage(msg)
		}
		m.session = ses
		title := ses.Title
		if title == "" {
			title = "untitled"
		}
		return streamMsg(fmt.Sprintf("Resumed session: %s \u2014 %s (%d messages)\n\n", ses.ID, title, len(ses.Messages)))
	}
}

func (m *Model) exportSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg("Session store not configured.\n\n")
		}
		md, err := m.sessionStore.ExportMarkdown(id)
		if err != nil {
			return streamMsg(fmt.Sprintf("Error exporting session: %v\n\n", err))
		}
		filename := fmt.Sprintf("session-%s.md", id)
		if err := os.WriteFile(filename, []byte(md), 0644); err != nil {
			return streamMsg(fmt.Sprintf("Error writing file: %v\n\n", err))
		}
		absPath, _ := filepath.Abs(filename)
		return streamMsg(fmt.Sprintf("Exported session %s to %s\n\n", id, absPath))
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
		m.output.WriteString(fmt.Sprintf("\u2713 %s is now always allowed\n\n", pa.ToolName))
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
		m.output.WriteString(m.styles.error.Render("  Rejected.\n"))
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
	m.output.WriteString(fmt.Sprintf("Mode: %s\n", m.mode))
	return m, nil
}

func (m *Model) handleModeCommand(parts []string) tea.Cmd {
	if len(parts) > 1 {
		newMode := permission.ParsePermissionMode(parts[1])
		m.mode = newMode
		if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
			cp.SetMode(newMode)
		}
		m.output.WriteString(fmt.Sprintf("Mode set to: %s\n\n", newMode))
	} else {
		m.output.WriteString(fmt.Sprintf("Current mode: %s\nUsage: /mode <supervised|plan|auto|bypass>\n\n", m.mode))
	}
	return nil
}

func (m *Model) handleUndoCommand() tea.Cmd {
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg("Checkpointing not enabled.\n\n")
		}
		cp, err := cpMgr.Undo()
		if err != nil {
			return streamMsg(fmt.Sprintf("Undo failed: %v\n\n", err))
		}
		// Show diff (new -> old)
		diffText := diff.UnifiedDiff(cp.NewContent, cp.OldContent, 3)
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Undid %s on %s (checkpoint %s)\n", cp.ToolCall, cp.FilePath, cp.ID))
		b.WriteString(FormatDiff(diffText))
		b.WriteString("\n")
		return streamMsg(b.String())
	}
}

func (m *Model) handleCheckpointsCommand() tea.Cmd {
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg("Checkpointing not enabled.\n\n")
		}
		ps := cpMgr.List()
		if len(ps) == 0 {
			return streamMsg("No checkpoints.\n\n")
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Checkpoints (%d):\n\n", len(ps)))
		for i, cp := range ps {
			b.WriteString(fmt.Sprintf("  %d. %s  %s  %s  %s\n", i+1, cp.ID, cp.FilePath, cp.ToolCall, cp.Timestamp.Format("15:04:05")))
		}
		b.WriteString("\nUse /undo to revert the most recent.\n\n")
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
			m.output.WriteString(m.styles.prompt.Render("Auto memory not initialized.\n\n"))
			return nil
		}
		keys, err := m.autoMem.List()
		if err != nil {
			m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Error listing memories: %v\n\n", err)))
			return nil
		}
		if len(keys) == 0 {
			m.output.WriteString(m.styles.prompt.Render("No auto memories saved.\n\n"))
			return nil
		}
		m.output.WriteString(m.styles.title.Render("Auto Memories:\n"))
		for _, k := range keys {
			m.output.WriteString(fmt.Sprintf("  - %s\n", k))
		}
		m.output.WriteString("\n")
	case "clear":
		if m.autoMem == nil {
			m.output.WriteString(m.styles.prompt.Render("Auto memory not initialized.\n\n"))
			return nil
		}
		if err := m.autoMem.Clear(); err != nil {
			m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Error clearing memories: %v\n\n", err)))
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render("All auto memories cleared.\n\n"))
	default:
		m.output.WriteString(m.styles.title.Render("Memory:\n"))
		if len(m.projMemFiles) > 0 {
			m.output.WriteString(m.styles.assistant.Render("Project Memory (GGCODE.md):\n"))
			for _, f := range m.projMemFiles {
				m.output.WriteString(fmt.Sprintf("  %s\n", f))
			}
			m.output.WriteString("\n")
		} else {
			m.output.WriteString(m.styles.prompt.Render("  No GGCODE.md files loaded.\n"))
		}
		if len(m.autoMemFiles) > 0 {
			m.output.WriteString(m.styles.assistant.Render("Auto Memory:\n"))
			for _, f := range m.autoMemFiles {
				m.output.WriteString(fmt.Sprintf("  %s\n", f))
			}
			m.output.WriteString("\n")
		} else {
			m.output.WriteString(m.styles.prompt.Render("  No auto memories loaded.\n"))
		}
		m.output.WriteString(m.styles.prompt.Render("\nUsage: /memory [list|clear]\n\n"))
	}
	return nil
}

func (m *Model) handleCompactCommand() tea.Cmd {
	return func() tea.Msg {
		cm := m.agent.ContextManager()
		if cm == nil {
			return streamMsg("Context manager not available.\n\n")
		}
		if err := cm.Summarize(context.Background(), m.agent.Provider()); err != nil {
			return streamMsg(fmt.Sprintf("Compact failed: %v\n\n", err))
		}
		return streamMsg("Conversation history compacted.\n\n")
	}
}

func (m *Model) handleTodoCommand(parts []string) tea.Cmd {
	if len(parts) > 1 && strings.ToLower(parts[1]) == "clear" {
		// Clear todos
		todopath := func() string { d, _ := os.UserHomeDir(); return filepath.Join(d, ".ggcode", "todos.json") }()
		if err := os.WriteFile(todopath, []byte("[]\n"), 0644); err != nil {
			return func() tea.Msg {
				return streamMsg(fmt.Sprintf("Error clearing todos: %v\n\n", err))
			}
		}
		m.output.WriteString(m.styles.assistant.Render("Todo list cleared.\n\n"))
		return nil
	}
	return func() tea.Msg {
		todopath := func() string { d, _ := os.UserHomeDir(); return filepath.Join(d, ".ggcode", "todos.json") }()
		data, err := os.ReadFile(todopath)
		if err != nil {
			if os.IsNotExist(err) {
				return streamMsg("No todo list found. Use the todo_write tool to create one.\n\n")
			}
			return streamMsg(fmt.Sprintf("Error reading todos: %v\n\n", err))
		}
		// Pretty print JSON
		var raw interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			return streamMsg(fmt.Sprintf("Error parsing todos: %v\n\n", err))
		}
		pretty, _ := json.MarshalIndent(raw, "", "  ")
		return streamMsg(fmt.Sprintf("Todo list:\n%s\n\n", string(pretty)))
	}
}

func (m *Model) handleBugCommand() tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		b.WriteString("=== Bug Report Diagnostics ===\n\n")

		// Version info
		b.WriteString("Version: ggcode (dev)\n")
		b.WriteString(fmt.Sprintf("OS: %s %s\n", runtime.GOOS, runtime.GOARCH))
		b.WriteString(fmt.Sprintf("Go: %s\n", runtime.Version()))

		// Config info
		if m.config != nil {
			b.WriteString(fmt.Sprintf("Provider: %s\n", m.config.Provider))
			b.WriteString(fmt.Sprintf("Model: %s\n", m.config.Model))
		}

		// Session info
		if m.session != nil {
			b.WriteString(fmt.Sprintf("Session: %s (%d messages)\n", m.session.ID, len(m.session.Messages)))
		}

		// MCP info
		if len(m.mcpServers) > 0 {
			b.WriteString(fmt.Sprintf("MCP servers: %d\n", len(m.mcpServers)))
		}

		// Recent errors from output
		output := m.output.String()
		if idx := strings.LastIndex(output, "Error:"); idx >= 0 {
			end := idx + 500
			if end > len(output) {
				end = len(output)
			}
			b.WriteString(fmt.Sprintf("Last error: %s\n", output[idx:end]))
		}

		b.WriteString("\nPlease include this information when reporting a bug.\n\n")
		return streamMsg(b.String())
	}
}

func (m *Model) handleConfigCommand(parts []string) tea.Cmd {
	if len(parts) > 1 && strings.ToLower(parts[1]) == "set" {
		if len(parts) < 4 {
			m.output.WriteString(m.styles.error.Render("Usage: /config set <key> <value>\n\n"))
			return nil
		}
		key := parts[2]
		value := parts[3]
		if m.config == nil {
			m.output.WriteString(m.styles.error.Render("Config not loaded.\n\n"))
			return nil
		}
		switch key {
		case "model":
			m.config.Model = value
			m.output.WriteString(fmt.Sprintf("Config: model = %s\n\n", value))
		case "provider":
			m.config.Provider = value
			m.output.WriteString(fmt.Sprintf("Config: provider = %s\n\n", value))
		default:
			m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Unknown config key: %s\nSupported: model, provider\n\n", key)))
		}
		return nil
	}
	// Show current config
	var b strings.Builder
	b.WriteString(m.styles.title.Render("Current Configuration:\n"))
	if m.config != nil {
		b.WriteString(fmt.Sprintf("  Provider:    %s\n", m.config.Provider))
		b.WriteString(fmt.Sprintf("  Model:       %s\n", m.config.Model))
		if pc, ok := m.config.Providers[m.config.Provider]; ok && pc.MaxTokens > 0 {
			b.WriteString(fmt.Sprintf("  MaxTokens:   %d\n", pc.MaxTokens))
		}
		if len(m.config.Providers) > 0 {
			b.WriteString(fmt.Sprintf("  Providers:    %v\n", m.providerNames()))
		}
		b.WriteString(fmt.Sprintf("  MCP Servers: %d\n", len(m.config.MCPServers)))
	}
	b.WriteString(m.styles.prompt.Render("\nUsage: /config set <key> <value>\n\n"))
	m.output.WriteString(b.String())
	return nil
}

func (m *Model) handleStatusCommand() tea.Cmd {
	var b strings.Builder
	b.WriteString(m.styles.title.Render("Status:\n"))
	b.WriteString(fmt.Sprintf("  Provider:    %s\n", m.config.Provider))
	b.WriteString(fmt.Sprintf("  Model:       %s\n", m.config.Model))
	b.WriteString(fmt.Sprintf("  Mode:        %s\n", m.mode))
	b.WriteString(fmt.Sprintf("  Fullscreen:  %v\n", m.fullscreen))

	if m.session != nil {
		b.WriteString(fmt.Sprintf("  Session:     %s\n", m.session.ID))
		b.WriteString(fmt.Sprintf("  Messages:    %d\n", len(m.session.Messages)))
	}

	if m.lastCost != "" {
		b.WriteString(fmt.Sprintf("  %s\n", m.lastCost))
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

func (m *Model) handlePluginsCommand() tea.Cmd {
	if m.pluginMgr == nil {
		m.output.WriteString(m.styles.prompt.Render("Plugin manager not available.\n\n"))
		return nil
	}
	results := m.pluginMgr.Results()
	if len(results) == 0 {
		m.output.WriteString(m.styles.prompt.Render("No plugins loaded.\n\n"))
		return nil
	}
	m.output.WriteString(m.styles.title.Render("Plugins:\n"))
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
		m.output.WriteString(m.styles.prompt.Render("No MCP servers configured.\n\n"))
		return nil
	}
	m.output.WriteString(m.styles.title.Render("MCP Servers:\n"))
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

func (m *Model) handleCostCommand(parts []string) tea.Cmd {
	if m.costMgr == nil {
		m.output.WriteString(m.styles.error.Render("Cost tracking not enabled.\n\n"))
		return nil
	}

	showAll := len(parts) > 1 && strings.ToLower(parts[1]) == "all"

	if showAll {
		all := m.costMgr.AllCosts()
		if len(all) == 0 {
			m.output.WriteString("No cost data yet.\n\n")
			return nil
		}
		m.output.WriteString(m.styles.title.Render("Cost Summary (all sessions)\n"))
		for _, sc := range all {
			m.output.WriteString(cost.FormatSessionCost(sc, time.Time{}) + "\n")
		}
		total := m.costMgr.TotalCost()
		m.output.WriteString(fmt.Sprintf("\n  Total: %s\n\n", cost.FormatCost(total)))
		return nil
	}

	// Current session
	if sc, ok := m.costMgr.SessionCost("current"); ok {
		m.output.WriteString(m.styles.title.Render("Current Session Cost\n"))
		m.output.WriteString(fmt.Sprintf("  Provider: %s\n", sc.Provider))
		m.output.WriteString(fmt.Sprintf("  Model:    %s\n", sc.Model))
		m.output.WriteString(fmt.Sprintf("  Input:    %s tokens\n", cost.FormatTokens(sc.InputTokens)))
		m.output.WriteString(fmt.Sprintf("  Output:   %s tokens\n", cost.FormatTokens(sc.OutputTokens)))
		m.output.WriteString(fmt.Sprintf("  Cost:     %s USD\n\n", cost.FormatCost(sc.TotalCostUSD)))
	} else {
		m.output.WriteString("No cost data for current session yet.\n\n")
	}
	return nil
}

func (m *Model) handleImageCommand(parts []string) tea.Cmd {
	if len(parts) < 2 {
		m.output.WriteString(m.styles.error.Render("Usage: /image <path/to/file.png>\n"))
		m.output.WriteString(m.styles.prompt.Render("Supported formats: PNG, JPEG, GIF, WebP (max 20MB)\n\n"))
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
		state = "on"
	}
	m.output.WriteString(fmt.Sprintf("Fullscreen: %s\n\n", state))
	return nil
}

func (m *Model) handleAgentsCommand(parts []string) tea.Cmd {
	if m.subAgentMgr == nil {
		m.output.WriteString(m.styles.error.Render("Sub-agent manager not configured.\n\n"))
		return nil
	}
	agents := m.subAgentMgr.List()
	if len(agents) == 0 {
		m.output.WriteString("No sub-agents spawned yet.\nUsage: LLM can use spawn_agent tool to create sub-agents.\n\n")
		return nil
	}
	m.output.WriteString(fmt.Sprintf("%d sub-agent(s):\n", len(agents)))
	for _, sa := range agents {
		duration := ""
		if !sa.EndedAt.IsZero() && !sa.StartedAt.IsZero() {
			duration = fmt.Sprintf(" (%v)", sa.EndedAt.Sub(sa.StartedAt).Round(1e9))
		}
		m.output.WriteString(fmt.Sprintf("  %s [%s]%s - %s\n", sa.ID, sa.Status, duration, truncateStr(sa.Task, 60)))
	}
	m.output.WriteString("\nUse /agent <id> for details, /agent cancel <id> to cancel.\n\n")
	return nil
}

func (m *Model) handleAgentDetailCommand(parts []string) tea.Cmd {
	if m.subAgentMgr == nil {
		m.output.WriteString(m.styles.error.Render("Sub-agent manager not configured.\n\n"))
		return nil
	}
	if len(parts) < 2 {
		m.output.WriteString("Usage: /agent <id> or /agent cancel <id>\n\n")
		return nil
	}
	if parts[1] == "cancel" && len(parts) >= 3 {
		if m.subAgentMgr.Cancel(parts[2]) {
			m.output.WriteString(fmt.Sprintf("Cancelled sub-agent %s\n\n", parts[2]))
		} else {
			m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Could not cancel %s (not found or not running)\n\n", parts[2])))
		}
		return nil
	}
	sa, ok := m.subAgentMgr.Get(parts[1])
	if !ok {
		m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Sub-agent %s not found\n\n", parts[1])))
		return nil
	}
	m.output.WriteString(fmt.Sprintf("Agent: %s\nStatus: %s\nTask: %s\n", sa.ID, sa.Status, sa.Task))
	if sa.Result != "" {
		m.output.WriteString(fmt.Sprintf("Result: %s\n", sa.Result))
	}
	if sa.Error != nil {
		m.output.WriteString(fmt.Sprintf("Error: %v\n", sa.Error))
	}
	m.output.WriteString("\n")
	return nil
}

