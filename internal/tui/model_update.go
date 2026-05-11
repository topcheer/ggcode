package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"strings"
	"time"

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
		// Default: scroll the main conversation.
		if m.chatList != nil && m.chatList.Len() > 0 {
			if msg.Button == tea.MouseWheelUp {
				m.chatList.ScrollUp(3)
			} else {
				m.chatList.ScrollDown(3)
			}
		}
		return m, nil

	case tea.MouseMsg:
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
		// Paste is allowed while loading — the agent loop supports
		// interleaving user messages mid-run.
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
		if m.wecomPanel != nil && m.wecomPanel.createMode {
			m.wecomPanel.createInput += msg.Content
			return m, nil
		}
		if m.mattermostPanel != nil && m.mattermostPanel.createMode {
			m.mattermostPanel.createInput += msg.Content
			return m, nil
		}
		if m.matrixPanel != nil && m.matrixPanel.createMode {
			m.matrixPanel.createInput += msg.Content
			return m, nil
		}
		if m.signalPanel != nil && m.signalPanel.createMode {
			m.signalPanel.createInput += msg.Content
			return m, nil
		}
		if m.ircPanel != nil && m.ircPanel.createMode {
			m.ircPanel.createInput += msg.Content
			return m, nil
		}
		if m.nostrPanel != nil && m.nostrPanel.createMode {
			m.nostrPanel.createInput += msg.Content
			return m, nil
		}
		if m.twitchPanel != nil && m.twitchPanel.createMode {
			m.twitchPanel.createInput += msg.Content
			return m, nil
		}
		if m.whatsappPanel != nil && m.whatsappPanel.createMode {
			m.whatsappPanel.createInput += msg.Content
			return m, nil
		}
		// Forward paste to PC panel create-input.
		if m.pcPanel != nil && m.pcPanel.createMode {
			m.pcPanel.createInput += msg.Content
			return m, nil
		}
		// Forward paste to IM adapter edit mode (shared editInput).
		if m.qqPanel != nil && m.qqPanel.editState.mode == imEditInput {
			m.qqPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.tgPanel != nil && m.tgPanel.editState.mode == imEditInput {
			m.tgPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.discordPanel != nil && m.discordPanel.editState.mode == imEditInput {
			m.discordPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.feishuPanel != nil && m.feishuPanel.editState.mode == imEditInput {
			m.feishuPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.slackPanel != nil && m.slackPanel.editState.mode == imEditInput {
			m.slackPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.dingtalkPanel != nil && m.dingtalkPanel.editState.mode == imEditInput {
			m.dingtalkPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.wecomPanel != nil && m.wecomPanel.editState.mode == imEditInput {
			m.wecomPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.mattermostPanel != nil && m.mattermostPanel.editState.mode == imEditInput {
			m.mattermostPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.matrixPanel != nil && m.matrixPanel.editState.mode == imEditInput {
			m.matrixPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.signalPanel != nil && m.signalPanel.editState.mode == imEditInput {
			m.signalPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.ircPanel != nil && m.ircPanel.editState.mode == imEditInput {
			m.ircPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.nostrPanel != nil && m.nostrPanel.editState.mode == imEditInput {
			m.nostrPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.twitchPanel != nil && m.twitchPanel.editState.mode == imEditInput {
			m.twitchPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.whatsappPanel != nil && m.whatsappPanel.editState.mode == imEditInput {
			m.whatsappPanel.editState.editInput += msg.Content
			return m, nil
		}
		if m.pcPanel != nil && m.pcPanel.editState.mode == imEditInput {
			m.pcPanel.editState.editInput += msg.Content
			return m, nil
		}
		// Forward paste to questionnaire input if active.
		if m.pendingQuestionnaire != nil && m.pendingQuestionnaire.activeQuestionAllowsFreeform() {
			var cmd tea.Cmd
			m.pendingQuestionnaire.input, cmd = m.pendingQuestionnaire.input.Update(msg)
			m.pendingQuestionnaire.saveActiveQuestionInput()
			return m, cmd
		}
		// Forward paste to stream panel editing inputs.
		if m.streamPanel != nil && m.streamPanel.editingField != "" {
			var cmd tea.Cmd
			switch m.streamPanel.editingField {
			case "key":
				m.streamPanel.keyInput, cmd = m.streamPanel.keyInput.Update(msg)
			case "url":
				m.streamPanel.urlInput, cmd = m.streamPanel.urlInput.Update(msg)
			case "name":
				m.streamPanel.nameInput, cmd = m.streamPanel.nameInput.Update(msg)
			default:
				return m, nil
			}
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
		if m.startupBannerVisible {
			m.startupBannerVisible = false
		}
		if msg.String() != "ctrl+c" {
			m.resetExitConfirm()
		}
		debug.Log("tui", "KEYPRESS str=%q text=%q mod=%v code=%v input_before=%q", msg.String(), msg.Text, msg.Mod, msg.Code, util.Truncate(m.input.Value(), 80))
		if msg.String() == "ctrl+r" {
			m.sidebarVisible = !m.sidebarVisible
			if m.config != nil {
				_ = m.config.SaveSidebarPreference(m.sidebarVisible)
			}
			m.relayoutAfterSidebarChange()
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
			// Follow mode: open panel (navigate with arrow keys)
			if len(m.subAgentFollow.slots) > 0 && !m.subAgentFollow.isActive() {
				m.subAgentFollow.activate(0)
				// Immediately rebuild the follow view so View() has content
				m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
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

	case streamMsg:
		if m.runCanceled {
			return m, nil
		}
		m.appendStreamChunk(string(msg))
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	case remoteRestartMsg:
		m.quitting = true
		m.restartRequested = true
		return m, tea.Quit

	case remoteInboundMsg:
		// Track the originating adapter for per-channel echo suppression.
		m.remoteInboundAdapter = msg.Message.Envelope.Adapter
		prompt := buildRemoteInboundPrompt(msg.Message)

		// Handle IM approval reply: y/a/n for pending tool permission
		if m.pendingApproval != nil {
			text := strings.TrimSpace(prompt)
			if text != "" {
				decision, ok := parseApprovalReply(text)
				if ok {
					toolName := m.pendingApproval.ToolName
					decisionStr := "deny"
					var cmd tea.Cmd
					if decision == permission.Allow && isApprovalAlwaysReply(text) {
						cmd = m.handleApprovalAllowAlways()
						decisionStr = "always"
					} else {
						if decision == permission.Allow {
							decisionStr = "allow"
						}
						cmd = m.handleApproval(decision)
					}
					if msg.Response != nil {
						msg.Response <- nil
					}
					// Send result confirmation back to IM
					if m.approvalNotifiedIM {
						m.emitIMApprovalResult(toolName, decisionStr)
					}
					return m, cmd
				}
			}
		}

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
				q := m.pendingQuestionnaire.request.Questions[nextIdx]
				fallback := m.formatIMAskUserQuestion(m.pendingQuestionnaire.request.Title, q)
				if len(q.Choices) > 0 {
					m.emitIMAskUserInteractive(m.pendingQuestionnaire.request.Title, q, fallback)
				} else {
					m.emitIMAskUser(fallback)
				}
			}
			return m, nil
		}
		if response, handled := m.ExecuteRemoteSlashCommand(prompt); handled {
			// Handle /restart: send IM confirmation first, then quit after delay.
			if response == "RESTART" || response == "RESTART:DEBUG" {
				if response == "RESTART:DEBUG" {
					m.emitIMText("\U0001f504 Restarting ggcode with debug mode enabled...")
				} else {
					m.emitIMText("\U0001f504 Restarting ggcode...")
				}
				if msg.Response != nil {
					msg.Response <- nil
				}
				return m, m.scheduleRemoteRestart()
			}
			// Handle /muteself: send warning first, then mute after delay.
			if strings.HasPrefix(response, "MUTES:") {
				adapter := strings.TrimPrefix(response, "MUTES:")
				m.emitIMText("\U0001f507 Muting this adapter... You will stop receiving replies. Use /restart from another adapter to recover.")
				if msg.Response != nil {
					msg.Response <- nil
				}
				return m, m.scheduleMuteSelf(adapter)
			}
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
		m.chatWriteUser(nextChatID(), msg.Text)
		m.chatWriteSystem(nextSystemID(), m.t("interrupt.delivered"))
		m.chatListScrollToBottom()
		return m, nil

	case webchatUserMsg:
		// Webchat message injected from the webui. Handle like user input.
		text := msg.Text
		if text == "" {
			return m, nil
		}
		// Notify Knight idle timer — webchat counts as user activity too.
		if m.knight != nil {
			m.knight.NotifyActivity()
		}
		// Render the user bubble and persist to session -- same as keyboard input.
		m.chatWriteUser(nextChatID(), text)
		m.chatListScrollToBottom()
		m.appendUserMessage(text)
		// If agent is idle, start a new run with the webchat message
		if m.cancelFunc == nil {
			m.streamBuffer = &bytes.Buffer{}
			m.shellBuffer = nil
			m.streamPrefixWritten = false
			m.loading = true
			m.loopStart = time.Now()
			m.statusActivity = m.t("status.thinking")
			m.statusToolName = ""
			m.statusToolArg = ""
			m.statusToolCount = 0
			cmd := m.startAgent(text)
			return m, tea.Batch(m.startLoadingSpinner(m.statusActivity), cmd)
		}
		// Agent is busy — queue as interruption
		m.queuePendingSubmission(text)
		return m, nil

	case webuiReadyMsg:
		// Display webui URL as a subtle system message in the chat area
		if msg.Addr != "" {
			url := "http://" + msg.Addr
			if msg.Token != "" {
				url += "#token=" + msg.Token
			}
			m.chatWriteSystem(nextSystemID(), "\u2B21 WebUI: "+url)
			m.chatListScrollToBottom()
		}
		return m, nil

	case knightStartupHintMsg:
		if msg.Hint != "" {
			m.chatWriteSystem(nextSystemID(), msg.Hint)
			m.chatListScrollToBottom()
		}
		return m, nil

	case harnessPanelRefreshResultMsg:
		m.applyHarnessPanelResult(msg)
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
		m.chatFinishAllRunningTools()
		m.cancelFunc = nil
		m.streamPrefixWritten = false
		// Finalize streaming assistant in chatList
		m.chatFinishAssistant(m.currentAssistantID())
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
		m.chatListScrollToBottom()
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
		m.chatFinishAllRunningTools()
		m.cancelFunc = nil
		m.chatFinishAssistant(m.currentAssistantID())
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
		m.chatListScrollToBottom()
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
		// Auto-exit shell mode so user returns to the prompt
		m.setShellMode(false)
		if msg.Status == toolpkg.CommandJobFailed || msg.Status == toolpkg.CommandJobTimedOut {
			m.runFailed = true
			if m.pendingSubmissionCount() > 0 {
				m.restorePendingInput()
			}
			if text := strings.TrimSpace(msg.ErrText); text != "" {
				m.chatWriteSystem(nextSystemID(), text)
			}
		}
		if !wasCanceled && (hadShellOutput || strings.TrimSpace(msg.ErrText) != "") {
		}
		if msg.Status == toolpkg.CommandJobCompleted && m.pendingSubmissionCount() > 0 && !wasCanceled && !wasFailed {
			return m, m.submitShellCommand(m.consumePendingSubmission(), false)
		}
		m.chatListScrollToBottom()
		return m, nil

	case errMsg:
		if errors.Is(msg.err, context.Canceled) {
			return m, nil
		}
		m.runFailed = true
		m.loading = false
		m.spinner.Stop()
		m.chatFinishAllRunningTools()
		m.cancelFunc = nil
		if m.pendingSubmissionCount() > 0 {
			m.restorePendingInput()
		}
		m.chatWriteSystem(nextSystemID(), formatUserFacingError(m.currentLanguage(), msg.err))
		m.chatListScrollToBottom()
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
		m.chatFinishAllRunningTools()
		m.cancelFunc = nil
		if m.pendingSubmissionCount() > 0 {
			m.restorePendingInput()
		}
		m.chatWriteSystem(nextSystemID(), formatUserFacingError(m.currentLanguage(), msg.Err))
		m.emitIMText(formatUserFacingError(m.currentLanguage(), msg.Err))
		m.chatListScrollToBottom()
		return m, nil

	case autoRunCheckResultMsg:
		m.loading = false
		m.spinner.Stop()
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		if msg.Err != nil {
			debug.Log("auto-run", "routing check failed; continuing normally: %v", msg.Err)
			return m, m.continueDisplayedNormalTextRun(msg.Text)
		}
		if msg.Result != nil {
			switch msg.Result.Decision {
			case harness.RouteSuggest:
				m.pendingAutoRun = msg.Result
				m.pendingAutoRunText = msg.Text
				m.chatWriteSystem(nextChatID(), msg.Result.Message)
				m.chatListScrollToBottom()
				return m, nil
			case harness.RouteHarness:
				return m, m.handleAutoRun(msg.Text, msg.Result)
			}
		}
		// No harness routing — continue to normal agent.
		return m, m.continueDisplayedNormalTextRun(msg.Text)

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
		m.chatFinishAllRunningTools()
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
			m.chatWriteSystem(nextSystemID(), msg.Err.Error())
			m.chatListScrollToBottom()
			return m, nil
		}
		rendered := harness.FormatRunSummary(msg.Summary)
		if streamedHarnessOutput {
			rendered = trimHarnessRunOutputSection(rendered)
		}
		// Append CTA (next action) - generate from summary if not provided
		ctaAction := msg.CTA
		ctaMsg := msg.CTAMessage
		if ctaMsg == "" && msg.Summary != nil {
			ctaAction, ctaMsg = harness.GenerateCTA(msg.Summary, msg.Err)
		}
		if ctaMsg != "" {
			rendered += fmt.Sprintf("\nNext: %s", ctaMsg)
		}
		// Set pending review for one-key approve/reject if CTA is review
		if ctaAction == harness.CTAReview && msg.Summary != nil && msg.Summary.Task != nil {
			m.pendingHarnessReview = msg.Summary.Task
			rendered += "\nPress Enter to approve, Esc to skip."
		}
		m.renderStreamBuffer(true)
		m.chatWriteSystem(nextSystemID(), rendered)
		m.chatListScrollToBottom()
		// Broadcast harness result to WebUI subscribers if available
		if m.webuiBridge != nil {
			m.webuiBridge.BroadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: rendered})
		}
		if !wasCanceled && !wasFailed && m.pendingSubmissionCount() > 0 {
			return m, m.submitText(m.consumePendingSubmission(), false)
		}
		return m, nil

	case harnessReviewResultMsg:
		m.loading = false
		m.spinner.Stop()
		if msg.Err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Review failed for task %s: %v", msg.TaskID, msg.Err))
		} else {
			status := "approved"
			if msg.Task != nil {
				status = string(msg.Task.ReviewStatus)
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Task %s review: %s", msg.TaskID, status))
			// If approved, set pending promote for one-key CTA
			if msg.Task != nil && msg.Task.ReviewStatus == harness.ReviewApproved {
				m.pendingHarnessPromote = msg.Task
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Task %s approved. Press Enter to promote, Esc to skip.", msg.TaskID))
			}
		}
		m.chatListScrollToBottom()
		if m.webuiBridge != nil {
			m.webuiBridge.BroadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: fmt.Sprintf("Task %s review: done", msg.TaskID)})
		}
		return m, nil

	case harnessPromoteResultMsg:
		m.loading = false
		m.spinner.Stop()
		if msg.Err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Promote failed for task %s: %v", msg.TaskID, msg.Err))
		} else if msg.Task != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Task %s promoted successfully.", msg.TaskID))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Task %s promote completed.", msg.TaskID))
		}
		m.chatListScrollToBottom()
		if m.webuiBridge != nil {
			m.webuiBridge.BroadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: fmt.Sprintf("Task %s promoted", msg.TaskID)})
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
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Knight task failed: %v", msg.Err))
			m.chatListScrollToBottom()
			return m, nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🌙 Knight task completed: %s", msg.Goal))
		m.chatWriteSystem(nextSystemID(), strings.TrimSpace(msg.Result.Output))
		m.chatListScrollToBottom()
		return m, nil

	case knightProjectProposalResultMsg:
		m.loading = false
		m.spinner.Stop()
		m.cancelFunc = nil
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		if msg.Err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Knight proposal failed: %v", msg.Err))
			m.chatListScrollToBottom()
			return m, nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("📝 Knight proposal created: %s", msg.Proposal.Title))
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("ID: %s\nPath: %s\nReview with /knight proposals %s", msg.Proposal.ID, msg.Proposal.Path, msg.Proposal.ID))
		m.chatListScrollToBottom()
		return m, nil

	case knightTaskEventMsg:
		if msg.Report == "" {
			// Task started
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🌙 Knight: starting %s", msg.TaskName))
		} else if msg.TaskName == "" {
			// Detailed report from emitReport (same as IM)
			m.chatWriteSystem(nextSystemID(), msg.Report)
		} else {
			// Task completed with summary
			suffix := ""
			if msg.Duration > 0 {
				suffix = fmt.Sprintf(" (%.0fs)", msg.Duration.Seconds())
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🌙 Knight %s completed%s\n%s", msg.TaskName, suffix, msg.Report))
		}
		m.chatListScrollToBottom()
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
			m.chatWriteUser(nextChatID(), commandText)
			m.appendUserMessage(commandText)
			m.chatWriteSystem(nextSystemID(), formatHarnessInitResult(msg.Result))
			m.chatListScrollToBottom()
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
		cmd := m.refreshHarnessPanelForced()
		if !m.shouldAutoRefreshHarnessTask() {
			// Task completed — stop polling, but still return the refresh cmd
			// so the data arrives.
			return m, cmd
		}
		if cmd != nil {
			return m, tea.Batch(cmd, m.pollHarnessPanelAutoRefresh())
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
		m.approvalOptions = defaultApprovalOptions()
		m.approvalCursor = 0
		// Push to IM if available so user can approve remotely
		m.approvalNotifiedIM = false
		if m.imEmitter != nil {
			m.emitIMApproval(msg.ToolName, msg.Input)
			m.approvalNotifiedIM = true
		}
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
			q := msg.Request.Questions[0]
			fallback := m.formatIMAskUserQuestion(msg.Request.Title, q)
			if len(q.Choices) > 0 {
				m.emitIMAskUserInteractive(msg.Request.Title, q, fallback)
			} else {
				m.emitIMAskUser(fallback)
			}
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
		m.subAgentFollow.markDirty(msg.AgentID)

		if m.subAgentFollow.isActive() {
			// Follow panel open: only rebuild the active agent's view.
			// Strip is refreshed less frequently (on spawn/complete via other paths).
			if msg.AgentID == m.subAgentFollow.activeID && m.subAgentFollow.shouldRebuild(m.subAgentFollow.activeID) {
				m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
				m.chatListScrollToBottom()
			} else if msg.AgentID == m.subAgentFollow.activeID {
				// Throttled — schedule delayed retry to ensure eventual render.
				return m, tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
					return subAgentFollowRefreshMsg{}
				})
			}
		} else {
			// No follow panel — refresh strip slot list.
			m.subAgentFollow.refreshSlots(m.subAgentMgr)
			m.subAgentFollow.refreshSwarmSlots(m.swarmMgr)
		}

		if m.subAgentFollow.isActive() && m.subAgentFollow.currentSlotIndex() == -1 {
			m.subAgentFollow.deactivate()
		}
		if m.subAgentFollow.hasTerminalSlots() {
			return m, tea.Tick(15*time.Second, func(t time.Time) tea.Msg {
				return followGraceTickMsg{}
			})
		}
		return m, nil

	case subAgentFollowRefreshMsg:
		// Delayed rebuild after throttle window
		if m.subAgentFollow.isActive() && m.subAgentFollow.shouldRebuild(m.subAgentFollow.activeID) {
			m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
		} else if m.subAgentFollow.isActive() && m.subAgentFollow.dirty[m.subAgentFollow.activeID] {
			// Still dirty but throttled — reschedule
			return m, tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
				return subAgentFollowRefreshMsg{}
			})
		}
		return m, nil

	case followGraceTickMsg:
		// Re-evaluate grace period: refresh slots and remove expired terminal ones
		m.subAgentFollow.refreshSlots(m.subAgentMgr)
		m.subAgentFollow.refreshSwarmSlots(m.swarmMgr)
		m.subAgentFollow.cleanup(m.subAgentMgr, m.swarmMgr)

		// Auto-deactivate if the followed agent was removed
		if m.subAgentFollow.isActive() && m.subAgentFollow.currentSlotIndex() == -1 {
			m.subAgentFollow.deactivate()
		}

		// Continue ticking only while terminal slots still exist
		if m.subAgentFollow.hasTerminalSlots() {
			return m, tea.Tick(15*time.Second, func(t time.Time) tea.Msg {
				return followGraceTickMsg{}
			})
		}
		return m, nil

	case subAgentDoneMsg:
		// A sub-agent or swarm teammate finished its task.
		// Show a human-readable system message and wake the main agent.
		m.chatWriteSystem(nextSystemID(), m.formatSubAgentDoneNotice(msg))
		m.chatListScrollToBottom()

		// Build prompt for the main agent.
		var agentHint string
		if msg.IsError {
			agentHint = fmt.Sprintf("%s failed with an error. You may want to investigate or retry.", msg.AgentName)
		} else {
			agentHint = fmt.Sprintf("%s has completed its task. You can use list_agents or wait_agent to review the result, or continue your work.", msg.AgentName)
		}

		if !m.loading {
			// Agent is idle — start a new loop to process the notification.
			return m, m.submitText(agentHint, true)
		}
		// Agent is busy — queue for processing after current run.
		m.queuePendingSubmissionHidden(agentHint)
		return m, nil
	case modeChangeMsg:
		m.mode = msg.Mode
		return m, nil

	case cronPromptMsg:
		sysMsg := m.t("cron.firing")
		m.chatWriteSystem(nextSystemID(), sysMsg)
		m.emitIMText(sysMsg)
		// If agent is idle, submit the cron prompt immediately.
		// Otherwise queue it for processing after the current run finishes.
		if !m.loading {
			return m, m.submitText(msg.Prompt, true)
		}
		m.queuePendingSubmissionHidden(msg.Prompt)
		return m, nil

	case skillsChangedMsg:
		m.refreshCommands()
		return m, nil

	case updateCheckResultMsg:
		m.applyUpdateCheckResult(msg)
		return m, nil

	case updateCheckTickMsg:
		return m, tea.Batch(m.checkForUpdateCmd(), m.scheduleUpdateCheckCmd())

	case gitBranchTickMsg:
		m.refreshCachedGitBranch()
		return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
			return gitBranchTickMsg{}
		})

	case imPanelRefreshMsg:
		// Continue ticking as long as any IM panel that needs live state is open
		if m.whatsappPanel != nil {
			return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
				return imPanelRefreshMsg{}
			})
		}
		return m, nil

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
			m.chatStartTool(ts)
			if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
				m.renderStreamBuffer(true)
			}
			startCmd := m.spinner.Start(util.FirstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
			spinnerCmd = combineCmds(spinnerCmd, startCmd)
		} else {
			m.chatFinishTool(ts)
			ts.Elapsed = m.spinner.Elapsed()
			m.spinner.Stop()
			spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
			// Reset stream prefix so next text block gets ●
			m.streamPrefixWritten = false
			// Reset stream buffer position for next text chunk
		}
		m.chatListScrollToBottom()
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
				m.chatStartTool(ts.ToolStatusMsg)
				if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
					m.renderStreamBuffer(true)
				}
				startCmd := m.spinner.Start(util.FirstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts.ToolStatusMsg), toolDetail(ts.ToolStatusMsg))))
				spinnerCmd = combineCmds(spinnerCmd, startCmd)
			} else {
				m.chatFinishTool(ts.ToolStatusMsg)
				ts.ToolStatusMsg.Elapsed = m.spinner.Elapsed()
				m.spinner.Stop()
				spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
				m.streamPrefixWritten = false
			}
		}
		m.chatListScrollToBottom()
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
			m.chatStartTool(ts)
			if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
				m.renderStreamBuffer(true)
			}
			startCmd := m.spinner.Start(util.FirstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
			spinnerCmd = combineCmds(spinnerCmd, startCmd)
		} else {
			m.chatFinishTool(ts)
			ts.Elapsed = m.spinner.Elapsed()
			m.spinner.Stop()
			spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
			m.streamPrefixWritten = false
		}
		m.chatListScrollToBottom()
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

	case wechatQRCodeMsg:
		return m.handleWechatQRCodeMsg(msg)
	case wechatQRPollMsg:
		return m.handleWechatQRPollMsg(msg)

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
				// Open QR overlay with share link
				if msg.shareQRCode != "" || msg.shareLink != "" {
					m.openQROverlayDirect(
						"QQ — Share Link",
						"Scan to share",
						msg.shareQRCode,
						msg.shareLink,
					)
				}
			}
		}
		return m, nil

	case imPanelResultMsg:
		msgText := msg.message
		if msg.err != nil {
			msgText = msg.err.Error()
		}
		if m.imPanel != nil {
			m.imPanel.message = msgText
		}
		// Forward to the currently active channel panel so the user sees
		// feedback when toggling disable/enable from within a panel.
		if p := m.activeIMPanel(); p != nil {
			*p = msgText
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

	case whatsappBindResultMsg:
		if m.whatsappPanel != nil {
			if msg.err != nil {
				m.whatsappPanel.message = msg.err.Error()
			} else {
				m.whatsappPanel.message = msg.message
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

	case wecomBindResultMsg:
		if m.wecomPanel != nil {
			if msg.err != nil {
				m.wecomPanel.message = msg.err.Error()
			} else {
				m.wecomPanel.message = msg.message
			}
		}
		return m, nil

	case mattermostBindResultMsg:
		if m.mattermostPanel != nil {
			if msg.err != nil {
				m.mattermostPanel.message = msg.err.Error()
			} else {
				m.mattermostPanel.message = msg.message
			}
		}
		return m, nil

	case matrixBindResultMsg:
		if m.matrixPanel != nil {
			if msg.err != nil {
				m.matrixPanel.message = msg.err.Error()
			} else {
				m.matrixPanel.message = msg.message
			}
		}
		return m, nil

	case signalBindResultMsg:
		if m.signalPanel != nil {
			m.signalPanel.installing = false
			if msg.err != nil {
				m.signalPanel.message = msg.err.Error()
			} else {
				m.signalPanel.message = msg.message
			}
			// Re-check daemon status after install
			if m.signalPanel.daemonOK != nil && !*m.signalPanel.daemonOK {
				return m, checkSignalDaemonCmd()
			}
		}
		return m, nil

	case signalDaemonCheckMsg:
		if m.signalPanel != nil {
			m.signalPanel.daemonOK = &msg.ok
		}
		return m, nil

	case signalQRCodeMsg:
		if m.signalPanel != nil {
			m.signalPanel.qrFetching = false
			if msg.err != nil {
				m.signalPanel.qrError = msg.err.Error()
			} else {
				m.signalPanel.qrCode = msg.qr
				// Open QR overlay for user to scan device pairing code
				m.openQROverlayDirect(
					"Signal — "+m.t("panel.signal.qr_title"),
					m.t("panel.signal.qr_scan_hint"),
					msg.qr,
					"",
				)
			}
		}
		return m, nil

	case ircBindResultMsg:
		if m.ircPanel != nil {
			if msg.err != nil {
				m.ircPanel.message = msg.err.Error()
			} else {
				m.ircPanel.message = msg.message
			}
		}
		return m, nil

	case nostrBindResultMsg:
		if m.nostrPanel != nil {
			if msg.err != nil {
				m.nostrPanel.message = msg.err.Error()
			} else {
				m.nostrPanel.message = msg.message
				m.nostrPanel.qrCode = msg.qrCode
				m.nostrPanel.generatedNpub = msg.npub
				// Open QR overlay with npub
				if msg.qrCode != "" || msg.npub != "" {
					m.openQROverlayDirect(
						"Nostr — Public Key",
						m.t("panel.qr.scan_hint"),
						msg.qrCode,
						msg.npub,
					)
				}
			}
		}
		return m, nil

	case twitchBindResultMsg:
		if m.twitchPanel != nil {
			if msg.err != nil {
				m.twitchPanel.message = msg.err.Error()
			} else {
				m.twitchPanel.message = msg.message
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

	case imEditResultMsg:
		// Dispatch to whichever panel is active
		if m.qqPanel != nil && m.qqPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.qqPanel.editState, msg)
		} else if m.tgPanel != nil && m.tgPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.tgPanel.editState, msg)
		} else if m.discordPanel != nil && m.discordPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.discordPanel.editState, msg)
		} else if m.feishuPanel != nil && m.feishuPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.feishuPanel.editState, msg)
		} else if m.slackPanel != nil && m.slackPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.slackPanel.editState, msg)
		} else if m.dingtalkPanel != nil && m.dingtalkPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.dingtalkPanel.editState, msg)
		} else if m.pcPanel != nil && m.pcPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.pcPanel.editState, msg)
		} else if m.wechatPanel != nil {
			if m.wechatPanel.editState.mode != imEditNone {
				m.applyIMEditResult(&m.wechatPanel.editState, msg)
			} else if msg.err != nil {
				m.wechatPanel.message = fmt.Sprintf("Error: %v", msg.err)
			} else if msg.adapterName != "" {
				m.wechatPanel.message = m.t("panel.wechat.auth_success") + " (" + msg.adapterName + ")"
			}
		} else if m.wecomPanel != nil && m.wecomPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.wecomPanel.editState, msg)
		} else if m.mattermostPanel != nil && m.mattermostPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.mattermostPanel.editState, msg)
		} else if m.matrixPanel != nil && m.matrixPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.matrixPanel.editState, msg)
		} else if m.signalPanel != nil && m.signalPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.signalPanel.editState, msg)
		} else if m.ircPanel != nil && m.ircPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.ircPanel.editState, msg)
		} else if m.nostrPanel != nil && m.nostrPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.nostrPanel.editState, msg)
		} else if m.twitchPanel != nil && m.twitchPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.twitchPanel.editState, msg)
		} else if m.whatsappPanel != nil && m.whatsappPanel.editState.mode != imEditNone {
			m.applyIMEditResult(&m.whatsappPanel.editState, msg)
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
			debug.Log("tui", "clearing pre-drain input garbage: %q", util.Truncate(val, 80))
			m.input.Reset()
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

	oldValue := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	newValue := m.input.Value()
	if oldValue != newValue {
		debug.Log("tui", "CATCHALL input changed old=%q new=%q", util.Truncate(oldValue, 80), util.Truncate(newValue, 80))
	}

	// Update autocomplete state based on current input
	m.updateAutoComplete()

	// Clear input hint when user types
	if oldValue != newValue {
		m.inputHint = ""
	}

	return m, combineCmds(spinnerCmd, cmd)
}

// parseApprovalReply parses an IM text reply as an approval decision.
// Returns (decision, true) if the text is a valid approval response.
// Returns (Deny, false) for unrecognized text.
func parseApprovalReply(text string) (permission.Decision, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	switch t {
	case "y", "yes", "ok", "好", "好的", "允许", "同意", "确认":
		return permission.Allow, true
	case "a", "always", "总是允许", "总是", "始终允许":
		return permission.Allow, true
	case "n", "no", "nope", "拒绝", "取消", "不要", "deny":
		return permission.Deny, true
	}
	// Single-word prefix match: "y xxx" → allow
	if strings.HasPrefix(t, "y") && len(t) <= 3 {
		return permission.Allow, true
	}
	if strings.HasPrefix(t, "n") && len(t) <= 3 {
		return permission.Deny, true
	}
	return permission.Deny, false
}

// isApprovalAlwaysReply returns true if the IM text indicates "always allow".
func isApprovalAlwaysReply(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	switch t {
	case "a", "always", "总是允许", "总是", "始终允许":
		return true
	}
	return false
}
