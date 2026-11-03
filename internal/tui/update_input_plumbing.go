package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

func (m *Model) handleSetProgramMsg(msg setProgramMsg) (Model, tea.Cmd) {
	debug.Log("tui", "setProgramMsg received, program was nil=%v", m.program == nil)
	m.program = msg.Program

	if m.startedAt.IsZero() {
		m.startedAt = time.Now()
	}

	if val := m.input.Value(); val != "" && looksLikeStartupGarbage(val) {
		debug.Log("tui", "clearing pre-drain input garbage: %q", util.Truncate(val, 80))
		m.input.Reset()
	}

	m.inputDrainUntil = time.Now().Add(250 * time.Millisecond)
	return *m, tea.Tick(250*time.Millisecond, func(_ time.Time) tea.Msg {
		return inputDrainEndMsg{}
	})
}

func (m *Model) handleInputDrainEndMsg(msg inputDrainEndMsg) (Model, tea.Cmd) {
	m.inputDrainUntil = time.Time{}
	m.inputReady = true
	debug.Log("tui", "input drain ended, input ready")

	return *m, tea.Cmd(func() tea.Msg { return usageSidebarRefreshMsg{} })
}

func (m *Model) handleImageAttachedMsg(msg imageAttachedMsg) (Model, tea.Cmd) {
	m.pendingImages = append(m.pendingImages, msg)
	return *m, nil
}

func (m *Model) handleTextPasteMsg(msg textPasteMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(tea.PasteMsg{Content: msg.Content})
	return *m, cmd
}
