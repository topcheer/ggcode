package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/auth"
	"strings"
)

// handleProviderModelsRefreshResultMsg handles the corresponding message case.
func (m Model) handleProviderModelsRefreshResultMsg(msg providerModelsRefreshResultMsg) (Model, tea.Cmd) {
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

}

// handleProviderAuthStartMsg handles the corresponding message case.
func (m Model) handleProviderAuthStartMsg(msg providerAuthStartMsg) (Model, tea.Cmd) {
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

}

// handleProviderAuthResultMsg handles the corresponding message case.
func (m Model) handleProviderAuthResultMsg(msg providerAuthResultMsg) (Model, tea.Cmd) {
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

}

// handleModelPanelRefreshResultMsg handles the corresponding message case.
func (m Model) handleModelPanelRefreshResultMsg(msg modelPanelRefreshResultMsg) (Model, tea.Cmd) {
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

}
