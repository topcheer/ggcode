package tui

import (
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"strings"
	"time"
)

func anyPanelOpen(m Model) bool {
	return m.hasActivePanel() ||
		m.tmuxMenuOpen ||
		m.pendingQuestionnaire != nil ||
		m.qrOverlay != nil ||
		m.initPromptActive
}

func (m Model) handleKeyPress(msg tea.KeyPressMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {

	if !m.inputDrainUntil.IsZero() && time.Now().Before(m.inputDrainUntil) {

		return m, nil
	}
	if msg.String() != "ctrl+c" {
		m.resetExitConfirm()
	}
	if msg.String() != "ctrl+c" && msg.String() != "esc" {
		m.resetCancelConfirm()
	}
	if (msg.String() == "ctrl+c" || msg.String() == "esc") && m.hasActivePanel() {
		m.resetCancelConfirm()
	}

	if m.tmuxMenuOpen {
		return m.handleTmuxMenuKey(msg.String())
	}

	if m.historySearch.active {
		handled, m2, cmd := m.handleHistorySearchKey(msg)
		if handled {
			return m2, cmd
		}
		m = m2
	} else if msg.String() == "alt+r" {
		m.beginHistorySearch()
		return m, nil
	}
	if msg.String() == "ctrl+x" {
		m.openTmuxMenu()
		return m, nil
	}
	if msg.String() == "ctrl+r" {
		m.sidebarVisible = !m.sidebarVisible
		m.persistSidebarPreference()
		m.relayoutAfterSidebarChange()
		return m, nil
	}
	if msg.String() == "ctrl+g" {
		effort, ok := m.cycleReasoningEffort()
		if ok {
			label := displayReasoningEffort(effort)
			m.statusActivity = m.t("reasoning.effort.status", label)
			m.chatWriteSystem(nextSystemID(), m.t("reasoning.effort.set", label))
		} else {
			m.statusActivity = m.t("reasoning.effort.unsupported.status")
			m.chatWriteSystem(nextSystemID(), m.t("reasoning.effort.unsupported"))
		}
		return m, nil
	}

	if msg.String() == "alt+e" {
		if m.chatList.ToggleLastExpandable() {
			m.chatListFollowOutput()
		}
		return m, nil
	}

	if msg.String() == "ctrl+o" {
		style, ok := m.cycleOutputStyle()
		if ok {
			label := config.DisplayOutputStyle(style)
			m.statusActivity = m.t("output.style.status", label)
			m.chatWriteSystem(nextSystemID(), m.t("output.style.set", label))
		}
		return m, nil
	}

	if msg.String() == "ctrl+\\" {
		m.compactMode = !m.compactMode
		m.sidebarVisible = !m.compactMode
		m.persistSidebarPreference()
		m.relayoutAfterSidebarChange()
		return m, nil
	}

	if anyPanelOpen(m) {

	} else if msg.String() == "alt+up" || msg.String() == "alt+k" {
		return m, m.cycleSession(-1)
	} else if msg.String() == "alt+down" || msg.String() == "alt+j" {
		return m, m.cycleSession(1)
	}

	if msg.String() == "ctrl+shift+c" {
		m.copyLastAssistantResponse()
		return m, nil
	}

	if msg.String() == "ctrl+l" {
		m.handleClearChat()
		return m, nil
	}

	if msg.String() == "ctrl+c" && m.subAgentFollow.isActive() {
		m.subAgentFollow.deactivate()
		return m, nil
	}

	if msg.String() == "ctrl+c" && len(m.langOptions) == 0 && m.closeActivePanel() {
		return m, nil
	}

	if msg.String() == "ctrl+t" {
		_ = m.toggleConfigSaveScope()
		return m, nil
	}

	if m.modelPanel != nil {
		return m.handleModelPanelKey(msg)
	}

	if m.providerPanel != nil {
		return m.handleProviderPanelKey(msg)
	}

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

	if m.usagePanel != nil {
		return m.handleUsagePanelKey(msg)
	}

	if m.hooksPanel != nil {
		return m.handleHooksPanelKey(msg)
	}

	if m.inspectorPanel != nil {
		return m.handleInspectorPanelKey(msg)
	}

	if m.initPromptActive {
		return m.handleInitPromptKey(msg)
	}

	if m.lanChatPanel != nil {
		return m.handleLanChatKey(msg)
	}

	if m.pendingPairingChallenge() != nil {
		switch msg.String() {
		case "esc":
			return m, m.rejectPendingPairing()
		case "ctrl+c":
			if m.loading {
				m.resetExitConfirm()
				if m.cancelConfirmPending {
					m.cancelActiveRun()
				} else {
					m.promptCancelConfirm()
				}
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
		case "2":
			return m, m.applyLanguageSelection(LangJa)
		case "3":
			return m, m.applyLanguageSelection(LangKo)
		case "4":
			return m, m.applyLanguageSelection(LangEs)
		case "5":
			return m, m.applyLanguageSelection(LangFr)
		case "6":
			return m, m.applyLanguageSelection(LangDe)
		case "7":
			return m, m.applyLanguageSelection(LangRu)
		case "8":
			return m, m.applyLanguageSelection(LangPt)
		case "9":
			return m, m.applyLanguageSelection(LangVi)
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

	if msg.String() == "esc" && (m.shellMode || m.chatMode) {
		if m.autoCompleteActive {
			m.autoCompleteActive = false
			m.autoCompleteItems = nil
			return m, nil
		}
		if m.chatMode {
			m.setChatMode(false)
			m.input.Reset()
			return m, nil
		}
		if m.shellMode {
			m.setShellMode(false)
			m.input.Reset()
			return m, nil
		}
	}

	if m.loading && (msg.String() == "ctrl+c" || msg.String() == "esc") && !m.subAgentFollow.isActive() {
		m.resetExitConfirm()
		if m.cancelConfirmPending {
			m.cancelActiveRun()
		} else {
			m.promptCancelConfirm()
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+n":

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

	case "$", "!":
		if !m.shellMode && !m.chatMode && !m.projectMemoryLoading && strings.TrimSpace(m.input.Value()) == "" {
			m.setShellMode(true)
			return m, nil
		}
	case "#":
		if !m.chatMode && !m.shellMode && !m.projectMemoryLoading && strings.TrimSpace(m.input.Value()) == "" {
			m.setChatMode(true)
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
	case "ctrl+v", "ctrl+shift+v":

		return m, m.handleClipboardPaste()
	case "ctrl+backspace", "ctrl+h":

		if _, ok := m.popPendingImage(); ok {
			return m, nil
		}

		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, combineCmds(spinnerCmd, cmd)
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

		now := time.Now()
		if !m.exitConfirmPending && !m.cancelConfirmPending &&
			!m.autoCompleteActive &&
			!m.subAgentFollow.isActive() && !m.loading && !m.shellMode && !m.chatMode {
			if !m.lastEscPress.IsZero() && now.Sub(m.lastEscPress) < 500*time.Millisecond {
				m.lastEscPress = time.Time{}
				m.chatWriteSystem(nextSystemID(), m.t("rewind.activated"))
				m.chatListScrollToBottom()
				return m, m.handleUndoCommand()
			}
			m.lastEscPress = now

			if strings.TrimSpace(m.input.Value()) != "" {
				m.input.Reset()
				return m, nil
			}
			return m, nil
		}

		if m.exitConfirmPending {
			m.resetExitConfirm()
			return m, nil
		}

		if m.cancelConfirmPending {
			m.resetCancelConfirm()
			return m, nil
		}
		if m.autoCompleteActive {
			m.autoCompleteActive = false
			m.autoCompleteItems = nil
			return m, nil
		}

		if m.subAgentFollow.isActive() {
			m.subAgentFollow.deactivate()
			return m, nil
		}
	case "enter":
		if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
			return m, m.applyAutoComplete()
		}
		m.resetExitConfirm()
		text := strings.TrimSpace(m.input.Value())
		m.input.Reset()

		m.autoCompleteActive = false
		m.autoCompleteItems = nil
		m.autoCompleteIndex = 0
		m.inputHint = ""
		if text == "" && len(m.pendingImages) == 0 {
			m.chatListScrollToBottom()
			return m, nil
		}
		if m.shellMode {
			m.emitIMLocalUserText("$ " + text)
			return m, m.submitShellCommand(text, true)
		}
		if m.chatMode {

			m.submitChatMessage(text)
			return m, nil
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
	case "shift+enter", "ctrl+j", "alt+enter":

		m.input.InsertRune('\n')
		m.updateAutoComplete()
		return m, nil

	case "ctrl+a":

		m.input.CursorStart()

		m.updateAutoComplete()
		return m, nil
	case "ctrl+e":

		m.input.CursorEnd()

		m.updateAutoComplete()
		return m, nil
	case "ctrl+k":

		deleteFromCursorToEnd(&m.input)
		m.inputHint = ""
		m.updateAutoComplete()
		return m, nil
	case "ctrl+u":

		deleteFromLineStartToCursor(&m.input)
		m.inputHint = ""
		m.updateAutoComplete()
		return m, nil
	case "ctrl+w":

		deleteWordBeforeCursor(&m.input)
		m.inputHint = ""
		m.updateAutoComplete()
		return m, nil
	}

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

func deleteFromCursorToEnd(ta *textarea.Model) {
	line := ta.Line()
	col := ta.Column()
	val := ta.Value()
	lines := strings.Split(val, "\n")
	if line < 0 || line >= len(lines) {
		return
	}
	if col >= len(lines[line]) {
		return
	}
	lines[line] = lines[line][:col]
	setValueAtCursor(ta, strings.Join(lines, "\n"), line, col)
}

func deleteFromLineStartToCursor(ta *textarea.Model) {
	line := ta.Line()
	col := ta.Column()
	val := ta.Value()
	lines := strings.Split(val, "\n")
	if line < 0 || line >= len(lines) {
		return
	}
	if col == 0 {
		return
	}
	if col > len(lines[line]) {
		col = len(lines[line])
	}
	lines[line] = lines[line][col:]
	setValueAtCursor(ta, strings.Join(lines, "\n"), line, 0)
}

func deleteWordBeforeCursor(ta *textarea.Model) {
	val := ta.Value()
	lines := strings.Split(val, "\n")
	line := ta.Line()
	col := ta.Column()
	if line < 0 || line >= len(lines) {
		return
	}
	currentLine := lines[line]
	if col > len(currentLine) {
		col = len(currentLine)
	}
	if col == 0 {
		return
	}
	pos := col

	for pos > 0 && (currentLine[pos-1] == ' ' || currentLine[pos-1] == '\t') {
		pos--
	}

	for pos > 0 && currentLine[pos-1] != ' ' && currentLine[pos-1] != '\t' {
		pos--
	}

	for pos > 0 && (currentLine[pos-1] == ' ' || currentLine[pos-1] == '\t') {
		pos--
	}
	lines[line] = currentLine[:pos] + currentLine[col:]
	setValueAtCursor(ta, strings.Join(lines, "\n"), line, pos)
}

func setValueAtCursor(ta *textarea.Model, value string, row, col int) {
	ta.SetValue(value)

	numLines := strings.Count(value, "\n")
	for i := numLines; i > row; i-- {
		ta.CursorUp()
	}
	ta.SetCursorColumn(col)
}

func (m Model) handleInitPromptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		m.initPromptActive = false
		return m, m.handleInitCommand()
	case "n", "N", "esc":
		m.initPromptActive = false
		return m, nil
	}
	return m, nil
}
