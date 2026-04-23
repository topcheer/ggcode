package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/version"
)

func (m *Model) resetConversationView() {
	m.output.Reset()
	m.chatEntries.Reset()
	m.chatReset()
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
	m.clearPendingSubmissions()
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
		m.SetSession(ses, m.sessionStore)
		m.rebuildConversationFromMessages(ses.Messages)
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
	safego.Go("tui.commands.approvalRespond", func() {
		pa.Response <- d
	})
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
			m.dualWriteSystem(fmt.Sprintf("\u2713 已总是允许：%s\n\n", toolLine))
		} else {
			m.dualWriteSystem(fmt.Sprintf("\u2713 Always allow: %s\n\n", toolLine))
		}
	}
	if pa != nil && pa.Response != nil {
		safego.Go("tui.commands.approvalAlwaysAllow", func() {
			pa.Response <- permission.Allow
		})
	}
	return nil
}

func (m *Model) handleDiffConfirm(approved bool) tea.Cmd {
	pd := m.pendingDiffConfirm
	m.pendingDiffConfirm = nil
	if pd == nil || pd.Response == nil {
		return nil
	}
	safego.Go("tui.commands.diffConfirm", func() {
		pd.Response <- approved
	})
	if !approved {
		m.dualWriteSystem(m.styles.error.Render(m.t("approval.rejected")))
	}
	return nil
}

func (m *Model) handleHarnessCheckpointConfirm(approved bool) tea.Cmd {
	pc := m.pendingHarnessCheckpointConfirm
	m.pendingHarnessCheckpointConfirm = nil
	if pc == nil || pc.Response == nil {
		return nil
	}
	safego.Go("tui.commands.harnessCheckpoint", func() {
		pc.Response <- approved
	})
	if !approved {
		m.dualWriteSystem(m.styles.error.Render(m.t("command.harness_cancelled")))
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
	m.persistModePreference()
	return m, nil
}

func (m *Model) handleModeCommand(parts []string) tea.Cmd {
	if len(parts) > 1 {
		newMode := permission.ParsePermissionMode(parts[1])
		m.mode = newMode
		if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
			cp.SetMode(newMode)
		}
		m.persistModePreference()
	} else {
		m.dualWriteSystem(m.t("mode.current", m.mode))
	}
	return nil
}

func (m *Model) persistModePreference() {
	if m.config == nil {
		return
	}
	if err := m.config.SaveDefaultModePreference(m.mode.String()); err != nil {
		m.dualWriteSystem(m.styles.error.Render(m.t("mode.persist_failed", err)))
		m.dualWriteSystem("\n\n")
	}
}

func (m *Model) handleLangCommand(parts []string) tea.Cmd {
	if len(parts) == 1 {
		m.openLanguageSelector(false)
		return nil
	}
	raw := strings.TrimSpace(parts[1])
	lang := normalizeLanguage(raw)
	if lang == LangEnglish && !strings.EqualFold(raw, "en") && !strings.EqualFold(raw, "english") {
		m.dualWriteSystem(m.styles.error.Render(m.t("lang.invalid", raw, supportedLanguageUsage(m.currentLanguage()))))
		return nil
	}
	m.applyLanguageChange(lang)
	return nil
}

func (m *Model) applyLanguageSelection(lang Language) tea.Cmd {
	m.langOptions = nil
	m.langCursor = 0
	m.languagePromptRequired = false
	m.applyLanguageChange(lang)
	return nil
}

func (m *Model) openLanguageSelector(required bool) {
	m.langOptions = languageOptionsFor(m.currentLanguage())
	m.langCursor = 0
	m.languagePromptRequired = required
	for i, opt := range m.langOptions {
		if opt.lang == m.currentLanguage() {
			m.langCursor = i
			break
		}
	}
}

func (m *Model) applyLanguageChange(lang Language) {
	m.setLanguage(string(lang))
	if m.config != nil {
		if err := m.config.SaveLanguagePreference(string(m.currentLanguage())); err != nil {
			m.dualWriteSystem(m.styles.error.Render(err.Error() + "\n"))
			return
		}
	}
	m.dualWriteSystem(m.t("lang.switch", m.languageLabel()))
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
	m.openInspectorPanel(inspectorPanelCheckpoints)
	return nil
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
		m.openInspectorPanel(inspectorPanelMemory)
	case "clear":
		if m.autoMem == nil {
			m.dualWriteSystem(m.styles.prompt.Render(m.t("memory.auto_unavailable")))
			return nil
		}
		if err := m.autoMem.Clear(); err != nil {
			m.dualWriteSystem(m.styles.error.Render(m.t("memory.clear_failed", err)))
			return nil
		}
		m.dualWriteSystem(m.styles.assistant.Render(m.t("memory.cleared")))
	default:
		m.openInspectorPanel(inspectorPanelMemory)
	}
	return nil
}

func (m *Model) handleCompactCommand() tea.Cmd {
	return func() tea.Msg {
		// Cancel any background pre-compact so the manual /compact owns the
		// summarize call and we don't double-compact.
		m.agent.CancelPreCompact()
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
		todoPath := toolpkg.TodoFilePath(workingDirFromModel(m))
		if err := os.Remove(todoPath); err != nil && !os.IsNotExist(err) {
			return func() tea.Msg {
				return streamMsg(m.t("todo.clear_failed", err))
			}
		}
		m.todoSnapshot = nil
		m.activeTodo = nil
		m.dualWriteSystem(m.styles.assistant.Render(m.t("todo.cleared")))
		return nil
	}
	m.openInspectorPanel(inspectorPanelTodos)
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
	if len(parts) > 1 {
		switch strings.ToLower(parts[1]) {
		case "add-endpoint":
			return m.handleConfigAddEndpoint(parts[2:])
		case "remove-endpoint":
			return m.handleConfigRemoveEndpoint(parts[2:])
		}
	}
	if len(parts) > 1 && strings.ToLower(parts[1]) == "set" {
		if len(parts) < 4 {
			m.dualWriteSystem(m.styles.error.Render(m.t("config.usage")))
			return nil
		}
		key := parts[2]
		value := parts[3]
		if m.config == nil {
			m.dualWriteSystem(m.styles.error.Render(m.t("config.not_loaded")))
			return nil
		}
		switch key {
		case "model":
			if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, value); err != nil {
				m.dualWriteSystem(m.styles.error.Render(m.t("command.model_failed", err)))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.dualWriteSystem(m.styles.error.Render(m.t("command.model_failed", err)))
				return nil
			}
			m.dualWriteSystem(m.t("config.model_set", value))
		case "vendor":
			endpoints := m.config.EndpointNames(value)
			if len(endpoints) == 0 {
				m.dualWriteSystem(m.styles.error.Render(m.t("command.provider_unknown", value, m.vendorNames())))
				return nil
			}
			if err := m.config.SetActiveSelection(value, endpoints[0], ""); err != nil {
				m.dualWriteSystem(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.dualWriteSystem(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			m.dualWriteSystem(m.t("config.provider_set", value))
		case "endpoint":
			if err := m.config.SetActiveSelection(m.config.Vendor, value, ""); err != nil {
				m.dualWriteSystem(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.dualWriteSystem(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			m.dualWriteSystem(m.t("config.provider_set", value))
		case "language":
			m.applyLanguageChange(normalizeLanguage(value))
		case "apikey":
			vendorScoped := len(parts) > 4 && (parts[4] == "--vendor" || parts[4] == "-v")
			apiKeyValue := value
			if m.config.Vendor == "" {
				m.dualWriteSystem(m.styles.error.Render("No active vendor. Use /config set vendor <name> first."))
				return nil
			}
			if err := m.config.SetEndpointAPIKey(m.config.Vendor, m.config.Endpoint, apiKeyValue, vendorScoped); err != nil {
				m.dualWriteSystem(m.styles.error.Render(fmt.Sprintf("Failed to set API key: %s", err)))
				return nil
			}
			if err := m.config.Save(); err != nil {
				m.dualWriteSystem(m.styles.error.Render(fmt.Sprintf("Failed to save config: %s", err)))
				return nil
			}
			scope := "endpoint " + m.config.Endpoint
			if vendorScoped {
				scope = "vendor " + m.config.Vendor
			}
			masked := "****"
			if len(apiKeyValue) > 8 {
				masked = apiKeyValue[:4] + strings.Repeat("*", len(apiKeyValue)-8) + apiKeyValue[len(apiKeyValue)-4:]
			}
			m.dualWriteSystem(m.styles.assistant.Render(fmt.Sprintf("\u2713 API key set for %s: %s", scope, masked)))
			if err := m.reloadActiveProvider(); err != nil {
				m.dualWriteSystem(m.styles.assistant.Render(fmt.Sprintf("Provider reload: %s", err)))
			}
		default:
			m.dualWriteSystem(m.styles.error.Render(m.t("config.unknown_key", key)))
		}
		return nil
	}
	m.openInspectorPanel(inspectorPanelConfig)
	return nil
}

func (m *Model) handleStatusCommand() tea.Cmd {
	m.openInspectorPanel(inspectorPanelStatus)
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
		if resolved.AuthType == "oauth" {
			return fmt.Errorf("no login configured for vendor %q endpoint %q", resolved.VendorID, resolved.EndpointID)
		}
		return fmt.Errorf("no api key configured for vendor %q endpoint %q", resolved.VendorID, resolved.EndpointID)
	}
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return err
	}
	if m.agent != nil {
		m.agent.SetProvider(prov)
		if resolved.ContextWindow > 0 {
			m.agent.ContextManager().SetMaxTokens(resolved.ContextWindow)
		}
		if resolved.MaxTokens > 0 {
			m.agent.ContextManager().SetOutputReserve(resolved.MaxTokens)
		}
	}
	m.setActiveRuntimeSelection(resolved.VendorName, resolved.EndpointName, resolved.Model)
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
	m.openInspectorPanel(inspectorPanelPlugins)
	return nil
}

func (m *Model) handleMCPCommand() tea.Cmd {
	if len(m.mcpServers) == 0 {
		m.dualWriteSystem(m.styles.prompt.Render(m.t("mcp.none")))
		return nil
	}
	m.openMCPPanel()
	return nil
}

func (m *Model) handleQQCommand() tea.Cmd {
	m.openQQPanel()
	return nil
}

func (m *Model) handleImageCommand(parts []string) tea.Cmd {
	if len(parts) < 2 {
		m.dualWriteSystem(m.styles.error.Render(m.t("image.usage")))
		m.dualWriteSystem(m.styles.prompt.Render(m.t("image.formats")))
		return nil
	}
	path := parts[1]
	return func() tea.Msg {
		img, err := image.ReadFile(path)
		if err != nil {
			return errMsg{err: fmt.Errorf("reading image: %w", err)}
		}
		sourcePath := path
		if absPath, err := filepath.Abs(path); err == nil {
			sourcePath = absPath
		}
		placeholder := image.Placeholder(path, img)
		return imageAttachedMsg{
			placeholder: placeholder,
			img:         img,
			filename:    path,
			sourcePath:  sourcePath,
		}
	}
}

func (m *Model) handleClipboardPaste() tea.Cmd {
	return func() tea.Msg {
		loader := m.clipboardLoader
		if loader == nil {
			loader = loadClipboardImage
		}
		msg, err := loader()
		if err != nil {
			return errMsg{err: fmt.Errorf(m.t("image.clipboard_failed"), err)}
		}
		return msg
	}
}

func (m *Model) handleSwarmCommand(parts []string) tea.Cmd {
	if m.swarmMgr == nil {
		m.dualWriteSystem(m.styles.error.Render("Swarm is not available"))
		return nil
	}
	m.openSwarmPanel()
	return nil
}

func (m *Model) handleAgentsCommand(parts []string) tea.Cmd {
	m.openInspectorPanel(inspectorPanelAgents)
	return nil
}

func (m *Model) handleAgentDetailCommand(parts []string) tea.Cmd {
	if m.subAgentMgr == nil {
		m.dualWriteSystem(m.styles.error.Render(m.t("agents.unavailable")))
		return nil
	}
	if len(parts) < 2 {
		m.openInspectorPanel(inspectorPanelAgents)
		return nil
	}
	if parts[1] == "cancel" && len(parts) >= 3 {
		if m.subAgentMgr.Cancel(parts[2]) {
			m.dualWriteSystem(m.t("agent.cancelled", parts[2]))
		} else {
			m.dualWriteSystem(m.styles.error.Render(m.t("agent.cancel_failed", parts[2])))
		}
		return nil
	}
	sa, ok := m.subAgentMgr.Get(parts[1])
	if !ok {
		m.dualWriteSystem(m.styles.error.Render(m.t("agent.not_found", parts[1])))
		return nil
	}
	m.openAgentDetailPanel(sa.ID)
	return nil
}

func (m *Model) handleKnightCommand(parts []string) tea.Cmd {
	if m.knight == nil {
		m.dualWriteSystem("Knight is not available (only in daemon mode)\n")
		return nil
	}

	subcmd := ""
	if len(parts) > 1 {
		subcmd = parts[1]
	}

	switch subcmd {
	case "status", "":
		m.dualWriteSystem(fmt.Sprintf("🌙 Knight: %s\n", m.knight.Status()))
		used, remaining, limit := m.knight.BudgetStatus()
		if limit == 0 {
			m.dualWriteSystem(fmt.Sprintf("Budget: %d tokens used / unlimited\n", used))
		} else {
			m.dualWriteSystem(fmt.Sprintf("Budget: %d used / %d remaining / %d total\n", used, remaining, limit))
		}
		// Show staging skills
		staging, _ := m.knight.Index().StagingSkills()
		if len(staging) > 0 {
			m.dualWriteSystem("\nStaging skills:\n")
			for _, s := range staging {
				m.dualWriteSystem(fmt.Sprintf("  • %s (%s): %s\n", s.Name, s.Scope, s.Meta.Description))
			}
		}
	case "budget":
		used, remaining, limit := m.knight.BudgetStatus()
		if limit == 0 {
			m.dualWriteSystem(fmt.Sprintf("Knight budget: %d tokens used / unlimited\n", used))
		} else {
			m.dualWriteSystem(fmt.Sprintf("Knight budget: %d used / %d remaining / %d total\n", used, remaining, limit))
		}
	case "review":
		staging, _ := m.knight.Index().StagingSkills()
		if len(staging) == 0 {
			m.dualWriteSystem("No staging skills\n")
			return nil
		}
		if len(parts) >= 3 {
			name := parts[2]
			s, err := m.knight.FindStagingSkill(name)
			if err == nil {
				result := knight.ValidateSkill(s)
				content, err := os.ReadFile(s.Path)
				if err != nil {
					m.dualWriteSystem(fmt.Sprintf("Error: %v\n", err))
					return nil
				}
				m.dualWriteSystem(fmt.Sprintf("Reviewing staging skill '%s' (%s)\n", s.Name, s.Scope))
				m.dualWriteSystem(fmt.Sprintf("Validation: valid=%v warnings=%d errors=%d\n", result.Valid, len(result.Warnings), len(result.Errors)))
				if len(result.Warnings) > 0 {
					m.dualWriteSystem("Warnings:\n")
					for _, warning := range result.Warnings {
						m.dualWriteSystem(fmt.Sprintf("  - %s\n", warning))
					}
				}
				if len(result.Errors) > 0 {
					m.dualWriteSystem("Errors:\n")
					for _, issue := range result.Errors {
						m.dualWriteSystem(fmt.Sprintf("  - %s\n", issue))
					}
				}
				m.dualWriteSystem("\n")
				m.dualWriteSystem(strings.TrimSpace(string(content)))
				m.dualWriteSystem("\n")
				return nil
			}
			m.dualWriteSystem(fmt.Sprintf("Error: %v\n", err))
			return nil
		}
		m.dualWriteSystem(fmt.Sprintf("Staging skills (%d):\n", len(staging)))
		for _, s := range staging {
			result := knight.ValidateSkill(s)
			status := "valid"
			if !result.Valid {
				status = "invalid"
			}
			m.dualWriteSystem(fmt.Sprintf("  • %s (%s): %s [%s, warnings=%d, errors=%d]\n", s.Name, s.Scope, s.Meta.Description, status, len(result.Warnings), len(result.Errors)))
		}
	case "run":
		if len(parts) < 3 {
			m.dualWriteSystem("Usage: /knight run <task>\n")
			return nil
		}
		goal := strings.TrimSpace(strings.Join(parts[2:], " "))
		if goal == "" {
			m.dualWriteSystem("Usage: /knight run <task>\n")
			return nil
		}
		m.dualWriteSystem(fmt.Sprintf("🌙 Knight running: %s\n", goal))
		m.loading = true
		m.spinner.Start("Knight task")
		m.statusActivity = "Knight task"
		m.statusToolName = "knight"
		m.statusToolArg = truncateStr(goal, 80)
		m.statusToolCount = 1
		return func() tea.Msg {
			result, err := m.knight.RunAdhocTask(context.Background(), goal)
			return knightTaskResultMsg{
				Goal:   goal,
				Result: result,
				Err:    err,
			}
		}
	case "approve":
		if len(parts) < 3 {
			m.dualWriteSystem("Usage: /knight approve <skill-name>\n")
			return nil
		}
		name := parts[2]
		if err := m.knight.PromoteStaging(name); err != nil {
			m.dualWriteSystem(fmt.Sprintf("Error: %v\n", err))
		} else {
			m.dualWriteSystem(fmt.Sprintf("✅ Skill '%s' promoted\n", name))
		}
	case "reject":
		if len(parts) < 3 {
			m.dualWriteSystem("Usage: /knight reject <skill-name>\n")
			return nil
		}
		name := parts[2]
		if err := m.knight.RejectStaging(name); err != nil {
			m.dualWriteSystem(fmt.Sprintf("Error: %v\n", err))
		} else {
			m.dualWriteSystem(fmt.Sprintf("❌ Skill '%s' rejected\n", name))
		}
	case "freeze":
		if len(parts) < 3 {
			m.dualWriteSystem("Usage: /knight freeze <skill-name>\n")
			return nil
		}
		name := parts[2]
		if err := m.knight.SetSkillFrozen(name, true); err != nil {
			m.dualWriteSystem(fmt.Sprintf("Error: %v\n", err))
		} else {
			m.dualWriteSystem(fmt.Sprintf("🔒 Skill '%s' frozen\n", name))
		}
	case "unfreeze":
		if len(parts) < 3 {
			m.dualWriteSystem("Usage: /knight unfreeze <skill-name>\n")
			return nil
		}
		name := parts[2]
		if err := m.knight.SetSkillFrozen(name, false); err != nil {
			m.dualWriteSystem(fmt.Sprintf("Error: %v\n", err))
		} else {
			m.dualWriteSystem(fmt.Sprintf("🔓 Skill '%s' unfrozen\n", name))
		}
	case "rollback":
		if len(parts) < 3 {
			m.dualWriteSystem("Usage: /knight rollback <skill-name>\n")
			return nil
		}
		name := parts[2]
		if err := m.knight.RollbackSkill(name); err != nil {
			m.dualWriteSystem(fmt.Sprintf("Error: %v\n", err))
		} else {
			m.dualWriteSystem(fmt.Sprintf("↩️ Skill '%s' rolled back\n", name))
		}
	case "skills":
		active, _ := m.knight.Index().ActiveSkills()
		if len(active) == 0 {
			m.dualWriteSystem("No active skills\n")
		} else {
			m.dualWriteSystem(fmt.Sprintf("Active skills (%d):\n", len(active)))
			for _, s := range active {
				status := "✓"
				if s.Meta.Frozen {
					status = "🔒"
				}
				ref := knight.FormatSkillRefForDisplay(s.Scope, s.Name)
				used, _, _ := m.knight.SkillUsage(ref)
				avg, samples := m.knight.SkillFeedback(ref)
				feedback := "n/a"
				if samples > 0 {
					feedback = fmt.Sprintf("%.1f/5 (%d)", avg, samples)
				}
				m.dualWriteSystem(fmt.Sprintf("  %s %s (%s): %s [used: %d, feedback: %s]\n", status, s.Name, s.Scope, s.Meta.Description, used, feedback))
			}
		}
	case "rate":
		if len(parts) < 4 {
			m.dualWriteSystem("Usage: /knight rate <skill-name> <1-5>\n")
			return nil
		}
		name := parts[2]
		score, err := strconv.Atoi(parts[3])
		if err != nil || score < 1 || score > 5 {
			m.dualWriteSystem("Usage: /knight rate <skill-name> <1-5>\n")
			return nil
		}
		entry, err := m.knight.FindActiveSkill(name)
		if err != nil {
			m.dualWriteSystem(fmt.Sprintf("Error: %v\n", err))
			return nil
		}
		ref := knight.FormatSkillRefForDisplay(entry.Scope, entry.Name)
		m.knight.RecordSkillEffectiveness(ref, score)
		avg, samples := m.knight.SkillFeedback(ref)
		m.dualWriteSystem(fmt.Sprintf("⭐ Rated skill '%s' %d/5 (avg: %.1f/5 over %d signals)\n", name, score, avg, samples))
	default:
		m.dualWriteSystem("Knight commands: status, budget, review [name], run <task>, approve <name>, reject <name>, freeze <name>, unfreeze <name>, rollback <name>, rate <name> <1-5>, skills\n")
	}
	return nil
}

func (m *Model) handleConfigAddEndpoint(args []string) tea.Cmd {
	if m.config == nil {
		m.dualWriteSystem(m.styles.error.Render(m.t("config.not_loaded")))
		return nil
	}
	// Usage: /config add-endpoint <name> <base_url> [--protocol openai] [--apikey sk-xxx]
	if len(args) < 2 {
		m.dualWriteSystem(m.styles.error.Render("Usage: /config add-endpoint <name> <base_url> [--protocol openai] [--apikey sk-xxx]"))
		return nil
	}
	name := args[0]
	baseURL := args[1]
	protocol := "openai"
	apiKey := ""

	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--protocol", "-p":
			i++
			if i < len(args) {
				protocol = args[i]
			}
		case "--apikey", "-k":
			i++
			if i < len(args) {
				apiKey = args[i]
			}
		}
	}

	vendor := m.config.Vendor
	if vendor == "" {
		m.dualWriteSystem(m.styles.error.Render("No active vendor. Use /config set vendor <name> first."))
		return nil
	}

	if err := m.config.AddEndpoint(vendor, name, protocol, baseURL, apiKey); err != nil {
		m.dualWriteSystem(m.styles.error.Render(fmt.Sprintf("Failed to add endpoint: %s", err)))
		return nil
	}
	if err := m.config.Save(); err != nil {
		m.dualWriteSystem(m.styles.error.Render(fmt.Sprintf("Failed to save config: %s", err)))
		return nil
	}

	msg := fmt.Sprintf("\u2713 Added endpoint %q to vendor %q (protocol=%s, base_url=%s)", name, vendor, protocol, baseURL)
	if apiKey != "" {
		masked := "****"
		if len(apiKey) > 8 {
			masked = apiKey[:4] + strings.Repeat("*", len(apiKey)-8) + apiKey[len(apiKey)-4:]
		}
		msg += fmt.Sprintf(", apikey=%s", masked)
	}
	m.dualWriteSystem(m.styles.assistant.Render(msg))
	m.dualWriteSystem(m.styles.assistant.Render(fmt.Sprintf("Use /config set endpoint %s to activate it.", name)))
	return nil
}

func (m *Model) handleConfigRemoveEndpoint(args []string) tea.Cmd {
	if m.config == nil {
		m.dualWriteSystem(m.styles.error.Render(m.t("config.not_loaded")))
		return nil
	}
	if len(args) < 1 {
		m.dualWriteSystem(m.styles.error.Render("Usage: /config remove-endpoint <name>"))
		return nil
	}
	name := args[0]
	vendor := m.config.Vendor
	if vendor == "" {
		m.dualWriteSystem(m.styles.error.Render("No active vendor."))
		return nil
	}
	if err := m.config.RemoveEndpoint(vendor, name); err != nil {
		m.dualWriteSystem(m.styles.error.Render(fmt.Sprintf("Failed to remove endpoint: %s", err)))
		return nil
	}
	if err := m.config.Save(); err != nil {
		m.dualWriteSystem(m.styles.error.Render(fmt.Sprintf("Failed to save config: %s", err)))
		return nil
	}
	m.dualWriteSystem(m.styles.assistant.Render(fmt.Sprintf("\u2713 Removed endpoint %q from vendor %q", name, vendor)))
	return nil
}
