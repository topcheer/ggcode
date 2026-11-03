package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

func (m Model) handleKnightStartupHintMsg(msg knightStartupHintMsg) (tea.Model, tea.Cmd) {
	if msg.Hint != "" {
		m.chatWriteSystem(nextSystemID(), msg.Hint)
		m.chatListScrollToBottom()
	}
	return m, nil
}

func (m Model) handleKnightTaskResultMsg(msg knightTaskResultMsg) (tea.Model, tea.Cmd) {

	m.setLoading(false)

	m.spinner.Stop()
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	if msg.Err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight task %s failed: %v", msg.Result.TaskName, msg.Err))
	} else {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight task %s done (%s): %s", msg.Result.TaskName, msg.Result.Duration, msg.Result.Output))
	}
	return m, nil
}

func (m Model) handleKnightProjectProposalResultMsg(msg knightProjectProposalResultMsg) (tea.Model, tea.Cmd) {

	m.setLoading(false)

	m.spinner.Stop()
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	if msg.Err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight proposal failed: %v", msg.Err))
	} else {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight proposal ready: %s", msg.Proposal.Title))
	}
	return m, nil
}

func (m Model) handleKnightTaskEventMsg(msg knightTaskEventMsg) (tea.Model, tea.Cmd) {

	if msg.Report != "" {
		if m.knightRunning > 0 {
			m.knightRunning--
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight %s: %s", msg.TaskName, msg.Report))
	} else {
		m.knightRunning++
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight %s started", msg.TaskName))
	}
	return m, nil
}
