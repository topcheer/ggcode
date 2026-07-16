package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// handleClearChat creates a new session, clears the conversation view,
// and notifies any connected mobile client.
func (m *Model) handleClearChat() {
	// Flush final metadata for the old session. Messages were already
	// persisted incrementally via AppendMessageToDisk (PersistHandler),
	// so we must NOT call Save() here — Save() rewrites the entire JSONL
	// from ses.Messages, which only contains user submissions (not the
	// agent replies that were written directly to disk). Calling Save()
	// would overwrite and PERMANENTLY DELETE all agent/tool messages.
	if m.session != nil && m.sessionStore != nil {
		oldSes := m.session
		oldStore := m.sessionStore
		safego.Go("tui.clearChat.metaFlush", func() {
			if jsonlStore, ok := oldStore.(*session.JSONLStore); ok {
				_ = jsonlStore.AppendMetaToDisk(oldSes)
			}
		})
	}

	// Create new session.
	vendor, endpoint, model := "", "", ""
	if m.config != nil {
		vendor = m.config.Vendor
		endpoint = m.config.Endpoint
		model = m.config.Model
	}
	ses := session.NewSession(vendor, endpoint, model)
	// No need to call Save here — the JSONL file is created lazily on the
	// first AppendMessageToDisk call when the user sends their first message.
	// Calling Save would just create an empty file.

	// Switch agent, view, lock, cron, and tunnel to the new session.
	m.switchToSession(ses, true)

	m.chatWriteSystem(nextSystemID(), m.t("session.new", ses.ID))
}

// switchToSession is the shared helper for all session-switching paths
// (/clear, /sessions resume, /branch). It handles:
//   - Resetting view state (streaming buffers, autocomplete, spinner)
//   - Clearing and restoring agent context (via RestoreSessionIntoAgent)
//   - Updating session store and persisted message count
//   - Releasing old session lock and acquiring new one
//   - Rebinding cron scheduler to the new session
//   - Restoring session-scoped permission mode
//   - Restoring session-scoped sidebar visibility
//   - Notifying connected mobile clients
//
// For new sessions (isNew=true), permission mode is set to the global default
// instead of restored from session metadata.
func (m *Model) switchToSession(ses *session.Session, isNew bool) {
	if ses == nil {
		return
	}

	// Reset view state so stale streaming/autocomplete state doesn't leak.
	m.resetConversationView()

	// Capture old workspace BEFORE SetSession overwrites m.session.
	oldWorkspace := ""
	if m.session != nil {
		oldWorkspace = m.session.Workspace
	}

	// Clear and restore agent context.
	if m.agent != nil {
		m.agent.Clear()
		agentruntime.RestoreSessionIntoAgent(m.agent, ses)
	}

	m.SetSession(ses, m.sessionStore)

	// Switch CWD if the session belongs to a different workspace.
	// Note: must compare against oldWorkspace (captured before SetSession),
	// because SetSession sets m.session = ses, making ses.Workspace == m.session.Workspace.
	if ses.Workspace != "" && oldWorkspace != "" && ses.Workspace != oldWorkspace {
		if m.agent != nil {
			m.agent.SetWorkingDir(ses.Workspace)
		}
		debug.Log("tui", "switchToSession: switched agent workingDir from %q to %q", oldWorkspace, ses.Workspace)
	}

	// Release old session lock and acquire new one.
	if m.sessionLockSwitch != nil {
		m.sessionLockSwitch(ses.ID)
	}
	// Rebind cron scheduler to the new session's store path.
	// Use the session's workspace for migration, not the current r.workingDir.
	if m.sessionCronSwitch != nil {
		m.sessionCronSwitch(ses.ID)
	}

	// Restore session-scoped state.
	if isNew {
		// New session inherits global default permission mode.
		if m.config != nil {
			globalMode := permission.ParsePermissionMode(m.config.DefaultMode)
			m.mode = globalMode
		}
	} else {
		// Resumed session restores its saved permission mode.
		if ses.PermissionMode != "" {
			sessionMode := permission.ParsePermissionMode(ses.PermissionMode)
			if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
				cp.SetMode(sessionMode)
			}
			m.mode = sessionMode
		}
		// Restore session-scoped sidebar visibility (if set).
		if ses.SidebarVisible != nil {
			m.sidebarVisible = *ses.SidebarVisible
		}
	}

	m.rebuildConversationFromMessages(ses.Messages)

	// Refresh cached git branch — sessions from other workspaces may have
	// different active branches.
	m.refreshCachedGitBranch()

	if !isNew {
		// Only restore input history for resumed sessions (new sessions
		// start with empty history so applyAutoComplete can add commands).
		m.restoreHistoryFromMessages(ses.Messages)
	}

	// Notify mobile client of session switch.
	m.publishTunnelSnapshotForCurrentSession(true)
}

// handleUnshare stops the active tunnel/sharing session.
func (m *Model) handleUnshare() {
	if m.tunnelSession == nil && !m.tunnelStarting {
		m.chatWriteSystem(nextSystemID(), m.t("tunnel.not_active"))
		return
	}
	m.closeTunnelGracefullyAsync(2 * time.Second)
	m.chatWriteSystem(nextSystemID(), m.t("tunnel.stopped"))
}

func (m *Model) resetConversationView() {
	m.chatReset()
	m.streamBuffer = nil
	m.streamPrefixWritten = false
	m.setLoading(false)
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.autoCompleteActive = false
	m.autoCompleteItems = nil
	m.autoCompleteIndex = 0
	m.exitConfirmPending = false
	m.clearPendingSubmissions()
	m.runCanceled = false
	m.runFailed = false
	m.spinner.Stop()
	m.chatListScrollToBottom()
}

func (m *Model) handleApproval(d permission.Decision) tea.Cmd {
	pa := m.pendingApproval
	requestID := m.tunnelPendingApprovalID
	m.pendingApproval = nil
	m.tunnelPendingApprovalID = ""
	if pa == nil || pa.Response == nil {
		return nil
	}
	m.pushTunnelApprovalResult(requestID, tunnelDecisionString(d))
	safego.Go("tui.commands.approvalRespond", func() {
		select {
		case pa.Response <- d:
		default:
			// Receiver already gone; drop to avoid goroutine leak.
		}
	})
	return nil
}

func (m *Model) handleApprovalAllowAlways() tea.Cmd {
	pa := m.pendingApproval
	requestID := m.tunnelPendingApprovalID
	m.pendingApproval = nil
	m.tunnelPendingApprovalID = ""
	if pa != nil && m.policy != nil {
		m.policy.SetOverride(pa.ToolName, permission.Allow)
		present := describeTool(m.currentLanguage(), pa.ToolName, pa.Input)
		toolLine := formatToolInline(present.DisplayName, present.Detail)
		if m.currentLanguage() == LangZhCN {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 已总是允许：%s", toolLine))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 Always allow: %s", toolLine))
		}
	}
	if pa != nil && pa.Response != nil {
		safego.Go("tui.commands.approvalAlwaysAllow", func() {
			select {
			case pa.Response <- permission.Allow:
			default:
			}
		})
	}
	m.pushTunnelApprovalResult(requestID, tunnel.DecisionAlwaysAllow)
	return nil
}

func (m *Model) handleDiffConfirm(approved bool) tea.Cmd {
	pd := m.pendingDiffConfirm
	m.pendingDiffConfirm = nil
	if pd == nil || pd.Response == nil {
		return nil
	}
	safego.Go("tui.commands.diffConfirm", func() {
		select {
		case pd.Response <- approved:
		default:
		}
	})
	if !approved {
		m.chatWriteSystem(nextSystemID(), m.t("approval.rejected"))
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
		select {
		case pc.Response <- approved:
		default:
		}
	})
	if !approved {
		m.chatWriteSystem(nextSystemID(), m.t("command.harness_cancelled"))
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

func (m *Model) handleLangCommand(parts []string) tea.Cmd {
	if len(parts) == 1 {
		m.openLanguageSelector(false)
		return nil
	}
	raw := strings.TrimSpace(parts[1])
	lang := normalizeLanguage(raw)
	if lang == LangEnglish && !strings.EqualFold(raw, "en") && !strings.EqualFold(raw, "english") {
		m.chatWriteSystem(nextSystemID(), m.t("lang.invalid", raw, supportedLanguageUsage(m.currentLanguage())))
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
			m.chatWriteSystem(nextSystemID(), err.Error())
			return
		}
	}
	m.chatWriteSystem(nextSystemID(), m.t("lang.switch", m.languageLabel()))
}

func workingDirFromModel(m *Model) string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func (m *Model) handleImageCommand(parts []string) tea.Cmd {
	if len(parts) < 2 {
		m.chatWriteSystem(nextSystemID(), m.t("image.usage"))
		m.chatWriteSystem(nextSystemID(), m.t("image.formats"))
		return nil
	}
	switch strings.ToLower(parts[1]) {
	case "paste":
		return m.handleClipboardPaste()
	case "remove", "rm", "del", "delete":
		if img, ok := m.popPendingImage(); ok {
			m.chatWriteSystem(nextSystemID(), "Removed: "+img.filename)
		} else {
			m.chatWriteSystem(nextSystemID(), "No images attached.")
		}
		return nil
	case "clear", "reset":
		n := m.pendingImageCount()
		m.clearPendingImages()
		if n > 0 {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Cleared %d image(s).", n))
		} else {
			m.chatWriteSystem(nextSystemID(), "No images attached.")
		}
		return nil
	case "list":
		if m.pendingImageCount() == 0 {
			m.chatWriteSystem(nextSystemID(), "No images attached.")
			return nil
		}
		var names []string
		for i, img := range m.pendingImages {
			names = append(names, fmt.Sprintf("  %d. %s", i+1, img.filename))
		}
		m.chatWriteSystem(nextSystemID(), "Attached images:\n"+strings.Join(names, "\n"))
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
			filename:    filepath.Base(path),
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
			// Silently ignore if clipboard doesn't contain an image.
			// This is common (user pressed Ctrl+V with text in clipboard).
			if errors.Is(err, image.ErrClipboardImageUnavailable) {
				// On Windows, show a hint since the terminal likely intercepted
				// the keypress and the clipboard may not have been checked at all.
				if runtime.GOOS == "windows" {
					return systemNotifyMsg{Text: m.t("image.clipboard_no_image_windows")}
				}
				return nil // no-op on macOS/Linux
			}
			// Use systemNotifyMsg instead of errMsg so the actual clipboard
			// error (e.g. "Install wl-clipboard or xclip") is shown as-is.
			// errMsg routes through provider.UserFacingError() which strips
			// clipboard errors to a generic "请求失败，请稍后重试" and kills
			// any active agent run via handleErrMsg.
			return systemNotifyMsg{Text: fmt.Sprintf(m.t("image.clipboard_failed"), err)}
		}
		return msg
	}
}

// handleClipboardPasteFallback is used for tea.PasteMsg on Windows where the
// Ctrl+V keypress is intercepted by the terminal. It attempts to load a
// clipboard image; if the clipboard contains text instead, it falls back to a
// plain text paste message.
func (m *Model) handleClipboardPasteFallback(original tea.PasteMsg) tea.Cmd {
	return func() tea.Msg {
		loader := m.clipboardLoader
		if loader == nil {
			loader = loadClipboardImage
		}
		msg, err := loader()
		if err == nil {
			return msg
		}
		if errors.Is(err, image.ErrClipboardImageUnavailable) {
			return textPasteMsg{Content: original.Content}
		}
		return systemNotifyMsg{Text: fmt.Sprintf(m.t("image.clipboard_failed"), err)}
	}
}

func (m *Model) handleRestartCommand(text string) tea.Cmd {
	// Check for "debug" argument
	parts := strings.Fields(text)
	debugMode := false
	for _, p := range parts[1:] {
		if strings.EqualFold(p, "debug") {
			debugMode = true
		}
	}
	if debugMode {
		m.restartDebug = true
	}
	m.chatWriteSystem(nextSystemID(), "Restarting ggcode...")
	if debugMode {
		m.chatWriteSystem(nextSystemID(), "  (debug mode enabled: GGCODE_DEBUG=1)")
	}
	m.quitting = true
	m.restartRequested = true
	m.shutdownAll()
	return tea.Quit
}

// buildRestartArgs reconstructs the CLI arguments needed to relaunch ggcode
// with the same configuration and resume the current session.
func (m *Model) buildRestartArgs() []string {
	var args []string

	// Preserve config file from the update service.
	if m.updateSvc != nil && m.updateSvc.ConfigPath != "" {
		args = append(args, "--config", m.updateSvc.ConfigPath)
	}

	// Resume current session.
	if m.session != nil && m.session.ID != "" {
		args = append(args, "--resume", m.session.ID)
	}

	// Preserve bypass mode.
	if m.mode == permission.BypassMode {
		args = append(args, "--bypass")
	}

	return args
}

// handleBranchCommand forks the current conversation into a new session.
// The new session gets a copy of all messages and metadata, allowing the user
// to explore a different direction without losing the original conversation.
func (m *Model) handleBranchCommand() tea.Cmd {
	if m.loading {
		m.chatWriteSystem(nextSystemID(), m.t("branch.busy"))
		m.chatListScrollToBottom()
		return nil
	}
	if m.session == nil || m.sessionStore == nil {
		m.chatWriteSystem(nextSystemID(), m.t("branch.no_session"))
		m.chatListScrollToBottom()
		return nil
	}
	if len(m.session.Messages) == 0 {
		m.chatWriteSystem(nextSystemID(), m.t("branch.empty"))
		m.chatListScrollToBottom()
		return nil
	}

	// Flush final metadata for the current session before branching.
	// Messages are already on disk via AppendMessageToDisk — Save() would
	// overwrite them because ses.Messages lacks agent replies.
	oldSes := m.session
	oldStore := m.sessionStore
	safego.Go("tui.branch.metaFlush", func() {
		if jsonlStore, ok := oldStore.(*session.JSONLStore); ok {
			_ = jsonlStore.AppendMetaToDisk(oldSes)
		}
	})

	// Create the branched session with copied data (cannot copy Session by
	// value because it contains a sync.RWMutex).
	branched := session.NewSession(oldSes.Vendor, oldSes.Endpoint, oldSes.Model)
	branched.Workspace = oldSes.Workspace
	branched.TokenUsage = oldSes.TokenUsage
	branched.CostJSON = append([]byte(nil), oldSes.CostJSON...)
	branched.PermissionMode = oldSes.PermissionMode
	if oldSes.SidebarVisible != nil {
		val := *oldSes.SidebarVisible
		branched.SidebarVisible = &val
	}

	// Deep-copy messages.
	branched.Messages = make([]provider.Message, len(oldSes.Messages))
	copy(branched.Messages, oldSes.Messages)

	// Deep-copy usage history.
	if len(oldSes.UsageHistory) > 0 {
		branched.UsageHistory = make([]session.UsageEntry, len(oldSes.UsageHistory))
		copy(branched.UsageHistory, oldSes.UsageHistory)
	}
	if len(oldSes.Metrics) > 0 {
		branched.Metrics = make([]metrics.MetricEvent, len(oldSes.Metrics))
		copy(branched.Metrics, oldSes.Metrics)
	}
	branched.EndpointUsage = make(map[string]provider.TokenUsage, len(oldSes.EndpointUsage))
	for k, v := range oldSes.EndpointUsage {
		branched.EndpointUsage[k] = v
	}
	branched.EndpointMetrics = make(map[string][]metrics.MetricEvent, len(oldSes.EndpointMetrics))
	for k, v := range oldSes.EndpointMetrics {
		cp := make([]metrics.MetricEvent, len(v))
		copy(cp, v)
		branched.EndpointMetrics[k] = cp
	}

	// Title: indicate this is a branch.
	origTitle := oldSes.Title
	if origTitle == "" {
		origTitle = oldSes.ID
	}
	branched.Title = "Branch: " + origTitle

	// Persist the new session: touch file + update index, then write messages
	// and metadata explicitly (Save no longer writes messages).
	if jsonlStore, ok := m.sessionStore.(*session.JSONLStore); ok {
		if err := jsonlStore.Save(branched); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("branch.save_failed", err))
			m.chatListScrollToBottom()
			return nil
		}
		if err := jsonlStore.AppendMetaToDisk(branched); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("branch.save_failed", err))
			m.chatListScrollToBottom()
			return nil
		}
		if err := jsonlStore.AppendMessagesBatchToDisk(branched, branched.Messages); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("branch.save_failed", err))
			m.chatListScrollToBottom()
			return nil
		}
	} else {
		if err := m.sessionStore.Save(branched); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("branch.save_failed", err))
			m.chatListScrollToBottom()
			return nil
		}
	}

	// Switch to the branched session.
	m.applyResumedSession(branched)

	m.chatWriteSystem(nextSystemID(), m.t("branch.success", branched.ID, origTitle))
	m.chatListScrollToBottom()
	return nil
}
