package tui

import (
	"runtime"

	tea "charm.land/bubbletea/v2"
)

type textPasteMsg struct {
	Content string
}

// handlePaste handles the tea.PasteMsg case.
func (m Model) handlePaste(msg tea.PasteMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
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
		if (m.providerPanel.newVendorStep > 0 && m.providerPanel.newVendorStep != newVendorStepProtocol) ||
			(m.providerPanel.newEndpointStep > 0 && m.providerPanel.newEndpointStep != newEndpointStepProtocol) {
			var cmd tea.Cmd
			m.providerPanel.newVendorInput, cmd = m.providerPanel.newVendorInput.Update(msg)
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
	if m.mcpPanel != nil && m.mcpPanel.installMode {
		m.mcpPanel.installInput += msg.Content
		return m, nil
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
	if m.wechatPanel != nil && m.wechatPanel.editState.mode == imEditInput {
		m.wechatPanel.editState.editInput += msg.Content
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
	// Forward paste to main input.
	if runtime.GOOS == "windows" {
		// On Windows terminals Ctrl+V is usually intercepted and delivered as a
		// PasteMsg. Try reading a clipboard image first; if the clipboard holds
		// text instead, fall back to a plain text paste.
		return m, m.handleClipboardPasteFallback(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
