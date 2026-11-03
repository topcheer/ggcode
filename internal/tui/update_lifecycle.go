package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m Model) handleLogoMsg(msg logoMsg) (tea.Model, tea.Cmd) {
	m.startupVendor = msg.Vendor
	m.startupEndpoint = msg.Endpoint
	m.startupModel = msg.Model
	m.setActiveRuntimeSelection(msg.Vendor, msg.Endpoint, msg.Model)
	return m, nil
}

func (m Model) handleTmuxStartupSetupMsg(msg tmuxStartupSetupMsg) (tea.Model, tea.Cmd) {
	if m.tmuxAvailable() {
		m.setupTmuxLayout(msg.Layout)
	}
	return m, nil
}

func (m Model) handleMouseWheelMsg(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {

	if vp := m.activePanelViewport(); vp != nil {
		if msg.Button == tea.MouseWheelUp {
			vp.ScrollUp(3)
		} else {
			vp.ScrollDown(3)
		}
		return m, nil
	}
	if m.chatList != nil && m.chatList.Len() > 0 {
		if msg.Button == tea.MouseWheelUp {
			m.chatList.ScrollUp(3)
		} else {
			m.chatList.ScrollDown(3)
		}
	}
	return m, nil
}
