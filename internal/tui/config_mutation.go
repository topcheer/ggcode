package tui

import (
	tea "charm.land/bubbletea/v2"
)

type configMutationMsg struct {
	apply func(m *Model) error

	next func(m *Model) tea.Cmd

	fail func(err error) tea.Msg
}

func (m Model) handleConfigMutationMsg(msg configMutationMsg) (Model, tea.Cmd) {
	if msg.apply == nil {
		return m, nil
	}
	if err := msg.apply(&m); err != nil {
		if msg.fail != nil {
			return m, func() tea.Msg { return msg.fail(err) }
		}
		return m, nil
	}
	if msg.next != nil {
		return m, msg.next(&m)
	}
	return m, nil
}
