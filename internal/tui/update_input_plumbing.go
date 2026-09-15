package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// handleSetProgramMsg wires the tea.Program reference and starts the startup
// input-drain window (#2422 slice 1: extracted from Model.Update - body moved
// verbatim, zero behavior change).
func (m *Model) handleSetProgramMsg(msg setProgramMsg) (Model, tea.Cmd) {
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
	return *m, tea.Tick(250*time.Millisecond, func(_ time.Time) tea.Msg {
		return inputDrainEndMsg{}
	})
}

// handleInputDrainEndMsg ends the startup input-drain window (#2422 slice 1:
// extracted from Model.Update - body moved verbatim, zero behavior change).
func (m *Model) handleInputDrainEndMsg(msg inputDrainEndMsg) (Model, tea.Cmd) {
	m.inputDrainUntil = time.Time{} // zero = drain ended
	m.inputReady = true
	debug.Log("tui", "input drain ended, input ready")
	return *m, nil
}

// handleImageAttachedMsg queues a user-attached image (#2422 slice 1:
// extracted from Model.Update - body moved verbatim, zero behavior change).
func (m *Model) handleImageAttachedMsg(msg imageAttachedMsg) (Model, tea.Cmd) {
	m.pendingImages = append(m.pendingImages, msg)
	return *m, nil
}

// handleTextPasteMsg injects a programmatic paste into the composer (#2422
// slice 1: extracted from Model.Update - body moved verbatim, zero behavior
// change).
func (m *Model) handleTextPasteMsg(msg textPasteMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(tea.PasteMsg{Content: msg.Content})
	return *m, cmd
}
