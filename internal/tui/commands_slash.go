package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func (m *Model) handleClearChat() {

	if m.loading {
		m.chatWriteSystem(nextSystemID(), m.t("session.switch_blocked_running"))
		return
	}

	if m.subAgentMgr != nil {
		safego.Go("tui.clearChat.cancelSubAgents", func() {
			m.subAgentMgr.CancelAll()
		})
	}
	if m.swarmMgr != nil {
		safego.Go("tui.clearChat.cancelSwarm", func() {
			m.swarmMgr.CancelAll()
		})
	}

	if m.session != nil && m.sessionStore != nil {
		oldSes := m.session
		oldStore := m.sessionStore
		safego.Go("tui.clearChat.metaFlush", func() {
			if jsonlStore, ok := oldStore.(*session.JSONLStore); ok {
				if err := jsonlStore.AppendMetaToDisk(oldSes); err != nil {
					debug.Log("tui", "clearChat oldSes meta persist: %v", err)
				}
			}
		})
	}

	vendor, endpoint, model := "", "", ""
	if m.config != nil {
		vendor = m.config.Vendor
		endpoint = m.config.Endpoint
		model = m.config.Model
	}
	ses := session.NewSession(vendor, endpoint, model)

	if m.sessionStore != nil {
		if err := m.sessionStore.Save(ses); err != nil {
			debug.Log("tui", "clearChat persist new session: %v", err)
		}
	}

	m.switchToSession(ses, true)

	m.chatWriteSystem(nextSystemID(), m.t("session.new", ses.ID))
}

func (m *Model) switchToSession(ses *session.Session, isNew bool) {
	if ses == nil {
		return
	}

	m.resetConversationView()

	oldWorkspace := ""
	if m.session != nil {
		oldWorkspace = m.session.Workspace
	}

	if m.agent != nil {
		m.agent.Clear()

		if cpMgr := m.agent.CheckpointManager(); cpMgr != nil {
			cpMgr.Clear()
		}
		agentruntime.RestoreSessionIntoAgent(m.agent, ses)
	}

	if recoveryMsg, resumeTask := agentruntime.CheckCrashRecovery(ses.ID); recoveryMsg != "" {
		m.chatWrite(chat.NewSystemItem("crash-recovery", recoveryMsg, m.chatStyles))
		if m.agent != nil {
			m.agent.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: recoveryMsg}}})
		}

		agentruntime.SeedCrashDedupWindow(m.agent, ses.ID)

		m.crashResumeTask = resumeTask
	}

	if contMsg := agentruntime.CheckContinuation(ses.ID); contMsg != "" {
		m.chatWrite(chat.NewSystemItem("continuation", contMsg, m.chatStyles))
		if m.agent != nil {
			m.agent.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: contMsg}}})
		}
	}

	m.SetSession(ses, m.sessionStore)

	if ses.Workspace != "" && oldWorkspace != "" && ses.Workspace != oldWorkspace {
		if m.agent != nil {
			m.agent.SetWorkingDir(ses.Workspace)
		}
		debug.Log("tui", "switchToSession: switched agent workingDir from %q to %q", oldWorkspace, ses.Workspace)
	}

	if m.sessionLockSwitch != nil {
		m.sessionLockSwitch(ses.ID)
	}

	if m.sessionCronSwitch != nil {
		m.sessionCronSwitch(ses.ID)
	}

	m.clearPendingApprovals()
	if isNew {

		if m.config != nil {
			globalMode := permission.ParsePermissionMode(m.config.DefaultMode)

			if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
				cp.SetMode(globalMode)
			}
			m.mode = globalMode
		}
	} else {

		if ses.PermissionMode != "" {
			sessionMode := permission.ParsePermissionMode(ses.PermissionMode)
			if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
				cp.SetMode(sessionMode)
			}
			m.mode = sessionMode
		}

		if ses.SidebarVisible != nil {
			m.sidebarVisible = *ses.SidebarVisible
		}
	}

	m.restoreTasksFromSession(ses)
	m.setResumeNote("")

	m.rebuildConversationFromMessages(ses.Messages)

	m.refreshCachedGitBranch()

	if !isNew {

		m.restoreHistoryFromMessages(ses.Messages)

		if recap := sessionRecap(ses, time.Now()); recap != "" {
			m.chatWriteSystem(nextSystemID(), recap)
		}

		if rec := m.prepareResumeReconciliation(ses); rec != "" {
			m.chatWriteSystem(nextSystemID(), rec)
		}
	}

	m.publishTunnelSnapshotForCurrentSession(true)
}

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

	m.todoSnapshot = nil
	m.todoOrder = nil
	m.activeTodo = nil
	m.runCanceled = false
	m.runFailed = false
	m.spinner.Stop()
	m.chatListScrollToBottom()
}

func (m *Model) clearPendingApprovals() {
	if pa := m.pendingApproval; pa != nil {
		m.pendingApproval = nil
		if pa.Response != nil {
			select {
			case pa.Response <- permission.Deny:
			default:
			}
		}
	}
	if pd := m.pendingDiffConfirm; pd != nil {
		m.pendingDiffConfirm = nil
		if pd.Response != nil {
			select {
			case pd.Response <- false:
			default:
			}
		}
	}

	if qs := m.pendingQuestionnaire; qs != nil {
		m.pendingQuestionnaire = nil
		m.tunnelPendingAskUserID = ""
		if qs.response != nil {
			resp := qs.buildResponse(toolpkg.AskUserStatusCancelled)
			select {
			case qs.response <- resp:
			default:
			}
		}
	}
	m.tunnelPendingApprovalID = ""
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
	m.oscWorking("")
	safego.Go("tui.commands.approvalRespond", func() {
		select {
		case pa.Response <- d:
		default:

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

		if cmd := extractCommandFromInput(pa.Input); cmd != "" {
			pattern := permission.CommandPrefixToPattern(cmd)
			if pattern != "" {
				m.policy.AllowCommandPattern(pattern)
				present := describeTool(m.currentLanguage(), pa.ToolName, pa.Input)
				toolLine := formatToolInline(present.DisplayName, present.Detail)
				if m.currentLanguage() == LangZhCN {
					m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 已总是允许：%s (%s)", toolLine, pattern))
				} else {
					m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 Always allow: %s (%s)", toolLine, pattern))
				}
			} else {
				m.policy.SetOverride(pa.ToolName, permission.Allow)
			}
		} else {
			m.policy.SetOverride(pa.ToolName, permission.Allow)
			present := describeTool(m.currentLanguage(), pa.ToolName, pa.Input)
			toolLine := formatToolInline(present.DisplayName, present.Detail)
			if m.currentLanguage() == LangZhCN {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 已总是允许：%s", toolLine))
			} else {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 Always allow: %s", toolLine))
			}
		}
	}

	m.persistPermissionRules()

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
	m.oscWorking("")
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

func (m Model) handleHistoryUp() (tea.Model, tea.Cmd) {

	if m.loading && m.input.Value() == "" && m.pendingSubmissionCount() > 0 {
		text, imgs, ok := m.dequeueLastVisible()
		if ok {
			m.input.SetValue(text)

			if len(imgs) > 0 {
				m.pendingImages = imgs
			}
			composerCursorEnd(&m.input)
			return m, nil
		}
	}
	if m.historyIdx > 0 {
		m.historyIdx--
		m.input.SetValue(m.history[m.historyIdx])

		composerCursorEnd(&m.input)
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

			if errors.Is(err, image.ErrClipboardImageUnavailable) {
				if runtime.GOOS == "windows" {
					return systemNotifyMsg{Text: m.t("image.clipboard_no_image_windows")}
				}
				return systemNotifyMsg{Text: m.t("image.clipboard_no_image")}
			}

			return systemNotifyMsg{Text: fmt.Sprintf(m.t("image.clipboard_failed"), err)}
		}
		return msg
	}
}

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
		if errors.Is(err, image.ErrClipboardImageUnavailable) || strings.TrimSpace(original.Content) != "" {
			return textPasteMsg{Content: original.Content}
		}
		return systemNotifyMsg{Text: fmt.Sprintf(m.t("image.clipboard_failed"), err)}
	}
}

func (m *Model) handleRestartCommand(text string) tea.Cmd {

	parts := strings.Fields(text)
	debugMode := false
	for _, p := range parts[1:] {
		if strings.EqualFold(p, "debug") {
			debugMode = true
		}
	}
	return m.beginRestart(debugMode)
}

func (m *Model) beginRestart(debugMode bool) tea.Cmd {
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

func (m *Model) buildRestartArgs() []string {
	var args []string

	if m.updateSvc != nil && m.updateSvc.ConfigPath != "" {
		args = append(args, "--config", m.updateSvc.ConfigPath)
	}

	if m.session != nil && m.session.ID != "" {
		args = append(args, "--resume", m.session.ID)
	}

	if m.mode == permission.BypassMode {
		args = append(args, "--bypass")
	}

	return args
}

func (m *Model) handleBranchCommand(parts []string) tea.Cmd {
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

	dropRounds := 0
	if len(parts) > 1 {
		n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || n < 0 {
			m.chatWriteSystem(nextSystemID(), m.t("branch.bad_arg"))
			m.chatListScrollToBottom()
			return nil
		}
		dropRounds = n
	}
	cutoff, ok := session.ComputeBranchCutoff(m.session.Messages, dropRounds)
	if !ok {
		m.chatWriteSystem(nextSystemID(), m.t("branch.back_too_far"))
		m.chatListScrollToBottom()
		return nil
	}

	oldSes := m.session
	oldStore := m.sessionStore
	safego.Go("tui.branch.metaFlush", func() {
		if jsonlStore, ok := oldStore.(*session.JSONLStore); ok {
			if err := jsonlStore.AppendMetaToDisk(oldSes); err != nil {
				debug.Log("tui", "branch oldSes meta persist: %v", err)
			}
		}
	})

	branched := session.NewSession(oldSes.Vendor, oldSes.Endpoint, oldSes.Model)
	branched.Workspace = oldSes.Workspace
	branched.TokenUsage = oldSes.TokenUsage
	branched.CostJSON = append([]byte(nil), oldSes.CostJSON...)
	branched.PermissionMode = oldSes.PermissionMode

	m.snapshotTasksInto(oldSes)
	branched.TasksJSON = append([]byte(nil), oldSes.TasksJSON...)

	branched.TasksEnvJSON = append([]byte(nil), oldSes.TasksEnvJSON...)
	if oldSes.SidebarVisible != nil {
		val := *oldSes.SidebarVisible
		branched.SidebarVisible = &val
	}

	branched.Messages = make([]provider.Message, cutoff)
	copy(branched.Messages, oldSes.Messages[:cutoff])
	branched.ParentSessionID = oldSes.ID
	branched.ForkPoint = cutoff

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

	origTitle := oldSes.Title
	if origTitle == "" {
		origTitle = oldSes.ID
	}
	branched.Title = "Branch: " + origTitle
	if dropRounds > 0 {
		branched.Title = fmt.Sprintf("Branch (-%d turns): %s", dropRounds, origTitle)
	}

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

	m.applyResumedSession(branched)

	m.chatWriteSystem(nextSystemID(), m.t("branch.success", branched.ID, origTitle))
	m.chatListScrollToBottom()
	return nil
}

func (m *Model) handleBranchesCommand(parts []string) tea.Cmd {
	if m.session == nil || m.sessionStore == nil {
		m.chatWriteSystem(nextSystemID(), m.t("branch.no_session"))
		m.chatListScrollToBottom()
		return nil
	}
	jsonlStore, ok := m.sessionStore.(*session.JSONLStore)
	if !ok {
		m.chatWriteSystem(nextSystemID(), "Branch listing requires a JSONL session store.")
		m.chatListScrollToBottom()
		return nil
	}
	own, err := jsonlStore.ListChildren(m.session.ID)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to list branches: %v", err))
		m.chatListScrollToBottom()
		return nil
	}

	var lines []string
	if m.session.ParentSessionID != "" {
		if parent, perr := jsonlStore.Load(m.session.ParentSessionID); perr == nil && parent != nil {
			pt := parent.Title
			if pt == "" {
				pt = parent.ID
			}
			lines = append(lines, fmt.Sprintf("Parent: %s (%s)", pt, parent.ID))
		} else {
			lines = append(lines, fmt.Sprintf("Parent: %s", m.session.ParentSessionID))
		}
		siblings, serr := jsonlStore.ListChildren(m.session.ParentSessionID)
		if serr == nil && len(siblings) > 0 {
			lines = append(lines, "Sibling branches ('>' marks this session):")
			for _, sib := range siblings {
				marker := "  "
				if sib.ID == m.session.ID {
					marker = "> "
				}
				lines = append(lines, fmt.Sprintf("%s%s (%s, forked at msg %d)", marker, sib.Title, sib.ID, sib.ForkPoint))
			}
		}
	}
	if len(own) > 0 {
		lines = append(lines, fmt.Sprintf("Branches of this session (%d):", len(own)))
		for _, ch := range own {
			lines = append(lines, fmt.Sprintf("  %s (%s, forked at msg %d)", ch.Title, ch.ID, ch.ForkPoint))
		}
	} else if m.session.ParentSessionID == "" {
		lines = append(lines, "No branches yet. Use /branch [N] to fork this session.")
	}
	m.chatWriteSystem(nextSystemID(), strings.Join(lines, "\n"))
	m.chatListScrollToBottom()
	return nil
}

func (m *Model) handleBranchSwitchCommand(parts []string) tea.Cmd {
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
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		m.chatWriteSystem(nextSystemID(), "Usage: /branch-switch <session-id> (see /branches for IDs)")
		m.chatListScrollToBottom()
		return nil
	}
	id := strings.TrimSpace(parts[1])

	jsonlStore, ok := m.sessionStore.(*session.JSONLStore)
	if !ok {
		m.chatWriteSystem(nextSystemID(), "Branch switching requires a JSONL session store.")
		m.chatListScrollToBottom()
		return nil
	}
	related := id == m.session.ID || id == m.session.ParentSessionID
	if !related {
		if own, err := jsonlStore.ListChildren(m.session.ID); err == nil {
			for _, ch := range own {
				if ch.ID == id {
					related = true
					break
				}
			}
		}
	}
	if !related && m.session.ParentSessionID != "" {
		if sibs, err := jsonlStore.ListChildren(m.session.ParentSessionID); err == nil {
			for _, sib := range sibs {
				if sib.ID == id {
					related = true
					break
				}
			}
		}
	}
	if !related {
		m.chatWriteSystem(nextSystemID(), "That session is not in the current branch tree. Use /resume <id> for arbitrary sessions.")
		m.chatListScrollToBottom()
		return nil
	}

	target, err := m.sessionStore.Load(id)
	if err != nil || target == nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to load session %s: %v", id, err))
		m.chatListScrollToBottom()
		return nil
	}

	oldSes := m.session
	oldStore := m.sessionStore
	safego.Go("tui.branchSwitch.metaFlush", func() {
		if js, ok := oldStore.(*session.JSONLStore); ok {
			if err := js.AppendMetaToDisk(oldSes); err != nil {
				debug.Log("tui", "branchSwitch meta persist: %v", err)
			}
		}
	})

	m.applyResumedSession(target)
	title := target.Title
	if title == "" {
		title = target.ID
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Switched to %s (%s).", title, target.ID))
	m.chatListScrollToBottom()
	return nil
}

func extractCommandFromInput(input string) string {
	return permission.ExtractCommandFromInput(input)
}

func (m *Model) persistPermissionRules() {
	if m.policy == nil {
		return
	}
	cp, ok := m.policy.(*permission.ConfigPolicy)
	if !ok {
		return
	}
	rulesPath := permission.RulesFilePath(config.ConfigDir())
	data := permission.SnapshotRules(cp, config.ConfigDir())
	if err := permission.SaveRules(rulesPath, data); err != nil {
		debug.Log("tui", "failed to persist permission rules: %v", err)
	}
}

func (m *Model) handleNotifyCommand(args []string) tea.Cmd {
	if m.config == nil {
		m.chatWriteSystem(nextSystemID(), "Configuration not available.")
		m.chatListScrollToBottom()
		return nil
	}

	if len(args) == 0 {
		n := m.config.Notifications
		mode := n.EffectiveMode()
		bell := "off"
		if n.ShouldBell() {
			bell = "on"
		}
		desktop := "off"
		if n.Desktop {
			desktop = "on"
		}
		minDur := n.EffectiveMinDuration()
		msg := fmt.Sprintf("Notification settings:\n  mode: %s (all=every run, long=>Nsec, errors=failures only, off=disabled)\n  bell: %s (terminal bell on completion)\n  desktop: %s (OS desktop notification)\n  min_duration_sec: %d (threshold for 'long' mode)\n\nUsage: /notify mode <all|long|errors|off> | bell <on|off> | desktop <on|off> | min_duration <seconds>",
			mode, bell, desktop, minDur)
		m.chatWriteSystem(nextSystemID(), msg)
		m.chatListScrollToBottom()
		return nil
	}

	changed := false
	i := 0
	for i < len(args) {
		key := args[i]
		switch key {
		case "mode":
			if i+1 >= len(args) {
				m.chatWriteSystem(nextSystemID(), "Usage: /notify mode <all|long|errors|off>")
				m.chatListScrollToBottom()
				return nil
			}
			val := args[i+1]
			switch val {
			case "all", "long", "errors", "off":
				m.config.Notifications.Mode = val
				changed = true
			default:
				m.chatWriteSystem(nextSystemID(), "Invalid mode. Use: all, long, errors, or off.")
				m.chatListScrollToBottom()
				return nil
			}
			i += 2
		case "bell":
			if i+1 >= len(args) {
				m.chatWriteSystem(nextSystemID(), "Usage: /notify bell <on|off>")
				m.chatListScrollToBottom()
				return nil
			}
			bell := parseOnOff(args[i+1])
			m.config.Notifications.Bell = &bell
			changed = true
			i += 2
		case "desktop":
			if i+1 >= len(args) {
				m.chatWriteSystem(nextSystemID(), "Usage: /notify desktop <on|off>")
				m.chatListScrollToBottom()
				return nil
			}
			m.config.Notifications.Desktop = parseOnOff(args[i+1])
			changed = true
			i += 2
		case "min_duration":
			if i+1 >= len(args) {
				m.chatWriteSystem(nextSystemID(), "Usage: /notify min_duration <seconds>")
				m.chatListScrollToBottom()
				return nil
			}
			sec, err := strconv.Atoi(args[i+1])
			if err != nil || sec < 0 {
				m.chatWriteSystem(nextSystemID(), "Invalid seconds value. Use a non-negative integer.")
				m.chatListScrollToBottom()
				return nil
			}
			m.config.Notifications.MinDuration = sec
			changed = true
			i += 2
		default:
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Unknown key %q. Use: mode, bell, desktop, or min_duration.", key))
			m.chatListScrollToBottom()
			return nil
		}
	}

	if changed {
		if err := m.config.Save(); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to save notification settings: %v", err))
		} else {
			n := m.config.Notifications
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf(
				"Notification settings saved: mode=%s, bell=%v, desktop=%v, min_duration=%ds",
				n.EffectiveMode(), n.ShouldBell(), n.Desktop, n.EffectiveMinDuration(),
			))
		}
	}
	m.chatListScrollToBottom()
	return nil
}

func parseOnOff(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "true", "1", "yes":
		return true
	default:
		return false
	}
}
