package tui

// #2422 knife C (lifecycle/window/program): extracted verbatim from
// Model.Update's inline case bodies - zero behavior change, Update is a
// thin dispatcher now (see model_update_dispatch.go).

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

func (m Model) handleLogoMsg(msg logoMsg) (tea.Model, tea.Cmd) {
	m.startupVendor = msg.Vendor
	m.startupEndpoint = msg.Endpoint
	m.startupModel = msg.Model
	m.setActiveRuntimeSelection(msg.Vendor, msg.Endpoint, msg.Model)
	return m, nil
}

func (m Model) handleWindowSizeMsg(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.handleResize(msg.Width, msg.Height)
	// Reset the startup clock when Bubble Tea sends the first WindowSizeMsg.
	// This ensures the startup input gate window is measured from the moment
	// the TUI event loop actually starts, not from model creation time (which
	// can be hundreds of milliseconds earlier due to config loading, IM setup, etc.).
	if m.startedAt.IsZero() || time.Since(m.startedAt) > startupInputGateWindow {
		m.startedAt = time.Now()
	}
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

func (m Model) handleSetProgramMsg(msg setProgramMsg) (tea.Model, tea.Cmd) {
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
}

func (m Model) handleInputDrainEndMsg() (tea.Model, tea.Cmd) {
	m.inputDrainUntil = time.Time{} // zero = drain ended
	m.inputReady = true
	debug.Log("tui", "input drain ended, input ready")
	return m, nil
}
