package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/util"
)

func (m Model) handleAgentTurnDoneMsg(msg agentTurnDoneMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {

	m.chatFinishReasoning()
	m.chatFinishAssistant(m.currentAssistantID())
	m.streamPrefixWritten = false
	m.reasoningActive = false

	if m.streamBuffer != nil {
		m.streamBuffer.Reset()
	}
	return m, spinnerCmd
}

func (m Model) handleAgentInterruptMsg(msg agentInterruptMsg) (tea.Model, tea.Cmd) {
	if msg.RunID != m.activeAgentRunID {
		return m, nil
	}
	m.chatWriteUser(nextChatID(), msg.Text)
	m.chatWriteSystem(nextSystemID(), m.t("interrupt.delivered"))
	m.chatListScrollToBottom()
	return m, nil
}

func (m Model) handleAgentRoundSummaryMsg(msg agentRoundSummaryMsg) (tea.Model, tea.Cmd) {
	if msg.RunID != m.activeAgentRunID {
		return m, nil
	}
	m.emitIMRoundSummary(msg.Text, msg.ToolCalls, msg.ToolSuccesses, msg.ToolFailures)
	return m, nil
}

func (m Model) handleVerifyProgressMsg(msg verifyProgressMsg) (tea.Model, tea.Cmd) {
	m.chatWriteSystem(nextSystemID(), msg.text)
	m.chatListFollowOutput()
	return m, nil
}

func (m Model) handleToolProgressMsg(msg toolProgressMsg) (tea.Model, tea.Cmd) {

	m.noteTurnActivity()
	if msg.toolID != "" {
		m.chatUpdateToolOutput(msg.toolID, msg.output)
	}
	m.chatListFollowOutput()
	return m, nil
}

func (m Model) handleVerifyResultMsg(msg verifyResultMsg) (tea.Model, tea.Cmd) {
	if msg.result.Passed {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("✅ [Verification passed: `%s`]", msg.result.Command))
	} else {
		output := msg.result.Output
		if len(output) > 500 {
			output = util.Truncate(output, 500) + "…"
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("❌ [Verification failed: `%s`]\n```\n%s\n```", msg.result.Command, output))
	}
	m.chatListFollowOutput()
	return m, nil
}
