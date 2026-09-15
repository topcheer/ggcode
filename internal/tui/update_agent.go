package tui

// #2422 knife A (agent stream/status/progress/verification): extracted
// verbatim from Model.Update's inline case bodies - zero behavior change.

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/util"
)

func (m Model) handleAgentTurnDoneMsg(msg agentTurnDoneMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
	// LLM turn boundary: collapse reasoning, finalize the assistant item,
	// and reset stream state so the next LLM turn creates a fresh
	// assistant item with its own reasoning/text.
	m.chatFinishReasoning()
	m.chatFinishAssistant(m.currentAssistantID())
	m.streamPrefixWritten = false
	m.reasoningActive = false
	// CRITICAL: reset streamBuffer so next turn's text doesn't accumulate
	// on top of the previous turn's content.
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

// removed: handleStatusMsg/handleAgentStatusMsg live in update_status.go
// (#2423 slice 1). Anchor retained for history; safe to delete.
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
	// Update the running tool's output in-place for a streaming effect.
	m.noteTurnActivity() // streaming tool output is turn activity (#375)
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

// removed: handleStatusMsg / handleAgentStatusMsg live in update_status.go
// (#2423 slice 1, merged to main first).
