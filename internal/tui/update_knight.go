package tui

// #2422 knife D (knight task family): extracted verbatim from
// Model.Update's inline case bodies - zero behavior change.

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
	// #902: empty case left the spinner forever and agentBusy stuck —
	// every later submission queued behind a dead /knight run.
	m.setLoading(false)
	if msg.Err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight task %s failed: %v", msg.Result.TaskName, msg.Err))
	} else {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight task %s done (%s): %s", msg.Result.TaskName, msg.Result.Duration, msg.Result.Output))
	}
	return m, nil
}

func (m Model) handleKnightProjectProposalResultMsg(msg knightProjectProposalResultMsg) (tea.Model, tea.Cmd) {
	// #902: same deadlock class as knightTaskResultMsg.
	m.setLoading(false)
	if msg.Err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight proposal failed: %v", msg.Err))
	} else {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight proposal ready: %s", msg.Proposal.Title))
	}
	return m, nil
}

func (m Model) handleKnightTaskEventMsg(msg knightTaskEventMsg) (tea.Model, tea.Cmd) {
	// #902: per model_messages.go these should surface as a system chat
	// message (task started/completed progress).
	// #1890: the START event used to setLoading(false) - a scheduled task
	// never set loading, so this actively CLEARED a loading state owned
	// by something else. Only completion touches loading now.
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
