package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/safego"
)

func (m Model) Update(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
	defer func() {
		next, ok := model.(Model)
		if !ok {
			return
		}
		model, cmd = next.withTerminalTitleCmd(cmd)
	}()

	m.syncAsyncStateCaches()
	model = m

	if m.usageProbeDirty {
		m.usageProbeDirty = false
		m2, probeCmd := m.handleUsageSidebarRefreshMsg()
		m = m2
		if probeCmd != nil {
			prog := m.program
			go safego.Run("usage.dirtyProbe", func() {
				if msg := probeCmd(); msg != nil && prog != nil {
					prog.Send(msg)
				}
			})
		}
	}

	var spinnerCmd tea.Cmd
	if m.spinner.IsActive() {
		spinnerCmd = m.spinner.Update(msg)
	}

	if m2, hit, c := m.dispatchUpdate(msg, spinnerCmd); hit {
		return m2, c
	}

	if _, isMouse := msg.(tea.MouseMsg); isMouse {

		return m, nil
	}

	if _, isSpinner := msg.(spinnerMsg); !isSpinner {
		msgType := fmt.Sprintf("%T", msg)
		if !strings.Contains(msgType, "Blink") {

		}
	}
	_, isKeyPress := msg.(tea.KeyPressMsg)
	if !isKeyPress {

		var fwdCmd tea.Cmd
		m.input, fwdCmd = m.input.Update(msg)
		return m, combineCmds(spinnerCmd, fwdCmd)
	}
	var keyCmd tea.Cmd

	if !m.inputDrainUntil.IsZero() && time.Now().Before(m.inputDrainUntil) {

		return m, spinnerCmd
	}

	if !m.inputReady {

		return m, spinnerCmd
	}

	oldValue := m.input.Value()
	m.input, keyCmd = m.input.Update(msg)
	newValue := m.input.Value()
	if oldValue != newValue {

	}

	m.updateAutoComplete()

	if oldValue != newValue {
		m.inputHint = ""
	}

	return m, combineCmds(spinnerCmd, keyCmd)
}
