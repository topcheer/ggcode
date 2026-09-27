package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/session"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// maskAPIKey renders an API key for display: first 4 + stars + last 4
// runes when long enough, otherwise a fixed mask.
// #1410: the gate used BYTE length while the arithmetic used RUNE count -
// multi-byte values (e.g. 15-byte/5-rune keys) computed a negative Repeat
// count and panicked, crashing the whole TUI (bubbletea Update has no
// recover). Both measures are runes now.
func maskAPIKey(key string) string {
	runes := []rune(key)
	if utf8.RuneCountInString(key) <= 8 {
		return "****"
	}
	return string(runes[:4]) + strings.Repeat("*", len(runes)-8) + string(runes[len(runes)-4:])
}

func (m Model) handleModeSwitch() (tea.Model, tea.Cmd) {
	oldMode := m.mode
	m.mode = m.mode.Next()
	debug.Log("mode", "switched %s→%s via Shift+Tab", oldMode, m.mode)
	// Update policy mode
	if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(m.mode)
	}
	// A pending approval request belongs to the old mode's permission context;
	// drop it so it cannot be answered under the new mode (issue #688 LOW).
	m.clearPendingApprovals()
	m.persistModePreference()
	return m, nil
}

func (m *Model) handleModeCommand(parts []string) tea.Cmd {
	if len(parts) > 1 {
		// #743: reject unknown mode names instead of silently falling back
		// to supervised (ParsePermissionMode's fail-safe default is intended
		// for config parsing, not user input).
		if !permission.IsValidPermissionMode(parts[1]) {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Invalid mode %q. Valid modes: supervised | plan | auto | bypass | autopilot", parts[1]))
			return nil
		}
		oldMode := m.mode
		newMode := permission.ParsePermissionMode(parts[1])
		m.mode = newMode
		debug.Log("mode", "switched %s→%s via /mode %s", oldMode, newMode, parts[1])
		if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
			cp.SetMode(newMode)
		}
		// Drop any pending approval from the old mode's context (issue #688 LOW).
		m.clearPendingApprovals()
		m.persistModePreference()
	} else {
		m.chatWriteSystem(nextSystemID(), m.t("mode.current", m.mode))
	}
	return nil
}

func (m *Model) persistModePreference() {
	modeStr := m.mode.String()
	// Persist to session metadata, NOT to global config.
	// This ensures switching mode in one session doesn't affect
	// other sessions or future new sessions.
	if m.session != nil {
		m.session.PermissionMode = modeStr
		if m.sessionStore != nil {
			if err := m.sessionStore.AppendMetaToDisk(m.session); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("mode.persist_failed", err))
			}
		}
	}
}

// persistSidebarPreference saves the sidebar visibility to the current session,
// not to the global config. This ensures toggling the sidebar in one session
// doesn't affect other sessions or future new sessions.
func (m *Model) persistSidebarPreference() {
	if m.session != nil {
		visible := m.sidebarVisible
		m.session.SidebarVisible = &visible
		if m.sessionStore != nil {
			if err := m.sessionStore.AppendMetaToDisk(m.session); err != nil {
				debug.Log("tui", "persistSidebarPreference: %v", err)
			}
		}
	}
}

func (m *Model) handleCompactCommand() tea.Cmd {
	if m.agent == nil {
		return func() tea.Msg {
			return compactResultMsg{err: m.t("compact.unavailable")}
		}
	}
	// Enter loading state and start spinner immediately.
	m.setLoading(true)
	m.statusActivity = m.t("status.compacting")

	return tea.Batch(
		m.startLoadingSpinner(m.statusActivity),
		func() tea.Msg {
			// Cancel any background pre-compact so the manual /compact owns the
			// summarize call and we don't double-compact.
			m.agent.CancelPreCompact()
			cm := m.agent.ContextManager()
			if cm == nil {
				return compactResultMsg{err: m.t("compact.unavailable")}
			}
			tokens := cm.TokenCount()
			// Capture last message ID BEFORE compaction for checkpoint.
			var lastMsgID string
			if msgs := cm.Messages(); len(msgs) > 0 {
				lastMsgID = msgs[len(msgs)-1].ID
			}
			// sa-159: pre_compact lets hooks persist critical state before
			// the manual summarize condenses the context. Best-effort.
			if res := hooks.RunPreCompactHooks(m.agent.GetHookConfig(), hooks.HookEnv{
				TokenBefore:    tokens,
				CompactTrigger: "manual",
			}); res.Err != nil {
				debug.Log("hooks", "pre_compact hook error (non-fatal): %v", res.Err)
			}
			if err := cm.Summarize(context.Background(), m.agent.Provider()); err != nil {
				return compactResultMsg{err: fmt.Sprintf(m.t("compact.failed"), err)}
			}
			newTokens := cm.TokenCount()
			// sa-159: report the manual compaction outcome to on_compaction
			// hooks (fire-and-forget), consistent with auto/reactive paths.
			hooks.RunCompactionHooks(m.agent.GetHookConfig(), hooks.HookEnv{
				TokenBefore:    tokens,
				TokenAfter:     newTokens,
				CompactTrigger: "manual",
			})
			// Persist the compacted context as a checkpoint so --resume
			// restores the compacted state instead of the full history.
			m.agent.SaveCheckpointWithLastMsgID(lastMsgID)
			return compactResultMsg{text: fmt.Sprintf(m.t("compact.done_with_stats"), tokens, newTokens)}
		},
	)
}

func (m *Model) handleUndoCommand() tea.Cmd {
	if m.agent == nil {
		return func() tea.Msg {
			return streamMsg(m.t("checkpoint.disabled"))
		}
	}
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg(m.t("checkpoint.disabled"))
		}
		cp, err := cpMgr.Undo("user")
		if err != nil {
			return streamMsg(m.t("checkpoint.undo_failed", err))
		}
		// Invalidate tool caches so the agent doesn't serve stale results
		// from before the undo. The speculator cache and memoize TTL entries
		// are cleared; mtime-based entries (read_file) are safe because the
		// file's mtime changed during the undo write.
		m.agent.InvalidateToolCaches()
		// Show diff (new -> old)
		diffText := diff.UnifiedDiff(cp.NewContent, cp.OldContent, 3)
		var b strings.Builder
		b.WriteString(m.t("checkpoint.undid", cp.ToolCall, displayToolFileTarget(cp.FilePath), cp.ID))
		b.WriteString(FormatDiff(diffText))
		b.WriteString("\n")
		return streamMsg(b.String())
	}
}

// handleUndoRunCommand reverts ALL file changes from the most recent agent run
// in one batch operation. Unlike /undo (which reverts a single checkpoint),
// /undo-run identifies all checkpoints belonging to the current run and
// reverts every file to its pre-run state. This is the equivalent of Cursor's
// "Reject All" or Claude Code's "undo all changes" — essential when an agent
// made bad changes across multiple files.
func (m *Model) handleUndoRunCommand() tea.Cmd {
	// Block while an agent run is in flight: UndoRun rolls files back and
	// removes checkpoints while the agent may be writing the very same
	// files, so the rollback can be silently overwritten by subsequent
	// agent writes (issue #541). /retry, /edit and /regenerate carry the
	// same guard; /undo-run is also no longer whitelisted in
	// shouldExecuteWhileBusy so a mid-run invocation queues instead of
	// racing the agent loop.
	if m.loading {
		m.chatWriteSystem(nextSystemID(), "Cannot undo-run while the agent is running. Wait for the current run to finish.")
		m.chatListScrollToBottom()
		return nil
	}
	if m.agent == nil {
		return func() tea.Msg {
			return streamMsg(m.t("checkpoint.disabled"))
		}
	}
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg(m.t("checkpoint.disabled"))
		}
		// Check if there are any checkpoints before attempting undo.
		if cpMgr.Last() == nil {
			return streamMsg("No file changes to revert in this run.")
		}
		reverted, err := cpMgr.UndoRun()
		if err != nil {
			return streamMsg(fmt.Sprintf("Undo-run failed: %v", err))
		}
		if len(reverted) == 0 {
			return streamMsg("No file changes to revert in this run.")
		}
		// Invalidate tool caches so the agent doesn't serve stale results.
		m.agent.InvalidateToolCaches()

		// Build summary of reverted files.
		seen := make(map[string]bool)
		var files []string
		for _, cp := range reverted {
			if !seen[cp.FilePath] {
				seen[cp.FilePath] = true
				files = append(files, displayToolFileTarget(cp.FilePath))
			}
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Reverted %d file(s) from the last agent run:\n", len(files)))
		for _, f := range files {
			b.WriteString(fmt.Sprintf("  - %s\n", f))
		}
		b.WriteString("\nUse /redo to re-apply changes one at a time.")
		return streamMsg(b.String())
	}
}

// handleRedoCommand re-applies the most recently undone file edit.
func (m *Model) handleRedoCommand() tea.Cmd {
	if m.agent == nil {
		return func() tea.Msg {
			return streamMsg(m.t("checkpoint.disabled"))
		}
	}
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg(m.t("checkpoint.disabled"))
		}
		cp, err := cpMgr.Redo()
		if err != nil {
			return streamMsg(m.t("checkpoint.redo_failed", err))
		}
		// Invalidate tool caches so the agent doesn't serve stale results.
		m.agent.InvalidateToolCaches()
		// Show diff (old -> new) — the re-applied change
		diffText := diff.UnifiedDiff(cp.OldContent, cp.NewContent, 3)
		var b strings.Builder
		b.WriteString(m.t("checkpoint.redid", cp.ToolCall, displayToolFileTarget(cp.FilePath), cp.ID))
		b.WriteString(FormatDiff(diffText))
		b.WriteString("\n")
		return streamMsg(b.String())
	}
}

// nextSessionInCycle picks the session to switch to when cycling sessions
// with Alt+Up/Alt+Down. The current session is treated as part of the cycle
// even when the store listing does not include it yet — e.g. a fresh session
// created by /clear has no messages on disk, so ListForWorkspace does not
// list it. Without this, currentIdx silently defaulted to 0 and Alt+Down
// jumped the user to sessions[1], skipping the most recent session
// (issue #541).
func nextSessionInCycle(sessions []*session.Session, current *session.Session, direction int) *session.Session {
	if current != nil && !containsSessionID(sessions, current.ID) {
		sessions = append([]*session.Session{current}, sessions...)
	}
	if len(sessions) == 0 {
		return nil
	}
	currentID := ""
	if current != nil {
		currentID = current.ID
	}
	idx := 0
	for i, s := range sessions {
		if s.ID == currentID {
			idx = i
			break
		}
	}
	next := (idx + direction + len(sessions)) % len(sessions)
	if sessions[next].ID == currentID {
		return nil // cycle contains only the current session
	}
	return sessions[next]
}

func containsSessionID(sessions []*session.Session, id string) bool {
	for _, s := range sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}

// Iteration 2: cycleSession switches to next/prev session in the same workspace.
func (m *Model) cycleSession(direction int) tea.Cmd {
	if m.sessionStore == nil {
		return nil
	}
	workspace := m.currentWorkspacePath()
	if workspace == "" {
		return nil
	}
	sessions, err := m.sessionStore.ListForWorkspace(workspace)
	if err != nil {
		return nil
	}
	target := nextSessionInCycle(sessions, m.session, direction)
	if target == nil {
		return nil
	}
	return m.resumeSession(target.ID)
}

// Iteration 3: copyLastAssistantResponse copies last assistant message to clipboard.
func (m *Model) copyLastAssistantResponse() {
	if m.chatList == nil {
		return
	}
	text := m.chatList.LastAssistantText()
	if strings.TrimSpace(text) == "" {
		return
	}
	_ = clipboard.WriteAll(text)
	m.chatWriteSystem(nextSystemID(), "Copied to clipboard")
	m.chatListScrollToBottom()
}

func (m *Model) handleTodoCommand(parts []string) tea.Cmd {
	if len(parts) > 1 && strings.ToLower(parts[1]) == "clear" {
		// Clear todos
		sessionID := ""
		if m.session != nil {
			sessionID = m.session.ID
		}
		todoPath := toolpkg.TodoFilePath(sessionID)
		if err := os.Remove(todoPath); err != nil && !os.IsNotExist(err) {
			return func() tea.Msg {
				return streamMsg(m.t("todo.clear_failed", err))
			}
		}
		m.todoSnapshot = nil
		m.activeTodo = nil
		m.chatWriteSystem(nextSystemID(), m.t("todo.cleared"))
		return nil
	}
	m.openInspectorPanel(inspectorPanelTodos)
	return nil
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
			m.chatWriteSystem(nextSystemID(), m.t("config.usage"))
			return nil
		}
		key := parts[2]
		value := parts[3]
		if m.config == nil {
			m.chatWriteSystem(nextSystemID(), m.t("config.not_loaded"))
			return nil
		}
		switch key {
		case "model":
			if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, value); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.model_failed", err))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.model_failed", err))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("config.model_set", value))
		case "vendor":
			endpoints := m.config.EndpointNames(value)
			if len(endpoints) == 0 {
				m.chatWriteSystem(nextSystemID(), m.t("command.provider_unknown", value, m.vendorNames()))
				return nil
			}
			if err := m.config.SetActiveSelection(value, endpoints[0], ""); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.provider_failed", err))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.provider_failed", err))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("config.provider_set", value))
		case "endpoint":
			if err := m.config.SetActiveSelection(m.config.Vendor, value, ""); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.provider_failed", err))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.provider_failed", err))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("config.provider_set", value))
		case "language":
			m.applyLanguageChange(normalizeLanguage(value))
		case "apikey":
			vendorScoped := len(parts) > 4 && (parts[4] == "--vendor" || parts[4] == "-v")
			apiKeyValue := value
			if m.config.Vendor == "" {
				m.chatWriteSystem(nextSystemID(), "No active vendor. Use /config set vendor <name> first.")
				return nil
			}
			if err := m.config.SetEndpointAPIKey(m.config.Vendor, m.config.Endpoint, apiKeyValue, vendorScoped); err != nil {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to set API key: %s", err))
				return nil
			}
			if err := m.saveConfig(); err != nil {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to save config: %s", err))
				return nil
			}
			scope := "endpoint " + m.config.Endpoint
			if vendorScoped {
				scope = "vendor " + m.config.Vendor
			}
			masked := maskAPIKey(apiKeyValue)
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 API key set for %s: %s", scope, masked))
			if err := m.reloadActiveProvider(); err != nil {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Provider reload: %s", err))
			}
		default:
			m.chatWriteSystem(nextSystemID(), m.t("config.unknown_key", key))
		}
		return nil
	}
	m.openInspectorPanel(inspectorPanelConfig)
	return nil
}

func (m *Model) reloadActiveProvider() error {
	if err := m.tryActivateCurrentSelection(); err != nil {
		return err
	}
	m.syncSessionSelection()
	return nil
}

var reasoningEffortCycle = []string{"", "low", "medium", "high"}

func nextReasoningEffort(current string) string {
	current = strings.ToLower(strings.TrimSpace(current))
	for i, effort := range reasoningEffortCycle {
		if current == effort {
			return reasoningEffortCycle[(i+1)%len(reasoningEffortCycle)]
		}
	}
	return reasoningEffortCycle[1]
}

func displayReasoningEffort(effort string) string {
	if strings.TrimSpace(effort) == "" {
		return "auto (adaptive)"
	}
	return strings.TrimSpace(effort)
}

func (m *Model) cycleReasoningEffort() (string, bool) {
	if m.agent == nil {
		return "", false
	}
	current := m.agent.ReasoningEffort()
	next := nextReasoningEffort(current)
	if !m.agent.SetReasoningEffort(next) {
		return current, false
	}
	return next, true
}

// handleStyleCommand handles the /style slash command.
// Usage:
//
//	/style            - cycle to next style
//	/style concise    - set to a specific style
//	/style default    - reset to default
//	/style list       - show all available styles
func (m *Model) handleStyleCommand(parts []string) tea.Cmd {
	var current string
	if m.config != nil {
		current = m.config.OutputStyle
	}

	// If an argument is given, set directly
	if len(parts) > 1 {
		arg := strings.ToLower(strings.TrimSpace(parts[1]))
		if arg == "list" {
			m.chatWriteSystem(nextSystemID(), m.buildStyleListMessage(current))
			return nil
		}
		if arg == "off" || arg == "reset" || arg == "none" || arg == "default" {
			arg = ""
		}
		normalized := config.NormalizeOutputStyle(arg)
		if normalized != arg && arg != "" {
			// Unknown style
			m.chatWriteSystem(nextSystemID(), m.t("output.style.unknown", arg))
			return nil
		}
		m.setOutputStyle(normalized)
		label := config.DisplayOutputStyle(normalized)
		m.chatWriteSystem(nextSystemID(), m.t("output.style.set", label))
		return nil
	}

	// No argument — cycle
	next := config.NextOutputStyle(current)
	m.setOutputStyle(next)
	label := config.DisplayOutputStyle(next)
	m.chatWriteSystem(nextSystemID(), m.t("output.style.set", label))
	return nil
}

func (m *Model) setOutputStyle(style string) {
	if m.config != nil {
		m.config.OutputStyle = style
		if err := m.saveConfig(); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Output style set to %s but not saved: %s", config.DisplayOutputStyle(style), err))
		}
	}
	m.rebuildSystemPrompt()
}

func (m *Model) buildStyleListMessage(current string) string {
	current = config.NormalizeOutputStyle(current)
	var sb strings.Builder
	sb.WriteString(m.t("output.style.list.header"))
	sb.WriteString("\n")
	for _, s := range config.OutputStyleCycle() {
		label := config.DisplayOutputStyle(s)
		if s == current {
			sb.WriteString(fmt.Sprintf("  * %s (%s)\n", label, m.t("output.style.current")))
		} else {
			sb.WriteString(fmt.Sprintf("    %s\n", label))
		}
	}
	return sb.String()
}

// cycleOutputStyle cycles to the next output style (used by keybinding).
func (m *Model) cycleOutputStyle() (string, bool) {
	var current string
	if m.config != nil {
		current = m.config.OutputStyle
	}
	next := config.NextOutputStyle(current)
	m.setOutputStyle(next)
	return next, true
}

func (m *Model) tryActivateCurrentSelection() error {
	if m.config == nil {
		return fmt.Errorf("config not loaded")
	}
	resolved, prov, err := agentruntime.ResolveCurrentSelection(m.config)
	if err != nil {
		return err
	}
	if m.agent != nil {
		agentruntime.ApplyProviderToAgent(m.agent, prov, resolved)
		m.applySessionLevelLimits()
		sessionCW, sessionMT := 0, 0
		if m.session != nil {
			sessionCW = m.session.ContextWindow
			sessionMT = m.session.MaxTokens
		}
		agentruntime.StartAsyncRelayModelLimitRefreshWithSession(m.config, resolved, m.agent, sessionCW, sessionMT, nil)
		// Silently probe actual context window in background
		m.startContextProbe()
	}
	// 2026-09-19 display-name bug: pass VendorID/EndpointID (the config map
	// keys), NOT VendorName/EndpointName (display labels like "智谱 Z.AI").
	// Name-shaped values made every downstream resolver call
	// (explainUsageSidebar, currentEndpointForUsage) fail with `vendor "智谱
	// Z.AI" is not configured` after any mid-session switch.
	m.setActiveRuntimeSelection(resolved.VendorID, resolved.EndpointID, resolved.Model)
	return nil
}

// applySessionLevelLimits re-applies session-scoped context_window and max_tokens
// overrides on top of the endpoint defaults. This must be called after
// ApplyProviderToAgent (which sets endpoint-level defaults) to preserve
// user-edited values from the model panel.
func (m *Model) applySessionLevelLimits() {
	if m.agent == nil || m.agent.ContextManager() == nil || m.session == nil {
		return
	}
	if m.session.ContextWindow > 0 {
		m.agent.ContextManager().SetContextWindow(m.session.ContextWindow)
	}
	if m.session.MaxTokens > 0 {
		m.agent.ContextManager().SetOutputReserve(m.session.MaxTokens)
	}
}

// ensureProviderSync rebuilds the agent's provider from the current config
// if it's not already in sync. This guarantees that API key changes made in
// the provider panel take effect immediately on the next message, even if
// the user hasn't explicitly activated the vendor/endpoint.
func (m *Model) ensureProviderSync() {
	if m.config == nil || m.agent == nil {
		return
	}
	resolved, prov, err := agentruntime.ResolveCurrentSelection(m.config)
	if err != nil {
		debug.Log("provider", "ensureProviderSync: activate failed: %v", err)
		return
	}
	agentruntime.ApplyProviderToAgent(m.agent, prov, resolved)
	m.applySessionLevelLimits()
	sessionCW, sessionMT := 0, 0
	if m.session != nil {
		sessionCW = m.session.ContextWindow
		sessionMT = m.session.MaxTokens
	}
	agentruntime.StartAsyncRelayModelLimitRefreshWithSession(m.config, resolved, m.agent, sessionCW, sessionMT, nil)
	m.setActiveRuntimeSelection(resolved.VendorID, resolved.EndpointID, resolved.Model)
	m.syncSessionSelection()
	// Silently probe actual context window in background
	m.startContextProbe()
}

func (m *Model) syncSessionSelection() {
	if m.session == nil || m.config == nil {
		return
	}
	m.session.Vendor = m.config.Vendor
	m.session.Endpoint = m.config.Endpoint
	m.session.Model = m.config.Model
	if m.sessionStore != nil {
		if err := m.sessionStore.AppendMetaToDisk(m.session); err != nil {
			debug.Log("tui", "persistModelChange: %v", err)
		}
	}
}

func (m *Model) handleConfigAddEndpoint(args []string) tea.Cmd {
	if m.config == nil {
		m.chatWriteSystem(nextSystemID(), m.t("config.not_loaded"))
		return nil
	}
	// Usage: /config add-endpoint <name> <base_url> [--protocol openai] [--apikey sk-xxx]
	if len(args) < 2 {
		m.chatWriteSystem(nextSystemID(), "Usage: /config add-endpoint <name> <base_url> [--protocol openai] [--apikey sk-xxx]")
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
		m.chatWriteSystem(nextSystemID(), "No active vendor. Use /config set vendor <name> first.")
		return nil
	}

	if err := m.config.AddEndpoint(vendor, name, protocol, baseURL, apiKey); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to add endpoint: %s", err))
		return nil
	}
	if err := m.saveConfig(); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to save config: %s", err))
		return nil
	}

	msg := fmt.Sprintf("\u2713 Added endpoint %q to vendor %q (protocol=%s, base_url=%s)", name, vendor, protocol, baseURL)
	if apiKey != "" {
		masked := maskAPIKey(apiKey)
		msg += fmt.Sprintf(", apikey=%s", masked)
	}
	m.chatWriteSystem(nextSystemID(), msg)
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Use /config set endpoint %s to activate it.", name))
	return nil
}

func (m *Model) handleConfigRemoveEndpoint(args []string) tea.Cmd {
	if m.config == nil {
		m.chatWriteSystem(nextSystemID(), m.t("config.not_loaded"))
		return nil
	}
	if len(args) < 1 {
		m.chatWriteSystem(nextSystemID(), "Usage: /config remove-endpoint <name>")
		return nil
	}
	name := args[0]
	vendor := m.config.Vendor
	if vendor == "" {
		m.chatWriteSystem(nextSystemID(), "No active vendor.")
		return nil
	}
	if err := m.config.RemoveEndpoint(vendor, name); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to remove endpoint: %s", err))
		return nil
	}
	if err := m.saveConfig(); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to save config: %s", err))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 Removed endpoint %q from vendor %q", name, vendor))
	return nil
}

// handlePinCommand manages user-pinned context items that survive compaction.
//
// Usage:
//
//	/pin <text>     Pin a piece of context (survives compaction)
//	/pin list       Show all pinned items
//	/pin clear      Remove all pinned items
//	/pin remove <n> Remove pinned item by index or ID prefix
func (m *Model) handlePinCommand(parts []string) tea.Cmd {
	if m.agent == nil || m.agent.ContextManager() == nil {
		m.chatWriteSystem(nextSystemID(), "Context manager not available.")
		return nil
	}

	pinned := m.agent.ContextManager().Pinned()
	if pinned == nil {
		m.chatWriteSystem(nextSystemID(), "Context pinning not supported.")
		return nil
	}

	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		m.chatWriteSystem(nextSystemID(), m.pinUsageText())
		return nil
	}

	sub := parts[1]
	rest := strings.TrimSpace(strings.Join(parts[2:], " "))

	switch sub {
	case "list":
		items := pinned.List()
		if len(items) == 0 {
			m.chatWriteSystem(nextSystemID(), "No pinned context items.")
			return nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Pinned context (%d items, survives compaction):\n", len(items)))
		for i, item := range items {
			preview := item.Text
			if len(preview) > 80 {
				preview = string([]rune(preview)[:80]) + "..." // #1716: rune-safe clip (skills_panel.go pattern) - byte slice cut CJK mid-sequence
			}
			sb.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, item.ID, preview))
		}
		m.chatWriteSystem(nextSystemID(), strings.TrimRight(sb.String(), "\n"))
		return nil

	case "clear":
		n := pinned.Clear()
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 Cleared %d pinned item(s).", n))
		return nil

	case "remove", "delete", "del":
		if rest == "" {
			m.chatWriteSystem(nextSystemID(), "Usage: /pin remove <index|id>")
			return nil
		}
		if pinned.Remove(rest) {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 Removed pinned item %q.", rest))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Pinned item %q not found.", rest))
		}
		return nil

	default:
		// Treat the entire remaining text as the pin content.
		text := strings.TrimSpace(strings.Join(parts[1:], " "))
		id, err := pinned.Add(text)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to pin: %s", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 Pinned context [%s]. It will survive compaction.", id))
		return nil
	}
}

func (m *Model) pinUsageText() string {
	return "Usage:\n" +
		"  /pin <text>     Pin context (survives compaction)\n" +
		"  /pin list       Show pinned items\n" +
		"  /pin clear      Remove all pinned items\n" +
		"  /pin remove <n> Remove item by index or ID"
}
