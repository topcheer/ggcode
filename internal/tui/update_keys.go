package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/util"
	"strings"
	"time"
)

// handleKeyPress handles the tea.KeyPressMsg case.
func (m Model) handleKeyPress(msg tea.KeyPressMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
	// During startup input drain, suppress all keyboard input.
	// This prevents terminal responses (OSC 11, CPR, Kitty mode report)
	// from appearing as garbage in the text input field.
	if !m.inputDrainUntil.IsZero() && time.Now().Before(m.inputDrainUntil) {
		debug.Log("tui", "KEYPRESS dropped (input drain) key=%q text=%q", msg.String(), msg.Text)
		return m, nil
	}
	if m.startupBannerVisible {
		m.startupBannerVisible = false
	}
	if msg.String() != "ctrl+c" {
		m.resetExitConfirm()
	}
	debug.Log("tui", "KEYPRESS str=%q text=%q mod=%v code=%v input_before=%q", msg.String(), msg.Text, msg.Mod, msg.Code, util.Truncate(m.input.Value(), 80))
	if m.tmuxMenuOpen {
		return m.handleTmuxMenuKey(msg.String())
	}
	if msg.String() == "ctrl+x" {
		m.openTmuxMenu()
		return m, nil
	}
	if msg.String() == "ctrl+r" {
		m.sidebarVisible = !m.sidebarVisible
		if m.config != nil {
			_ = m.config.SaveSidebarPreference(m.sidebarVisible)
		}
		m.relayoutAfterSidebarChange()
		return m, nil
	}
	if msg.String() == "ctrl+g" {
		effort, ok := m.cycleReasoningEffort()
		if ok {
			label := displayReasoningEffort(effort)
			m.statusActivity = fmt.Sprintf("Reasoning effort: %s", label)
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Reasoning effort set to %s for this session", label))
		} else {
			m.statusActivity = "Reasoning effort not supported by current provider"
			m.chatWriteSystem(nextSystemID(), "Reasoning effort is not supported by the current provider")
		}
		return m, nil
	}
	if msg.String() == "ctrl+f" {
		m.toggleFileBrowser()
		return m, nil
	}
	if m.fileBrowser != nil {
		return m.handleFileBrowserKey(msg)
	}
	if m.previewPanel != nil {
		return m.handlePreviewKey(msg)
	}

	if msg.String() == "ctrl+c" && !m.loading && len(m.langOptions) == 0 && m.closeActivePanel() {
		return m, nil
	}

	// Toggle config save scope (global ↔ instance) in any panel.
	if msg.String() == "ctrl+t" {
		_ = m.toggleConfigSaveScope()
		return m, nil
	}

	// Handle approval mode (selection list)
	if m.modelPanel != nil {
		return m.handleModelPanelKey(msg)
	}

	if m.providerPanel != nil {
		return m.handleProviderPanelKey(msg)
	}

	// QR overlay takes priority over all IM panels — Esc/q returns to the panel behind it.
	if m.qrOverlay != nil {
		return m.handleQROverlayKey(msg)
	}

	if m.qqPanel != nil {
		return m.handleQQPanelKey(msg)
	}

	if m.tgPanel != nil {
		return m.handleTGPanelKey(msg)
	}

	if m.pcPanel != nil {
		return m.handlePCPanelKey(msg)
	}

	if m.discordPanel != nil {
		return m.handleDiscordPanelKey(msg)
	}

	if m.feishuPanel != nil {
		return m.handleFeishuPanelKey(msg)
	}

	if m.slackPanel != nil {
		return m.handleSlackPanelKey(msg)
	}

	if m.dingtalkPanel != nil {
		return m.handleDingtalkPanelKey(msg)
	}

	if m.wechatPanel != nil {
		return m.handleWechatPanelKey(msg)
	}
	if m.wecomPanel != nil {
		return m.handleWeComPanelKey(msg)
	}
	if m.mattermostPanel != nil {
		return m.handleMattermostPanelKey(msg)
	}
	if m.matrixPanel != nil {
		return m.handleMatrixPanelKey(msg)
	}
	if m.signalPanel != nil {
		return m.handleSignalPanelKey(msg)
	}
	if m.ircPanel != nil {
		return m.handleIRCPanelKey(msg)
	}
	if m.nostrPanel != nil {
		return m.handleNostrPanelKey(msg)
	}
	if m.twitchPanel != nil {
		return m.handleTwitchPanelKey(msg)
	}

	if m.whatsappPanel != nil {
		return m.handleWhatsAppPanelKey(msg)
	}

	if m.imPanel != nil {
		return m.handleIMPanelKey(msg)
	}

	if m.mcpPanel != nil {
		return m.handleMCPPanelKey(msg)
	}

	if m.streamPanel != nil {
		return m.updateStreamPanel(msg)
	}

	if m.knightPanel != nil {
		return m.updateKnightPanel(msg)
	}

	if m.impersonatePanel != nil {
		return m.handleImpersonatePanelKey(msg)
	}

	if m.skillsPanel != nil {
		return m.handleSkillsPanelKey(msg)
	}

	if m.statsPanel != nil {
		return m.handleStatsPanelKey(msg)
	}

	if m.inspectorPanel != nil {
		return m.handleInspectorPanelKey(msg)
	}

	if m.harnessContextPrompt != nil {
		return m.handleHarnessContextPromptKey(msg)
	}

	if m.harnessPanel != nil {
		return m.handleHarnessPanelKey(msg)
	}

	if m.pendingPairingChallenge() != nil {
		switch msg.String() {
		case "esc":
			return m, m.rejectPendingPairing()
		case "ctrl+c":
			if m.loading {
				m.resetExitConfirm()
				m.cancelActiveRun()
				return m, nil
			}
			if m.exitConfirmPending {
				m.quitting = true
				m.shutdownAll()
				return m, tea.Quit
			}
			m.promptExitConfirm()
			return m, nil
		default:
			return m, nil
		}
	}

	if m.pendingQuestionnaire != nil {
		return m.handleQuestionnaireKey(msg)
	}

	if len(m.langOptions) > 0 {
		switch msg.String() {
		case "up", "k":
			m.langCursor = (m.langCursor - 1 + len(m.langOptions)) % len(m.langOptions)
			return m, nil
		case "down", "j", "tab":
			m.langCursor = (m.langCursor + 1) % len(m.langOptions)
			return m, nil
		case "shift+tab":
			m.langCursor = (m.langCursor - 1 + len(m.langOptions)) % len(m.langOptions)
			return m, nil
		case "enter", "right":
			return m, m.applyLanguageSelection(m.langOptions[m.langCursor].lang)
		case "e", "E":
			return m, m.applyLanguageSelection(LangEnglish)
		case "z", "Z":
			return m, m.applyLanguageSelection(LangZhCN)
		case "esc":
			if m.languagePromptRequired {
				return m, nil
			}
			m.langOptions = nil
			return m, nil
		case "ctrl+c":
			m.promptExitConfirm()
			return m, nil
		}
		return m, nil
	}

	// Handle approval mode (selection list)
	if m.pendingApproval != nil {
		switch msg.String() {
		case "up", "k":
			m.approvalCursor = (m.approvalCursor - 1 + len(m.approvalOptions)) % len(m.approvalOptions)
			return m, nil
		case "down", "j":
			m.approvalCursor = (m.approvalCursor + 1) % len(m.approvalOptions)
			return m, nil
		case "tab":
			m.approvalCursor = (m.approvalCursor + 1) % len(m.approvalOptions)
			return m, nil
		case "shift+tab":
			m.approvalCursor = (m.approvalCursor - 1 + len(m.approvalOptions)) % len(m.approvalOptions)
			return m, nil
		case "enter", "right":
			opt := m.approvalOptions[m.approvalCursor]
			if opt.shortcut == "a" {
				return m, m.handleApprovalAllowAlways()
			}
			return m, m.handleApproval(opt.decision)
		case "y", "Y":
			return m, m.handleApproval(permission.Allow)
		case "n", "N":
			return m, m.handleApproval(permission.Deny)
		case "a", "A":
			return m, m.handleApprovalAllowAlways()
		case "esc", "ctrl+c":
			return m, m.handleApproval(permission.Deny)
		}
		return m, nil
	}

	// Handle diff confirmation mode (selection list)
	if m.pendingDiffConfirm != nil {
		switch msg.String() {
		case "up", "k":
			m.diffCursor = (m.diffCursor - 1 + len(m.diffOptions)) % len(m.diffOptions)
			return m, nil
		case "down", "j":
			m.diffCursor = (m.diffCursor + 1) % len(m.diffOptions)
			return m, nil
		case "tab":
			m.diffCursor = (m.diffCursor + 1) % len(m.diffOptions)
			return m, nil
		case "shift+tab":
			m.diffCursor = (m.diffCursor - 1 + len(m.diffOptions)) % len(m.diffOptions)
			return m, nil
		case "enter", "right":
			opt := m.diffOptions[m.diffCursor]
			return m, m.handleDiffConfirm(opt.decision == permission.Allow)
		case "y", "Y":
			return m, m.handleDiffConfirm(true)
		case "n", "N":
			return m, m.handleDiffConfirm(false)
		case "esc", "ctrl+c":
			return m, m.handleDiffConfirm(false)
		}
		return m, nil
	}
	if m.pendingHarnessCheckpointConfirm != nil {
		switch msg.String() {
		case "up", "k":
			m.diffCursor = (m.diffCursor - 1 + len(m.diffOptions)) % len(m.diffOptions)
			return m, nil
		case "down", "j":
			m.diffCursor = (m.diffCursor + 1) % len(m.diffOptions)
			return m, nil
		case "tab":
			m.diffCursor = (m.diffCursor + 1) % len(m.diffOptions)
			return m, nil
		case "shift+tab":
			m.diffCursor = (m.diffCursor - 1 + len(m.diffOptions)) % len(m.diffOptions)
			return m, nil
		case "enter", "right":
			opt := m.diffOptions[m.diffCursor]
			return m, m.handleHarnessCheckpointConfirm(opt.decision == permission.Allow)
		case "y", "Y":
			return m, m.handleHarnessCheckpointConfirm(true)
		case "n", "N":
			return m, m.handleHarnessCheckpointConfirm(false)
		case "esc", "ctrl+c":
			return m, m.handleHarnessCheckpointConfirm(false)
		}
		return m, nil
	}

	if msg.String() == "esc" && m.previewPanel != nil && !m.subAgentFollow.isActive() {
		m.closePreviewPanel()
		return m, nil
	}

	if m.loading && (msg.String() == "ctrl+c" || msg.String() == "esc") && !m.subAgentFollow.isActive() {
		m.resetExitConfirm()
		m.cancelActiveRun()
		return m, nil
	}

	switch msg.String() {
	case "ctrl+n":
		// Follow mode: toggle panel on/off
		if len(m.subAgentFollow.slots) > 0 {
			if m.subAgentFollow.isActive() {
				m.subAgentFollow.deactivate()
			} else {
				m.subAgentFollow.activate(0)
				m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
			}
			return m, nil
		}
	case "ctrl+p":
		// Removed: use arrow keys to navigate in follow mode
	case "$", "!":
		if !m.shellMode && !m.loading && !m.projectMemoryLoading && strings.TrimSpace(m.input.Value()) == "" {
			m.setShellMode(true)
			return m, nil
		}
	case "ctrl+c":
		if m.autoCompleteActive {
			m.autoCompleteActive = false
			m.autoCompleteItems = nil
			m.resetExitConfirm()
			return m, nil
		}
		if m.exitConfirmPending {
			m.quitting = true
			m.shutdownAll()
			return m, tea.Quit
		}
		m.promptExitConfirm()
		return m, nil
	case "ctrl+v":
		// Clipboard image paste — allowed while loading so users can
		// attach images for interleaved messages during agent runs.
		return m, m.handleClipboardPaste()
	case "ctrl+d":
		m.quitting = true
		m.shutdownAll()
		return m, tea.Quit
	case "shift+tab":
		if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
			m.autoCompleteIndex = (m.autoCompleteIndex - 1 + len(m.autoCompleteItems)) % len(m.autoCompleteItems)
			return m, nil
		}
		return m.handleModeSwitch()
	case "pgup":
		if m.chatList != nil {
			m.chatList.ScrollUp(m.chatList.Height() / 2)
		}
		return m, nil
	case "pgdown":
		if m.chatList != nil {
			m.chatList.ScrollDown(m.chatList.Height() / 2)
		}
		return m, nil

	case "up":
		// Follow mode: navigate to previous slot
		if m.subAgentFollow.isActive() && len(m.subAgentFollow.slots) > 0 {
			currentIdx := m.subAgentFollow.currentSlotIndex()
			if currentIdx > 0 {
				m.subAgentFollow.activate(currentIdx - 1)
			} else {
				m.subAgentFollow.activate(len(m.subAgentFollow.slots) - 1)
			}
			m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
			return m, nil
		}
		if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
			m.autoCompleteIndex = (m.autoCompleteIndex - 1 + len(m.autoCompleteItems)) % len(m.autoCompleteItems)
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		return m.handleHistoryUp()
	case "down":
		// Follow mode: navigate to next slot
		if m.subAgentFollow.isActive() && len(m.subAgentFollow.slots) > 0 {
			currentIdx := m.subAgentFollow.currentSlotIndex()
			if currentIdx < len(m.subAgentFollow.slots)-1 {
				m.subAgentFollow.activate(currentIdx + 1)
			} else {
				m.subAgentFollow.activate(0)
			}
			m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
			return m, nil
		}
		if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
			m.autoCompleteIndex = (m.autoCompleteIndex + 1) % len(m.autoCompleteItems)
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		return m.handleHistoryDown()
	case "left":
		// Follow mode: navigate to previous slot (same as up)
		if m.subAgentFollow.isActive() && len(m.subAgentFollow.slots) > 0 {
			currentIdx := m.subAgentFollow.currentSlotIndex()
			if currentIdx > 0 {
				m.subAgentFollow.activate(currentIdx - 1)
			} else {
				m.subAgentFollow.activate(len(m.subAgentFollow.slots) - 1)
			}
			m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
			return m, nil
		}
	case "right":
		// Follow mode: navigate to next slot (same as down)
		if m.subAgentFollow.isActive() && len(m.subAgentFollow.slots) > 0 {
			currentIdx := m.subAgentFollow.currentSlotIndex()
			if currentIdx < len(m.subAgentFollow.slots)-1 {
				m.subAgentFollow.activate(currentIdx + 1)
			} else {
				m.subAgentFollow.activate(0)
			}
			m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
			return m, nil
		}
	case "tab":
		if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
			if len(m.autoCompleteItems) == 1 {
				return m, m.applyAutoComplete()
			}
			m.autoCompleteIndex = (m.autoCompleteIndex + 1) % len(m.autoCompleteItems)
			return m, nil
		}
	case "esc":
		// Handle pending auto-run suggestion: Esc dismisses (before autocomplete)
		// Handle pending harness review: Esc skips review
		if m.pendingHarnessReview != nil {
			taskID := m.pendingHarnessReview.ID
			m.pendingHarnessReview = nil
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Review skipped for task %s. Use /harness review approve %s to approve later.", taskID, taskID))
			m.chatListScrollToBottom()
			return m, nil
		}
		// Handle pending harness promote: Esc skips promote
		if m.pendingHarnessPromote != nil {
			taskID := m.pendingHarnessPromote.ID
			m.pendingHarnessPromote = nil
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Promote skipped for task %s. Use /harness promote apply %s to promote later.", taskID, taskID))
			m.chatListScrollToBottom()
			return m, nil
		}
		if m.pendingAutoRun != nil {
			text := m.pendingAutoRunText
			m.pendingAutoRun = nil
			m.pendingAutoRunText = ""
			m.chatWriteSystem(nextSystemID(), "Running normally (harness skipped).")
			m.chatListScrollToBottom()
			return m, m.continueDisplayedNormalTextRun(text)
		}
		if m.autoCompleteActive {
			m.autoCompleteActive = false
			m.autoCompleteItems = nil
			return m, nil
		}
		// Sub-agent follow mode: Esc exits follow
		if m.subAgentFollow.isActive() {
			m.subAgentFollow.deactivate()
			return m, nil
		}
		if m.shellMode && !m.loading {
			m.setShellMode(false)
			m.input.Reset()
			return m, nil
		}
	case "enter":
		// Handle pending auto-run suggestion: Enter confirms harness run (before autocomplete)
		// Handle pending harness review: Enter approves the task
		if m.pendingHarnessReview != nil {
			task := m.pendingHarnessReview
			m.pendingHarnessReview = nil
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Approving task %s...", task.ID))
			m.chatListScrollToBottom()
			return m, m.handleHarnessReviewApprove(task.ID)
		}
		// Handle pending harness promote: Enter promotes the task
		if m.pendingHarnessPromote != nil {
			task := m.pendingHarnessPromote
			m.pendingHarnessPromote = nil
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Promoting task %s...", task.ID))
			m.chatListScrollToBottom()
			return m, m.handleHarnessPromoteApply(task.ID)
		}
		if m.pendingAutoRun != nil {
			result := m.pendingAutoRun
			text := m.pendingAutoRunText
			m.pendingAutoRun = nil
			m.pendingAutoRunText = ""
			m.chatWriteSystem(nextSystemID(), "Running in harness...")
			m.chatListScrollToBottom()
			return m, m.handleAutoRun(text, result)
		}
		if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
			return m, m.applyAutoComplete()
		}
		m.resetExitConfirm()
		// Clear stale auto-run suggestion when user submits new text
		if m.pendingAutoRun != nil {
			m.pendingAutoRun = nil
			m.pendingAutoRunText = ""
		}
		if m.pendingHarnessReview != nil {
			m.pendingHarnessReview = nil
		}
		if m.pendingHarnessPromote != nil {
			m.pendingHarnessPromote = nil
		}
		text := strings.TrimSpace(m.input.Value())
		m.input.Reset()
		if text == "" {
			m.chatListScrollToBottom()
			return m, nil
		}
		if m.shellMode {
			m.emitIMLocalUserText("$ " + text)
			if m.loading || m.projectMemoryLoading {
				m.history = append(m.history, "$ "+text)
				m.historyIdx = len(m.history)
				m.queuePendingSubmission(text)
				return m, nil
			}
			return m, m.submitShellCommand(text, true)
		}
		m.emitIMLocalUserText(text)
		if m.loading || m.projectMemoryLoading {
			if shouldExecuteWhileBusy(text) {
				return m, m.submitText(text, true)
			}
			m.history = append(m.history, text)
			m.historyIdx = len(m.history)
			m.queuePendingSubmission(text)
			return m, nil
		}
		return m, m.submitText(text, true)
	case "shift+enter":
		// Shift+Enter inserts newline into textarea.
		// Use InsertRune('\n') instead of manual string splicing + SetValue
		// so that the textarea's internal cursor/row/col state stays correct.
		// SetValue resets the cursor to the end, causing visual glitches.
		m.input.InsertRune('\n')
		m.updateAutoComplete()
		return m, nil
	}

	// Forward unmatched keys to text input (was the catchall in original Update)
	oldValue := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	newValue := m.input.Value()
	m.updateAutoComplete()
	if oldValue != newValue {
		m.inputHint = ""
	}
	return m, combineCmds(spinnerCmd, cmd)

}
