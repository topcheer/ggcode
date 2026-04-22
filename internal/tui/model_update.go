package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// Update handles all Bubble Tea messages and is defined in model_update.go for file-size
// manageability. See model.go for the Model struct definition and other methods.

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle spinner ticks first
	var spinnerCmd tea.Cmd
	if m.spinner.IsActive() {
		spinnerCmd = m.spinner.Update(msg)
	}

	switch msg := msg.(type) {
	case logoMsg:
		m.startupVendor = msg.Vendor
		m.startupEndpoint = msg.Endpoint
		m.startupModel = msg.Model
		m.setActiveRuntimeSelection(msg.Vendor, msg.Endpoint, msg.Model)
		return m, nil

	case tea.WindowSizeMsg:
		m.handleResize(msg.Width, msg.Height)
		// Reset the startup clock when Bubble Tea sends the first WindowSizeMsg.
		// This ensures the startup input gate window is measured from the moment
		// the TUI event loop actually starts, not from model creation time (which
		// can be hundreds of milliseconds earlier due to config loading, IM setup, etc.).
		if m.startedAt.IsZero() || time.Since(m.startedAt) > startupInputGateWindow {
			m.startedAt = time.Now()
		}
		return m, nil

	case imRuntimeUpdatedMsg:
		return m, nil

	case tea.MouseWheelMsg:
		m.lastMouseAt = time.Now()
		if startupInputSuppressionActive(m.startedAt) {
			return m, nil
		}
		// Route mouse wheel to the active panel's viewport if one is open.
		// MouseWheelMsg implements the MouseMsg interface, so it must appear
		// BEFORE case tea.MouseMsg in this type switch to be matched here.
		if m.fileBrowser != nil && m.fileBrowser.preview != nil {
			if msg.Button == tea.MouseWheelUp {
				m.fileBrowser.preview.viewport.ScrollUp(3)
			} else {
				m.fileBrowser.preview.viewport.ScrollDown(3)
			}
			return m, nil
		}
		if m.previewPanel != nil {
			if msg.Button == tea.MouseWheelUp {
				m.previewPanel.viewport.ScrollUp(3)
			} else {
				m.previewPanel.viewport.ScrollDown(3)
			}
			return m, nil
		}
		// Default: scroll the main conversation viewport.
		m.syncConversationViewport()
		if msg.Button == tea.MouseWheelUp {
			m.viewport.ScrollUp(3)
		} else {
			m.viewport.ScrollDown(3)
		}
		return m, nil

	case tea.MouseMsg:
		m.lastMouseAt = time.Now()
		if startupInputSuppressionActive(m.startedAt) {
			debug.Log("tui", "startup gate dropping mouse event age=%s", time.Since(m.startedAt))
			return m, nil
		}
		if m.fileBrowser != nil {
			return m.handleFileBrowserMouse(msg)
		}
		if m.previewPanel != nil {
			return m.handlePreviewMouse(msg)
		}
		// Option/Alt+mouse: release mouse to terminal for native text selection
		return m, nil

	case tea.PasteMsg:
		if !m.inputReady {
			return m, spinnerCmd
		}
		if m.loading {
			return m, spinnerCmd
		}
		// Forward paste to active panel inputs.
		if m.providerPanel != nil {
			if m.providerPanel.editingField != "" {
				var cmd tea.Cmd
				m.providerPanel.editInput, cmd = m.providerPanel.editInput.Update(msg)
				return m, cmd
			}
			if m.providerPanel.modelFilter.Focused() {
				var cmd tea.Cmd
				m.providerPanel.modelFilter, cmd = m.providerPanel.modelFilter.Update(msg)
				syncModelSelection(&m.providerPanel.modelIndex, m.providerPanel.models, m.providerPanel.modelFilter)
				return m, cmd
			}
		}
		if m.modelPanel != nil && m.modelPanel.filter.Focused() {
			var cmd tea.Cmd
			m.modelPanel.filter, cmd = m.modelPanel.filter.Update(msg)
			syncModelSelection(&m.modelPanel.selected, m.modelPanel.models, m.modelPanel.filter)
			return m, cmd
		}
		if m.impersonatePanel != nil {
			if m.impersonatePanel.editingHeader >= 0 {
				var cmd tea.Cmd
				if m.impersonatePanel.headerKeyInput.Focused() {
					m.impersonatePanel.headerKeyInput, cmd = m.impersonatePanel.headerKeyInput.Update(msg)
				} else if m.impersonatePanel.headerValueInput.Focused() {
					m.impersonatePanel.headerValueInput, cmd = m.impersonatePanel.headerValueInput.Update(msg)
				}
				return m, cmd
			}
			if m.impersonatePanel.versionInput.Focused() {
				var cmd tea.Cmd
				m.impersonatePanel.versionInput, cmd = m.impersonatePanel.versionInput.Update(msg)
				return m, cmd
			}
		}
		if m.harnessContextPrompt != nil && m.harnessContextPrompt.inputFocus {
			var cmd tea.Cmd
			m.harnessContextPrompt.input, cmd = m.harnessContextPrompt.input.Update(msg)
			return m, cmd
		}
		if m.harnessPanel != nil && m.harnessPanel.actionInput.Focused() {
			var cmd tea.Cmd
			m.harnessPanel.actionInput, cmd = m.harnessPanel.actionInput.Update(msg)
			return m, cmd
		}
		// Forward paste to IM panel create-input fields (manual string inputs).
		if m.qqPanel != nil && m.qqPanel.createMode {
			m.qqPanel.createInput += msg.Content
			return m, nil
		}
		if m.tgPanel != nil && m.tgPanel.createMode {
			m.tgPanel.createInput += msg.Content
			return m, nil
		}
		if m.discordPanel != nil && m.discordPanel.createMode {
			m.discordPanel.createInput += msg.Content
			return m, nil
		}
		if m.slackPanel != nil && m.slackPanel.createMode {
			m.slackPanel.createInput += msg.Content
			return m, nil
		}
		if m.feishuPanel != nil && m.feishuPanel.createMode {
			m.feishuPanel.createInput += msg.Content
			return m, nil
		}
		if m.dingtalkPanel != nil && m.dingtalkPanel.createMode {
			m.dingtalkPanel.createInput += msg.Content
			return m, nil
		}
		// Forward paste to questionnaire input if active.
		if m.pendingQuestionnaire != nil && m.pendingQuestionnaire.activeQuestionAllowsFreeform() {
			var cmd tea.Cmd
			m.pendingQuestionnaire.input, cmd = m.pendingQuestionnaire.input.Update(msg)
			m.pendingQuestionnaire.saveActiveQuestionInput()
			return m, cmd
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		// During startup input drain, suppress all keyboard input.
		// This prevents terminal responses (OSC 11, CPR, Kitty mode report)
		// from appearing as garbage in the text input field.
		if !m.inputDrainUntil.IsZero() && time.Now().Before(m.inputDrainUntil) {
			debug.Log("tui", "KEYPRESS dropped (input drain) key=%q text=%q", msg.String(), msg.Text)
			return m, nil
		}
		// Mouse SGR fragments (split across read boundaries inside the
		// bubbletea parser) can leak through as standalone single-char
		// KeyPressMsg events whose text consists solely of digits/`;`/`<`/
		// `M`/`m`. Drop these for a short window after any mouse activity.
		if !m.lastMouseAt.IsZero() && time.Since(m.lastMouseAt) < 200*time.Millisecond &&
			msg.Mod == 0 && isMouseFragmentChar(msg.Text) {
			debug.Log("tui", "KEYPRESS dropped (mouse fragment) text=%q since_mouse=%s", msg.Text, time.Since(m.lastMouseAt))
			return m, nil
		}
		if shouldIgnoreTerminalProbeKey(msg) {
			debug.Log("tui", "ignoring terminal probe key=%q text=%q mod=%v", msg.String(), msg.Text, msg.Mod)
			return m, nil
		}
		if m.startupBannerVisible && !shouldIgnoreInputUpdate(msg, m.startedAt, m.lastResizeAt) {
			m.startupBannerVisible = false
		}
		if msg.String() != "ctrl+c" {
			m.resetExitConfirm()
		}
		debug.Log("tui", "KEYPRESS str=%q text=%q mod=%v code=%v input_before=%q", msg.String(), msg.Text, msg.Mod, msg.Code, truncateStr(m.input.Value(), 80))
		if msg.String() == "ctrl+r" {
			m.sidebarVisible = !m.sidebarVisible
			if m.config != nil {
				_ = m.config.SaveSidebarPreference(m.sidebarVisible)
			}
			m.chatEntries.InvalidateAll()
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

		// Handle approval mode (selection list)
		if m.modelPanel != nil {
			return m.handleModelPanelKey(msg)
		}

		if m.providerPanel != nil {
			return m.handleProviderPanelKey(msg)
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

		if m.imPanel != nil {
			return m.handleIMPanelKey(msg)
		}

		if m.mcpPanel != nil {
			return m.handleMCPPanelKey(msg)
		}

		if m.impersonatePanel != nil {
			return m.handleImpersonatePanelKey(msg)
		}

		if m.skillsPanel != nil {
			return m.handleSkillsPanelKey(msg)
		}

		if m.swarmPanel != nil {
			return m.handleSwarmPanelKey(msg)
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

		if m.agentDetailPanel != nil {
			return m.handleAgentDetailPanelKey(msg)
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

		if msg.String() == "esc" && m.previewPanel != nil {
			m.closePreviewPanel()
			return m, nil
		}

		if m.loading && (msg.String() == "ctrl+c" || msg.String() == "esc") {
			m.resetExitConfirm()
			m.cancelActiveRun()
			return m, nil
		}

		switch msg.String() {
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
				return m, tea.Quit
			}
			m.promptExitConfirm()
			return m, nil
		case "ctrl+v":
			if !m.loading {
				return m, m.handleClipboardPaste()
			}
			return m, nil
		case "ctrl+d":
			m.quitting = true
			return m, tea.Quit
		case "shift+tab":
			if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
				m.autoCompleteIndex = (m.autoCompleteIndex - 1 + len(m.autoCompleteItems)) % len(m.autoCompleteItems)
				return m, nil
			}
			return m.handleModeSwitch()
		case "pgup":
			m.syncConversationViewport()
			m.viewport.ScrollUp(m.viewport.VisibleLineCount() / 2)
			return m, nil
		case "pgdown":
			m.syncConversationViewport()
			m.viewport.ScrollDown(m.viewport.VisibleLineCount() / 2)
			return m, nil
		case "up":
			if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
				m.autoCompleteIndex = (m.autoCompleteIndex - 1 + len(m.autoCompleteItems)) % len(m.autoCompleteItems)
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			return m.handleHistoryUp()
		case "down":
			if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
				m.autoCompleteIndex = (m.autoCompleteIndex + 1) % len(m.autoCompleteItems)
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			return m.handleHistoryDown()
		case "tab":
			if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
				if len(m.autoCompleteItems) == 1 {
					return m, m.applyAutoComplete()
				}
				m.autoCompleteIndex = (m.autoCompleteIndex + 1) % len(m.autoCompleteItems)
				return m, nil
			}
		case "esc":
			if m.autoCompleteActive {
				m.autoCompleteActive = false
				m.autoCompleteItems = nil
				return m, nil
			}
			if m.shellMode && !m.loading {
				m.setShellMode(false)
				m.input.SetValue("")
				return m, nil
			}
		case "enter":
			if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
				return m, m.applyAutoComplete()
			}
			m.resetExitConfirm()
			text := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			if text == "" {
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
		}

	case streamMsg:
		if m.runCanceled {
			return m, nil
		}
		m.appendStreamChunk(string(msg))
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	case remoteInboundMsg:
		// Track the originating adapter for per-channel echo suppression.
		m.remoteInboundAdapter = msg.Message.Envelope.Adapter
		prompt := buildRemoteInboundPrompt(msg.Message)
		if m.pendingQuestionnaire != nil {
			if strings.TrimSpace(prompt) == "" {
				if msg.Response != nil {
					msg.Response <- fmt.Errorf("empty remote message")
				}
				return m, nil
			}
			completed, err := m.pendingQuestionnaire.applyRemoteAnswer(prompt, m.currentLanguage())
			if msg.Response != nil {
				msg.Response <- nil
			}
			if err != nil {
				switch m.currentLanguage() {
				case LangZhCN:
					m.emitIMText("没有识别出有效的问卷答案，请直接回复选项编号或文本答案。")
				default:
					m.emitIMText("I couldn't parse that questionnaire answer. Reply with choice numbers or plain text.")
				}
				return m, nil
			}
			if completed {
				return m, m.handleQuestionnaireResult(toolpkg.AskUserStatusSubmitted)
			}
			if nextIdx := m.pendingQuestionnaire.firstUnansweredQuestionIndex(); nextIdx >= 0 {
				m.emitIMAskUser(m.formatIMAskUserQuestion(m.pendingQuestionnaire.request.Title, m.pendingQuestionnaire.request.Questions[nextIdx]))
			}
			return m, nil
		}
		if response, handled := m.ExecuteRemoteSlashCommand(prompt); handled {
			if strings.TrimSpace(response) != "" {
				m.emitIMText(response)
			}
			if msg.Response != nil {
				msg.Response <- nil
			}
			return m, nil
		}
		if strings.TrimSpace(prompt) == "" {
			if msg.Response != nil {
				msg.Response <- fmt.Errorf("empty remote message")
			}
			return m, nil
		}
		if msg.Response != nil {
			msg.Response <- nil
		}
		// Echo user message to all channels EXCEPT the originating adapter,
		// so other IM users can see what was asked.
		m.emitIMLocalUserTextExcept(prompt, m.remoteInboundAdapter)
		if m.loading {
			m.queuePendingSubmission(prompt)
			return m, nil
		}
		return m, m.submitText(prompt, false)

	case displaySleepMsg:
		// stdout is dead (display sleep / terminal closed).
		// The stdoutDeadFlag is already set by the health monitor.
		// No action needed here — the renderer checks IsStdoutDead().
		return m, nil

	case displayWakeMsg:
		// stdout recovered — force a full redraw to refresh stale content.
		return m, nil

	case agentStreamMsg:
		if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
			return m, nil
		}
		m.appendStreamChunk(msg.Text)
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	case agentInterruptMsg:
		if msg.RunID != m.activeAgentRunID {
			return m, nil
		}
		m.ensureOutputEndsWithNewline()
		m.dualWriteSystem(m.renderConversationUserEntry("❯ ", msg.Text))
		m.dualWriteSystem("\n")
		m.dualWriteSystem(m.styles.prompt.Render(m.t("interrupt.delivered")))
		m.dualWriteSystem("\n")
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		return m, nil

	case shellCommandStreamMsg:
		if msg.RunID != m.activeShellRunID || m.runCanceled || !m.loading {
			return m, nil
		}
		m.appendShellChunk(msg.Text)
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	case doneMsg:
		finalIMText := m.pendingIMStreamText()
		m.loading = false
		m.remoteInboundAdapter = "" // reset per-channel suppression after agent run
		m.spinner.Stop()
		m.closeToolActivityGroup()
		m.flushGroupedActivitiesToOutput()
		m.cancelFunc = nil
		m.streamPrefixWritten = false
		wasCanceled := m.runCanceled
		wasFailed := m.runFailed
		m.runCanceled = false
		m.runFailed = false
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
			m.renderStreamBuffer(true)
			m.streamBuffer = nil
		}
		if finalIMText != "" {
			m.emitIMText(finalIMText)
		}
		m.dualWriteSystem("\n")
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		if !wasCanceled && !wasFailed && m.pendingSubmissionCount() > 0 {
			return m, m.submitText(m.consumePendingSubmission(), false)
		}
		return m, nil

	case agentDoneMsg:
		if msg.RunID != m.activeAgentRunID {
			return m, nil
		}
		if m.agent != nil {
			m.projMemFiles = m.agent.ProjectMemoryFiles()
		}
		m.loading = false
		m.remoteInboundAdapter = "" // reset per-channel suppression
		m.spinner.Stop()
		m.closeToolActivityGroup()
		m.flushGroupedActivitiesToOutput()
		m.cancelFunc = nil
		// Finalize streaming assistant entry in chatEntries
		if last := m.chatEntries.LastMatching("assistant"); last != nil && last.Streaming {
			last.Streaming = false
		}
		wasCanceled := m.runCanceled
		wasFailed := m.runFailed
		m.runCanceled = false
		m.runFailed = false
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
			m.renderStreamBuffer(true)
			m.streamBuffer = nil
		}
		m.dualWriteSystem("\n")
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		if !wasCanceled && !wasFailed && m.pendingSubmissionCount() > 0 {
			return m, m.submitText(m.consumePendingSubmission(), false)
		}
		return m, nil

	case shellCommandDoneMsg:
		if msg.RunID != m.activeShellRunID {
			return m, nil
		}
		hadShellOutput := m.shellBuffer != nil && m.shellBuffer.Len() > 0
		m.loading = false
		m.spinner.Stop()
		m.cancelFunc = nil
		wasCanceled := m.runCanceled
		wasFailed := m.runFailed
		m.runCanceled = false
		m.runFailed = false
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		if msg.Status == toolpkg.CommandJobFailed || msg.Status == toolpkg.CommandJobTimedOut {
			m.runFailed = true
			if m.pendingSubmissionCount() > 0 {
				m.restorePendingInput()
			}
			if text := strings.TrimSpace(msg.ErrText); text != "" {
				m.ensureOutputEndsWithNewline()
				m.dualWriteSystem(m.styles.error.Render(text))
				m.dualWriteSystem("\n")
			}
		}
		if !wasCanceled && (hadShellOutput || strings.TrimSpace(msg.ErrText) != "") {
			m.ensureOutputHasBlankLine()
		}
		if msg.Status == toolpkg.CommandJobCompleted && m.pendingSubmissionCount() > 0 && !wasCanceled && !wasFailed {
			return m, m.submitShellCommand(m.consumePendingSubmission(), false)
		}
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		return m, nil

	case errMsg:
		if errors.Is(msg.err, context.Canceled) {
			return m, nil
		}
		m.runFailed = true
		m.loading = false
		m.spinner.Stop()
		m.closeToolActivityGroup()
		m.flushGroupedActivitiesToOutput()
		m.cancelFunc = nil
		if m.pendingSubmissionCount() > 0 {
			m.restorePendingInput()
		}
		m.dualWriteSystem(m.styles.error.Render(formatUserFacingError(m.currentLanguage(), msg.err) + "\n\n"))
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		return m, nil

	case agentErrMsg:
		if msg.RunID != m.activeAgentRunID {
			return m, nil
		}
		if errors.Is(msg.Err, context.Canceled) {
			return m, nil
		}
		m.runFailed = true
		m.loading = false
		m.spinner.Stop()
		m.closeToolActivityGroup()
		m.flushGroupedActivitiesToOutput()
		m.cancelFunc = nil
		if m.pendingSubmissionCount() > 0 {
			m.restorePendingInput()
		}
		m.dualWriteSystem(m.styles.error.Render(formatUserFacingError(m.currentLanguage(), msg.Err) + "\n\n"))
		m.emitIMText(formatUserFacingError(m.currentLanguage(), msg.Err))
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		return m, nil

	case harnessRunResultMsg:
		if path := strings.TrimSpace(m.harnessRunLogPath); path != "" {
			chunk, nextOffset := readHarnessRunLogChunk(path, m.harnessRunLogOffset)
			m.harnessRunLogOffset = nextOffset
			m.appendHarnessLogChunk(chunk)
		} else if msg.Summary != nil && msg.Summary.Task != nil {
			path := strings.TrimSpace(msg.Summary.Task.LogPath)
			chunk, nextOffset := readHarnessRunLogChunk(path, m.harnessRunLogOffset)
			m.harnessRunLogPath = path
			m.harnessRunLogOffset = nextOffset
			m.appendHarnessLogChunk(chunk)
		}
		m.flushHarnessLogRemainder()
		m.loading = false
		m.spinner.Stop()
		m.closeToolActivityGroup()
		m.flushGroupedActivitiesToOutput()
		m.cancelFunc = nil
		wasCanceled := m.runCanceled
		wasFailed := m.runFailed
		m.runCanceled = false
		m.runFailed = false
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		m.harnessRunProject = nil
		m.harnessRunGoal = ""
		m.harnessRunTaskID = ""
		m.harnessRunLastDetail = ""
		m.harnessRunRemainder = ""
		m.harnessRunLiveTail = ""
		streamedHarnessOutput := m.harnessRunLogOffset > 0 || strings.TrimSpace(m.harnessRunLastDetail) != ""
		m.harnessRunLogPath = ""
		m.harnessRunLogOffset = 0
		if errors.Is(msg.Err, context.Canceled) {
			return m, nil
		}
		if msg.Err != nil {
			m.runFailed = true
			if m.pendingSubmissionCount() > 0 {
				m.restorePendingInput()
			}
			m.dualWriteSystem(m.styles.error.Render(msg.Err.Error()))
			m.dualWriteSystem("\n")
			m.syncConversationViewport()
			m.viewport.GotoBottom()
			return m, nil
		}
		rendered := harness.FormatRunSummary(msg.Summary)
		if streamedHarnessOutput {
			rendered = trimHarnessRunOutputSection(rendered)
		}
		m.renderStreamBuffer(true)
		m.ensureOutputHasBlankLine()
		if msg.Summary != nil && msg.Summary.Task != nil && msg.Summary.Task.Status == harness.TaskFailed {
			m.dualWriteSystem(m.styles.error.Render(rendered))
		} else {
			m.dualWriteSystem(m.styles.assistant.Render(rendered))
		}
		m.dualWriteSystem("\n")
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		if !wasCanceled && !wasFailed && m.pendingSubmissionCount() > 0 {
			return m, m.submitText(m.consumePendingSubmission(), false)
		}
		return m, nil

	case knightTaskResultMsg:
		m.loading = false
		m.spinner.Stop()
		m.cancelFunc = nil
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		if msg.Err != nil {
			m.ensureOutputEndsWithNewline()
			m.dualWriteSystem(m.styles.error.Render(fmt.Sprintf("Knight task failed: %v", msg.Err)))
			m.dualWriteSystem("\n")
			m.syncConversationViewport()
			m.viewport.GotoBottom()
			return m, nil
		}
		m.ensureOutputEndsWithNewline()
		m.dualWriteSystem(fmt.Sprintf("🌙 Knight task completed: %s\n", msg.Goal))
		m.dualWriteSystem(strings.TrimSpace(msg.Result.Output))
		m.dualWriteSystem("\n")
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		return m, nil

	case harnessContextSuggestionsMsg:
		state := m.harnessContextPrompt
		if state == nil || state.mode != harnessContextPromptInit {
			return m, nil
		}
		state.step = harnessContextPromptStepSelect
		state.suggestions = harness.NormalizeContexts(msg.Contexts)
		state.selected = map[int]bool{}
		state.cursor = 0
		state.input.Placeholder = "Optional custom contexts: payments, checkout=apps/checkout"
		state.input.SetValue("")
		state.inputFocus = len(state.suggestions) == 0
		if state.inputFocus {
			state.input.Focus()
		} else {
			state.input.Blur()
		}
		if msg.Err != nil {
			state.message = msg.Err.Error()
		} else if len(state.suggestions) == 0 {
			state.message = "No suggestions found. Add custom contexts below."
		} else {
			state.message = ""
		}
		return m, nil

	case harnessInitResultMsg:
		state := m.harnessContextPrompt
		if msg.Err != nil {
			if state != nil {
				state.message = msg.Err.Error()
				if state.existingProject {
					state.step = harnessContextPromptStepUpgrade
				} else {
					state.step = harnessContextPromptStepSelect
				}
			}
			return m, nil
		}
		m.closeHarnessContextPrompt("")
		m.refreshHarnessPanel()
		if msg.Result != nil {
			commandText := "/harness init"
			if state != nil && strings.TrimSpace(state.commandText) != "" {
				commandText = strings.TrimSpace(state.commandText)
			}
			m.dualWriteSystem(m.renderConversationUserEntry("❯ ", commandText))
			m.dualWriteSystem("\n")
			m.appendUserMessage(commandText)
			m.dualWriteSystem(m.styles.assistant.Render(formatHarnessInitResult(msg.Result)))
			m.dualWriteSystem("\n")
			m.syncConversationViewport()
			m.viewport.GotoBottom()
			if panel := m.harnessPanel; panel != nil {
				panel.message = fmt.Sprintf("Initialized harness in %s", msg.Result.Project.RootDir)
			}
		}
		return m, nil

	case harnessRunProgressMsg:
		if !m.loading || m.harnessRunProject == nil {
			return m, nil
		}
		if msg.TaskID != "" {
			m.harnessRunTaskID = msg.TaskID
		}
		if msg.LogPath != "" {
			m.harnessRunLogPath = msg.LogPath
		}
		if msg.LogChunk != "" {
			m.appendHarnessLogChunk(msg.LogChunk)
		}
		if msg.LogOffset > 0 {
			m.harnessRunLogOffset = msg.LogOffset
		}
		if detail := strings.TrimSpace(msg.Detail); detail != "" && detail != m.harnessRunLastDetail {
			m.harnessRunLastDetail = detail
			if !harnessLogChunkContainsDetail(m.currentLanguage(), m.harnessRunProject, msg.LogChunk, detail) {
				m.appendHarnessProgressDetail(detail)
			}
		}
		if strings.TrimSpace(msg.Activity) != "" {
			m.statusActivity = msg.Activity
		}
		return m, m.pollHarnessRunProgress()

	case harnessPanelAutoRefreshMsg:
		if !m.shouldAutoRefreshHarnessTask() {
			return m, nil
		}
		m.refreshHarnessPanel()
		if !m.shouldAutoRefreshHarnessTask() {
			return m, nil
		}
		return m, m.pollHarnessPanelAutoRefresh()

	case startupReadyMsg:
		m.startupBannerVisible = false
		return m, nil

	case projectMemoryLoadedMsg:
		m.projectMemoryLoading = false
		if msg.Err != nil {
			debug.Log("tui", "project memory load failed: %v", msg.Err)
			if m.pendingSubmissionCount() > 0 && !m.loading {
				return m, m.submitText(m.consumePendingSubmission(), false)
			}
			return m, nil
		}
		m.projMemFiles = append([]string(nil), msg.Files...)
		if m.agent != nil && strings.TrimSpace(msg.Content) != "" {
			m.agent.SetProjectMemoryFiles(msg.Files)
			m.agent.AddMessage(provider.Message{
				Role:    "system",
				Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + msg.Content}},
			})
		}
		if m.pendingSubmissionCount() > 0 && !m.loading {
			if m.shellMode {
				return m, m.submitShellCommand(m.consumePendingSubmission(), false)
			}
			return m, m.submitText(m.consumePendingSubmission(), false)
		}
		return m, nil

	case ApprovalMsg:
		if m.mode == permission.AutopilotMode {
			m.pendingApproval = &msg
			return m, m.handleApproval(permission.Allow)
		}
		// Agent is requesting approval
		m.pendingApproval = &msg
		if msg.ToolName == "exit_plan_mode" {
			m.approvalOptions = planApprovalOptions()
		} else {
			m.approvalOptions = defaultApprovalOptions()
		}
		m.approvalCursor = 0
		return m, nil

	case DiffConfirmMsg:
		if m.mode == permission.AutopilotMode {
			m.pendingDiffConfirm = &msg
			return m, m.handleDiffConfirm(true)
		}
		m.pendingDiffConfirm = &msg
		m.diffOptions = diffConfirmOptions()
		m.diffCursor = 0
		return m, nil

	case AskUserMsg:
		if m.pendingQuestionnaire != nil {
			if msg.Response != nil {
				safego.Go("tui.model.cancelAskUser", func() {
					msg.Response <- toolpkg.AskUserResponse{
						Status:        toolpkg.AskUserStatusCancelled,
						Title:         msg.Request.Title,
						QuestionCount: len(msg.Request.Questions),
					}
				})
			}
			return m, nil
		}
		m.pendingQuestionnaire = newQuestionnaireState(msg.Request, msg.Response, m.currentLanguage())
		m.syncQuestionnaireInputWidth()
		// Push the first question to IM so remote users can answer.
		if len(msg.Request.Questions) > 0 {
			m.emitIMAskUser(m.formatIMAskUserQuestion(msg.Request.Title, msg.Request.Questions[0]))
		}
		return m, nil

	case HarnessCheckpointConfirmMsg:
		if m.mode == permission.AutopilotMode {
			m.pendingHarnessCheckpointConfirm = &msg
			return m, m.handleHarnessCheckpointConfirm(true)
		}
		m.pendingHarnessCheckpointConfirm = &msg
		m.diffOptions = diffConfirmOptions()
		m.diffCursor = 0
		return m, nil

	case subAgentUpdateMsg:
		m.syncConversationViewport()
		if m.viewport.AutoFollow() {
			m.viewport.GotoBottom()
		}
		return m, nil

	case modeChangeMsg:
		m.mode = msg.Mode
		return m, nil

	case cronPromptMsg:
		m.queuePendingSubmission(msg.Prompt)
		return m, nil

	case skillsChangedMsg:
		m.refreshCommands()
		return m, nil

	case updateCheckResultMsg:
		m.applyUpdateCheckResult(msg)
		return m, nil

	case updateCheckTickMsg:
		return m, tea.Batch(m.checkForUpdateCmd(), m.scheduleUpdateCheckCmd())

	case updatePrepareResultMsg:
		return m.handlePreparedUpdate(msg)

	case toolStatusMsg:
		if m.runCanceled || !m.loading {
			return m, nil
		}
		ts := ToolStatusMsg(msg)
		m.updateActiveMCPTools(ts)
		if ts.Running {
			if !isSubAgentLifecycleTool(ts.ToolName) {
				m.statusToolCount++
			}
			m.startToolActivity(ts)
			if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
				m.renderStreamBuffer(true)
				m.streamStartPos = m.output.Len()
			}
			startCmd := m.spinner.Start(firstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
			spinnerCmd = combineCmds(spinnerCmd, startCmd)
		} else {
			m.finishToolActivity(ts)
			ts.Elapsed = m.spinner.Elapsed()
			m.spinner.Stop()
			spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
			// Reset stream prefix so next text block gets ●
			m.streamPrefixWritten = false
			// Reset stream buffer position for next text chunk
			m.streamStartPos = m.output.Len()
		}
		m.syncConversationViewport()
		if m.viewport.AutoFollow() {
			m.viewport.GotoBottom()
		}
		return m, spinnerCmd

	case agentToolBatchMsg:
		// Batched tool events — process all accumulated status + tool updates
		// in a single Update cycle instead of one message per event.
		// This prevents event-loop saturation from burst tool call/results.
		if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
			return m, nil
		}
		// Apply the last status message (only the latest matters for the status bar).
		if len(msg.StatusMsgs) > 0 {
			last := msg.StatusMsgs[len(msg.StatusMsgs)-1]
			m.statusActivity = last.Activity
			m.statusToolName = last.ToolName
			m.statusToolArg = last.ToolArg
			spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
		}
		// Apply all tool status updates sequentially.
		for _, ts := range msg.ToolMsgs {
			m.updateActiveMCPTools(ts.ToolStatusMsg)
			if ts.Running {
				if !isSubAgentLifecycleTool(ts.ToolName) {
					m.statusToolCount++
				}
				m.startToolActivity(ts.ToolStatusMsg)
				if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
					m.renderStreamBuffer(true)
					m.streamStartPos = m.output.Len()
				}
				startCmd := m.spinner.Start(firstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts.ToolStatusMsg), toolDetail(ts.ToolStatusMsg))))
				spinnerCmd = combineCmds(spinnerCmd, startCmd)
			} else {
				m.finishToolActivity(ts.ToolStatusMsg)
				ts.ToolStatusMsg.Elapsed = m.spinner.Elapsed()
				m.spinner.Stop()
				spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
				m.streamPrefixWritten = false
				m.streamStartPos = m.output.Len()
			}
		}
		m.syncConversationViewport()
		if m.viewport.AutoFollow() {
			m.viewport.GotoBottom()
		}
		return m, spinnerCmd

	case agentToolStatusMsg:
		if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
			return m, nil
		}
		ts := msg.ToolStatusMsg
		m.updateActiveMCPTools(ts)
		if ts.Running {
			if !isSubAgentLifecycleTool(ts.ToolName) {
				m.statusToolCount++
			}
			m.startToolActivity(ts)
			if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
				m.renderStreamBuffer(true)
				m.streamStartPos = m.output.Len()
			}
			startCmd := m.spinner.Start(firstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
			spinnerCmd = combineCmds(spinnerCmd, startCmd)
		} else {
			m.finishToolActivity(ts)
			ts.Elapsed = m.spinner.Elapsed()
			m.spinner.Stop()
			spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
			m.streamPrefixWritten = false
			m.streamStartPos = m.output.Len()
		}
		m.syncConversationViewport()
		if m.viewport.AutoFollow() {
			m.viewport.GotoBottom()
		}
		return m, spinnerCmd

	case mcpServersMsg:
		m.mcpServers = toMCPInfos(msg.Servers)
		m.refreshCommands()
		if m.mcpManager != nil {
			if pending := m.mcpManager.PendingOAuth(); pending != nil {
				m.mcpManager.ClearPendingOAuth()
				return m, m.startMCPOAuth(pending)
			}
		}
		return m, nil

	case mcpInstallResultMsg:
		if m.mcpPanel != nil {
			if msg.err != nil {
				m.mcpPanel.message = fmt.Sprintf("Install failed: %v", msg.err)
			} else if msg.replaced {
				m.mcpPanel.message = fmt.Sprintf("Updated MCP server %s.", msg.name)
			} else {
				m.mcpPanel.message = fmt.Sprintf("Installed MCP server %s.", msg.name)
			}
		}
		return m, nil

	case qqBindResultMsg:
		if m.qqPanel != nil {
			if msg.err != nil {
				m.qqPanel.shareAdapter = ""
				m.qqPanel.shareLink = ""
				m.qqPanel.shareQRCode = ""
				m.qqPanel.message = msg.err.Error()
			} else {
				m.qqPanel.shareAdapter = msg.shareAdapter
				m.qqPanel.shareLink = msg.shareLink
				m.qqPanel.shareQRCode = msg.shareQRCode
				m.qqPanel.message = msg.message
			}
		}
		return m, nil

	case imPanelResultMsg:
		if m.imPanel != nil {
			if msg.err != nil {
				m.imPanel.message = msg.err.Error()
			} else {
				m.imPanel.message = msg.message
			}
		}
		return m, nil

	case feishuBindResultMsg:
		if m.feishuPanel != nil {
			if msg.err != nil {
				m.feishuPanel.message = msg.err.Error()
			} else {
				m.feishuPanel.message = msg.message
			}
		}
		return m, nil

	case slackBindResultMsg:
		if m.slackPanel != nil {
			if msg.err != nil {
				m.slackPanel.message = msg.err.Error()
			} else {
				m.slackPanel.message = msg.message
			}
		}
		return m, nil

	case discordBindResultMsg:
		if m.discordPanel != nil {
			if msg.err != nil {
				m.discordPanel.message = msg.err.Error()
			} else {
				m.discordPanel.message = msg.message
			}
		}
		return m, nil

	case dingtalkBindResultMsg:
		if m.dingtalkPanel != nil {
			if msg.err != nil {
				m.dingtalkPanel.message = msg.err.Error()
			} else {
				m.dingtalkPanel.message = msg.message
			}
		}
		return m, nil

	case tgBindResultMsg:
		if m.tgPanel != nil {
			if msg.err != nil {
				m.tgPanel.message = msg.err.Error()
			} else {
				m.tgPanel.message = msg.message
			}
		}
		return m, nil
	case providerModelsRefreshResultMsg:
		if m.providerPanel != nil && m.providerPanel.refreshVendor == msg.vendor {
			m.providerPanel.refreshing = false
			m.providerPanel.refreshVendor = ""
			currentEndpoint := m.providerPanel.selectedEndpoint()
			currentModel := m.providerPanel.selectedModel()
			m.providerPanel.selectEndpoint(currentEndpoint, currentModel, m.configView())
			switch {
			case msg.saveErr != nil:
				m.providerPanel.message = m.t("panel.provider.refresh.save_failed", msg.saveErr.Error())
			case msg.updated > 0 && msg.discoverErr != nil:
				m.providerPanel.message = m.t("panel.provider.refresh.partial", msg.updated, msg.discovered, msg.discoverErr)
			case msg.updated > 0:
				m.providerPanel.message = m.t("panel.provider.refresh.success", msg.updated, msg.discovered)
			case msg.discoverErr != nil:
				m.providerPanel.message = m.t("panel.provider.refresh.failed", msg.discoverErr.Error())
			default:
				m.providerPanel.message = m.t("panel.provider.refresh.none")
			}
		}
		return m, nil

	case providerAuthStartMsg:
		if m.providerPanel != nil && msg.vendor == auth.ProviderAnthropic {
			if msg.err != nil {
				m.providerPanel.authBusy = false
				m.providerPanel.message = m.t("panel.provider.login.claude_failed", msg.err.Error())
				return m, nil
			}
			if msg.claudeFlow != nil {
				notes := []string{m.t("panel.provider.login.claude_instructions")}
				switch {
				case msg.openErr == nil:
					notes = append(notes, m.t("panel.provider.login.browser_opened"))
				default:
					notes = append(notes, m.t("panel.provider.login.browser_failed", msg.openErr.Error()))
					notes = append(notes, m.t("panel.provider.login.claude_manual", msg.claudeFlow.ManualURL))
				}
				m.providerPanel.message = strings.Join(notes, "\n")
				return m, m.waitForClaudeAuthCode(msg.claudeFlow)
			}
		}
		if m.providerPanel != nil && msg.vendor == auth.ProviderGitHubCopilot {
			if msg.err != nil {
				m.providerPanel.authBusy = false
				m.providerPanel.message = msg.err.Error()
				return m, nil
			}
			if msg.flow != nil {
				m.providerPanel.enterpriseURL = msg.flow.EnterpriseURL
				notes := []string{m.t("panel.provider.login.instructions", msg.flow.VerificationURI, msg.flow.UserCode)}
				switch {
				case msg.copyErr == nil:
					notes = append(notes, m.t("panel.provider.login.copied"))
				default:
					notes = append(notes, m.t("panel.provider.login.copy_failed", msg.copyErr.Error()))
				}
				switch {
				case msg.openErr == nil:
					notes = append(notes, m.t("panel.provider.login.browser_opened"))
				default:
					notes = append(notes, m.t("panel.provider.login.browser_failed", msg.openErr.Error()))
				}
				m.providerPanel.message = strings.Join(notes, "\n")
				return m, m.pollCopilotLogin(msg.flow)
			}
		}
		return m, nil

	case providerAuthResultMsg:
		if m.providerPanel != nil && msg.vendor == auth.ProviderAnthropic {
			m.providerPanel.authBusy = false
			if msg.err != nil {
				m.providerPanel.message = m.t("panel.provider.login.claude_failed", msg.err.Error())
				return m, nil
			}
			m.providerPanel.message = m.t("panel.provider.login.claude_success")
			return m, m.refreshProviderModelsForVendor(auth.ProviderAnthropic)
		}
		if m.providerPanel != nil && msg.vendor == auth.ProviderGitHubCopilot {
			m.providerPanel.authBusy = false
			if msg.err != nil {
				m.providerPanel.message = m.t("panel.provider.login.failed", msg.err.Error())
				return m, nil
			}
			if msg.info != nil {
				m.providerPanel.enterpriseURL = msg.info.EnterpriseURL
			}
			m.providerPanel.message = m.t("panel.provider.login.success")
			return m, m.refreshProviderModelsForVendor(auth.ProviderGitHubCopilot)
		}
		return m, nil

	case modelPanelRefreshResultMsg:
		if m.modelPanel != nil {
			m.modelPanel.refreshing = false
			m.modelPanel.remote = msg.remote
			m.modelPanel.models = uniqueStrings(msg.models)
			if len(m.modelPanel.models) == 0 && m.config != nil && strings.TrimSpace(m.config.Model) != "" {
				m.modelPanel.models = []string{m.config.Model}
			}
			if current := m.config.Model; strings.TrimSpace(current) != "" {
				m.modelPanel.selected = indexOf(m.modelPanel.models, current)
			}
			if m.modelPanel.selected < 0 {
				m.modelPanel.selected = 0
			}
			switch {
			case msg.saveErr != nil:
				m.modelPanel.message = m.t("panel.model.refresh.save_failed", msg.saveErr.Error())
			case msg.discoverErr != nil:
				m.modelPanel.message = m.t("panel.model.refresh.builtin_reason", msg.discoverErr.Error())
			case msg.remote:
				m.modelPanel.message = m.t("panel.model.refresh.remote_loaded", len(m.modelPanel.models))
			default:
				m.modelPanel.message = m.t("panel.model.refresh.builtin_loaded")
			}
		}
		return m, nil

	case mcpUninstallResultMsg:
		if m.mcpPanel != nil {
			if msg.err != nil {
				m.mcpPanel.message = fmt.Sprintf("Uninstall failed: %v", msg.err)
			} else {
				m.mcpPanel.message = fmt.Sprintf("Uninstalled MCP server %s.", msg.name)
				if m.mcpPanel.selected >= len(m.mcpServers) && len(m.mcpServers) > 0 {
					m.mcpPanel.selected = len(m.mcpServers) - 1
				}
			}
		}
		return m, nil

	case mcpOAuthStartMsg:
		if msg.err != nil {
			if m.mcpPanel != nil {
				m.mcpPanel.message = fmt.Sprintf("MCP OAuth failed for %s: %v", msg.serverName, msg.err)
			}
			return m, nil
		}
		if msg.deviceUserCode != "" {
			// Device flow: store code info for banner display, poll in background
			m.addDeviceCode(msg.serverName, msg.deviceUserCode, msg.authorizeURL)
			if m.mcpPanel != nil {
				m.mcpPanel.message = fmt.Sprintf("Waiting for %s device authorization...", msg.serverName)
			}
			return m, m.waitForMCPOAuthDevice(msg.handler)
		}
		// Browser flow
		// Auto-open MCP panel so user can see the auth instructions
		if m.mcpPanel == nil {
			m.openMCPPanel()
		}
		notes := []string{fmt.Sprintf("Opening browser for MCP server %s authentication...", msg.serverName)}
		if msg.openErr != nil {
			notes = append(notes, fmt.Sprintf("Browser failed: %v", msg.openErr))
			notes = append(notes, fmt.Sprintf("Visit: %s", msg.authorizeURL))
		}
		m.mcpPanel.message = strings.Join(notes, "\n")
		return m, m.waitForMCPOAuthCallback(msg.handler)

	case mcpOAuthResultMsg:
		if msg.err != nil {
			m.removeDeviceCode(msg.serverName)
			if m.mcpPanel != nil {
				m.mcpPanel.message = fmt.Sprintf("MCP OAuth failed for %s: %v", msg.serverName, msg.err)
			}
			return m, nil
		}
		m.removeDeviceCode(msg.serverName)
		if m.mcpPanel != nil {
			m.mcpPanel.message = fmt.Sprintf("MCP server %s authenticated successfully", msg.serverName)
		}
		if m.mcpManager != nil {
			m.mcpManager.Retry(msg.serverName)
		}
		return m, nil

	case setProgramMsg:
		debug.Log("tui", "setProgramMsg received, program was nil=%v", m.program == nil)
		m.program = msg.Program
		// Set startedAt for startup gate if not already set.
		if m.startedAt.IsZero() {
			m.startedAt = time.Now()
		}
		// Clear any terminal response garbage that leaked into the input
		// field before we had a chance to set up the drain guard.
		// Only clear when the content looks like terminal response fragments
		// (contains ;, :, /, digits etc.) to avoid wiping legitimate input
		// set programmatically by callers (e.g. IM tests).
		if val := m.input.Value(); val != "" && looksLikeStartupGarbage(val) {
			debug.Log("tui", "clearing pre-drain input garbage: %q", truncateStr(val, 80))
			m.input.SetValue("")
		}
		// Start the input drain window. Terminal responses (OSC 11 color
		// query, CPR, Kitty mode report, mouse-mode/altscreen ACKs) arrive
		// as individual KeyPressMsg events that are indistinguishable from
		// real typing. We suppress all keyboard input until inputDrainEndMsg
		// arrives. The window is intentionally generous (~250ms) because
		// some terminals (and especially when re-running the binary right
		// after a build, with leftover sequences from the previous process
		// in the input buffer) take longer than 50ms to settle.
		m.inputDrainUntil = time.Now().Add(250 * time.Millisecond)
		return m, tea.Tick(250*time.Millisecond, func(_ time.Time) tea.Msg {
			return inputDrainEndMsg{}
		})

	case inputDrainEndMsg:
		m.inputDrainUntil = time.Time{} // zero = drain ended
		m.inputReady = true
		debug.Log("tui", "input drain ended, input ready")
		return m, nil

	case imageAttachedMsg:
		m.setComposerImagePlaceholder(msg)
		m.pendingImage = &msg
		return m, nil

	case statusMsg:
		if m.runCanceled || !m.loading {
			return m, nil
		}
		m.statusActivity = msg.Activity
		m.statusToolName = msg.ToolName
		m.statusToolArg = msg.ToolArg
		if msg.ToolCount > 0 {
			m.statusToolCount = msg.ToolCount
		}
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	case agentStatusMsg:
		if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
			return m, nil
		}
		m.statusActivity = msg.Activity
		m.statusToolName = msg.ToolName
		m.statusToolArg = msg.ToolArg
		if msg.ToolCount > 0 {
			m.statusToolCount = msg.ToolCount
		}
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	case agentRoundProgressMsg:
		return m, nil

	case agentRoundSummaryMsg:
		if msg.RunID != m.activeAgentRunID {
			return m, nil
		}
		m.emitIMRoundSummary(msg.Text, msg.ToolCalls, msg.ToolSuccesses, msg.ToolFailures)
		return m, nil

	case agentAskUserMsg:
		// Don't emit IM here — AskUserMsg handler will emit the first question
		// after creating the questionnaire state.
		return m, nil

	}

	// Skip spinnerMsg — it fires every tick and would flood the log.
	if _, isSpinner := msg.(spinnerMsg); !isSpinner {
		debug.Log("tui", "CATCHALL msg=%T value=%q", msg, fmt.Sprintf("%+v", msg))
	}
	keyMsg, isKeyPress := msg.(tea.KeyPressMsg)
	if !isKeyPress {
		// Non-keyboard messages still need to reach the textinput so its
		// virtual cursor can process blink scheduling messages
		// (cursor.initialBlinkMsg / cursor.BlinkMsg). Without this the
		// composer cursor never blinks and, depending on the cursor's
		// initial IsBlinked state, may not be visible at all.
		// textinput.Update ignores message types it doesn't handle, so this
		// forward is safe — the input value is only mutated on
		// KeyPressMsg/PasteMsg, which take dedicated branches earlier in
		// this Update function.
		var fwdCmd tea.Cmd
		m.input, fwdCmd = m.input.Update(msg)
		return m, combineCmds(spinnerCmd, fwdCmd)
	}
	var cmd tea.Cmd
	// During startup input drain, suppress all keyboard input.
	if !m.inputDrainUntil.IsZero() && time.Now().Before(m.inputDrainUntil) {
		debug.Log("tui", "CATCHALL dropped (input drain) key=%q text=%q", keyMsg.String(), keyMsg.Text)
		return m, spinnerCmd
	}
	// Before inputReady, discard all keyboard input (same reason as KeyPressMsg handler).
	if !m.inputReady {
		debug.Log("tui", "CATCHALL dropped (not ready) key=%q text=%q", keyMsg.String(), keyMsg.Text)
		return m, spinnerCmd
	}
	if shouldIgnoreInputUpdate(msg, m.startedAt, m.lastResizeAt) {
		debug.Log("tui", "CATCHALL ignored key text=%q", keyMsg.Text)
		return m, spinnerCmd
	}
	if !m.lastMouseAt.IsZero() && time.Since(m.lastMouseAt) < 200*time.Millisecond &&
		keyMsg.Mod == 0 && isMouseFragmentChar(keyMsg.Text) {
		debug.Log("tui", "CATCHALL dropped (mouse fragment) text=%q", keyMsg.Text)
		return m, spinnerCmd
	}

	// During the startup window, terminal CSI/OSC responses arrive as
	// misparsed KeyPressMsg events. These are always multi-character or
	// carry modifiers. Real human typing is always a single printable
	// character with no modifiers (IME uses PasteMsg instead).
	// Filter aggressively during startup to prevent garbage in textinput.
	if startupInputSuppressionActive(m.startedAt) {
		if len(keyMsg.Text) != 1 || keyMsg.Mod != 0 || !unicode.IsPrint(rune(keyMsg.Text[0])) {
			debug.Log("tui", "CATCHALL startup gate dropped key=%q text=%q mod=%v", keyMsg.String(), keyMsg.Text, keyMsg.Mod)
			return m, spinnerCmd
		}
	}

	if len(keyMsg.Text) > 1 && looksLikeTerminalResponse(keyMsg.Text) {
		// Human keyboard input produces at most 1 character per KeyPressMsg
		// (IME compositions use PasteMsg instead). Multi-character Text
		// containing terminal response patterns is a misparse from
		// EscTimeout-truncated terminal responses.
		debug.Log("tui", "CATCHALL ignored terminal fragment text=%q", truncateStr(keyMsg.Text, 60))
		return m, spinnerCmd
	}
	oldValue := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	newValue := m.input.Value()
	if oldValue != newValue {
		debug.Log("tui", "CATCHALL input changed old=%q new=%q", truncateStr(oldValue, 80), truncateStr(newValue, 80))
	}

	// Post-update terminal response cleanup: after textinput processes the
	// key, check if the accumulated value looks like a terminal response that
	// leaked through as individual single-character KeyPressMsg events.
	// Terminal responses (OSC 11, CSI CPR, DECRPM, etc.) arrive character by
	// character, each indistinguishable from normal typing. We detect them by
	// inspecting the accumulated value for distinctive patterns.
	if oldValue != newValue && looksLikeTerminalResponseInput(newValue) {
		debug.Log("tui", "CATCHALL terminal response detected in input, clearing: %q", truncateStr(newValue, 80))
		m.input.SetValue("")
	}

	// Update autocomplete state based on current input
	m.updateAutoComplete()

	return m, combineCmds(spinnerCmd, cmd)
}
