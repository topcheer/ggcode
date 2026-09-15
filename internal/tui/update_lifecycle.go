package tui

// #2422 knife C (lifecycle tail): extracted verbatim from Model.Update's
// inline case bodies - zero behavior change, Update is a thin dispatcher
// now (see model_update_dispatch.go).
//
// The window/setProgram/input-drain cases live in update_window.go and
// update_input_plumbing.go (#2423 slice 1, merged to main first): those
// pointer-receiver versions won the race, so this file keeps only the
// cases unique to #2422.

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
	// Route mouse wheel to the active panel's viewport if the open panel
	// has one (the stats panel). Panels without a scrollable viewport
	// fall through to scrolling the main conversation. MouseWheelMsg
	// implements the MouseMsg interface, so the original type switch
	// matched it BEFORE case tea.MouseMsg; the exact-type dispatch table
	// preserves that distinction structurally (#2422).
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

// handleSetProgramMsg / handleInputDrainEndMsg: see update_input_plumbing.go
// (#2423 slice 1 versions, merged to main before this PR).

// handleWindowSizeMsg: see update_window.go (#2423 slice 1).
