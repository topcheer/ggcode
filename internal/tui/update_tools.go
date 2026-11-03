package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/util"
)

func (m Model) handleToolStatusMsg(msg toolStatusMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {
	if m.runCanceled || !m.loading {
		return m, nil
	}
	ts := ToolStatusMsg(msg)
	m.updateActiveMCPTools(ts)
	if ts.Running {
		if !isSubAgentLifecycleTool(ts.ToolName) {
			m.statusToolCount++
		}
		m.chatStartTool(ts)

		if m.reasoningActive {
			m.chatFinishReasoning()
		}
		startCmd := m.spinner.Start(util.FirstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
		spinnerCmd = combineCmds(spinnerCmd, startCmd)
	} else {

		ts.Elapsed = m.spinner.Elapsed()
		m.chatFinishTool(ts)
		m.spinner.Stop()
		spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	}
	m.chatListFollowOutput()
	return m, spinnerCmd

}

func (m Model) handleAgentToolBatchMsg(msg agentToolBatchMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {

	if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
		return m, nil
	}

	if len(msg.StatusMsgs) > 0 {
		last := msg.StatusMsgs[len(msg.StatusMsgs)-1]
		m.statusActivity = last.Activity
		m.statusToolName = last.ToolName
		m.statusToolArg = last.ToolArg
		spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
	}

	for _, ts := range msg.ToolMsgs {
		m.updateActiveMCPTools(ts.ToolStatusMsg)
		if ts.Running {
			if !isSubAgentLifecycleTool(ts.ToolName) {
				m.statusToolCount++
			}
			m.chatStartTool(ts.ToolStatusMsg)

			if m.reasoningActive {
				m.chatFinishReasoning()
			}
			startCmd := m.spinner.Start(util.FirstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts.ToolStatusMsg), toolDetail(ts.ToolStatusMsg))))
			spinnerCmd = combineCmds(spinnerCmd, startCmd)
		} else {

			ts.ToolStatusMsg.Elapsed = m.spinner.Elapsed()
			m.chatFinishTool(ts.ToolStatusMsg)
			m.spinner.Stop()
			spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

		}
	}
	m.chatListFollowOutput()
	return m, spinnerCmd

}

func (m Model) handleAgentToolStatusMsg(msg agentToolStatusMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {
	if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
		return m, nil
	}
	ts := msg.ToolStatusMsg
	m.updateActiveMCPTools(ts)
	if ts.Running {
		if !isSubAgentLifecycleTool(ts.ToolName) {
			m.statusToolCount++
		}
		m.chatStartTool(ts)

		if m.reasoningActive {
			m.chatFinishReasoning()
		}
		startCmd := m.spinner.Start(util.FirstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
		spinnerCmd = combineCmds(spinnerCmd, startCmd)
	} else {

		ts.Elapsed = m.spinner.Elapsed()
		m.chatFinishTool(ts)
		m.spinner.Stop()
		spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	}
	m.chatListFollowOutput()
	return m, spinnerCmd

}
