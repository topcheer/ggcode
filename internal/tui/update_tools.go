package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/util"
)

// handleToolStatusMsg handles the corresponding message case.
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
		// Collapse reasoning when the first tool event arrives.
		if m.reasoningActive {
			m.chatFinishReasoning()
		}
		startCmd := m.spinner.Start(util.FirstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
		spinnerCmd = combineCmds(spinnerCmd, startCmd)
	} else {
		m.chatFinishTool(ts)
		ts.Elapsed = m.spinner.Elapsed()
		m.spinner.Stop()
		spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
		// Do NOT reset streamPrefixWritten here. Resetting causes the
		// next text/reasoning flush to create a NEW assistant item,
		// which duplicates reasoning (fullReasoningBuf accumulates)
		// and fragments text (streamBuffer was emptied by tool start).
		// streamPrefixWritten is properly reset by chatFinishReasoning()
		// at the end of each LLM turn (agentReasoningDoneMsg).
	}
	m.chatListScrollToBottom()
	return m, spinnerCmd

}

// handleAgentToolBatchMsg handles the corresponding message case.
func (m Model) handleAgentToolBatchMsg(msg agentToolBatchMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {
	// Batched tool events — process all accumulated status + tool updates
	// in a single Update cycle instead of one message per event.
	// This prevents event-loop saturation from burst tool call/results.
	if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
		return m, nil
	}
	// Apply the last status message (only the latest matters for the status bar).
	if len(msg.StatusMsgs) > 0 {
		last := msg.StatusMsgs[len(msg.StatusMsgs)-1]
		m.statusActivity = last.Activity
		m.statusToolName = last.ToolName
		m.statusToolArg = last.ToolArg
		spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
	}
	// Apply all tool status updates sequentially.
	for _, ts := range msg.ToolMsgs {
		m.updateActiveMCPTools(ts.ToolStatusMsg)
		if ts.Running {
			if !isSubAgentLifecycleTool(ts.ToolName) {
				m.statusToolCount++
			}
			m.chatStartTool(ts.ToolStatusMsg)
			// Collapse reasoning when the first tool event arrives.
			if m.reasoningActive {
				m.chatFinishReasoning()
			}
			startCmd := m.spinner.Start(util.FirstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts.ToolStatusMsg), toolDetail(ts.ToolStatusMsg))))
			spinnerCmd = combineCmds(spinnerCmd, startCmd)
		} else {
			m.chatFinishTool(ts.ToolStatusMsg)
			ts.ToolStatusMsg.Elapsed = m.spinner.Elapsed()
			m.spinner.Stop()
			spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
			// Do NOT reset streamPrefixWritten here — that would fragment the
			// assistant text across multiple bubbles. The assistant item should
			// stay streaming so text from later turns continues on the same item.
		}
	}
	m.chatListScrollToBottom()
	return m, spinnerCmd

}

// handleAgentToolStatusMsg handles the corresponding message case.
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
		// Collapse reasoning when the first tool event arrives.
		if m.reasoningActive {
			m.chatFinishReasoning()
		}
		startCmd := m.spinner.Start(util.FirstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
		spinnerCmd = combineCmds(spinnerCmd, startCmd)
	} else {
		m.chatFinishTool(ts)
		ts.Elapsed = m.spinner.Elapsed()
		m.spinner.Stop()
		spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
		// Do NOT reset streamPrefixWritten (see handleToolStatusMsg).
	}
	m.chatListScrollToBottom()
	return m, spinnerCmd

}
