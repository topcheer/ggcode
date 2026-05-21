package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
)

// handleSubAgentUpdateMsg handles the corresponding message case.
func (m Model) handleSubAgentUpdateMsg(msg subAgentUpdateMsg) (Model, tea.Cmd) {
	m.subAgentFollow.markDirty(msg.AgentID)

	if m.subAgentFollow.isActive() {
		// Follow panel open: only rebuild the active agent's view.
		// Strip is refreshed less frequently (on spawn/complete via other paths).
		if msg.AgentID == m.subAgentFollow.activeID && m.subAgentFollow.shouldRebuild(m.subAgentFollow.activeID) {
			m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
			m.chatListScrollToBottom()
		} else if msg.AgentID == m.subAgentFollow.activeID {
			// Throttled — schedule delayed retry to ensure eventual render.
			return m, tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
				return subAgentFollowRefreshMsg{}
			})
		}
		// Also mark strip dirty for non-active slots (status changes).
		m.subAgentFollow.markStripDirty()
	} else {
		// No follow panel — defer strip refresh to the throttled path.
		// This avoids calling refreshSlots/refreshSwarmSlots on every streaming token.
		m.subAgentFollow.markStripDirty()
		if !m.subAgentFollow.refreshStripIfNeeded(m.subAgentMgr, m.swarmMgr) {
			// Still throttled — schedule a delayed tick to pick it up.
			return m, tea.Tick(stripRefreshInterval, func(t time.Time) tea.Msg {
				return subAgentFollowRefreshMsg{}
			})
		}
	}

	if m.subAgentFollow.isActive() && m.subAgentFollow.currentSlotIndex() == -1 {
		m.subAgentFollow.deactivate()
	}
	if m.subAgentFollow.hasTerminalSlots() {
		return m, tea.Tick(15*time.Second, func(t time.Time) tea.Msg {
			return followGraceTickMsg{}
		})
	}
	return m, nil

}

// handleSubAgentDoneMsg handles the corresponding message case.
func (m Model) handleSubAgentDoneMsg(msg subAgentDoneMsg) (Model, tea.Cmd) {
	// A sub-agent or swarm teammate finished its task.
	// Show a human-readable system message and wake the main agent.
	m.chatWriteSystem(nextSystemID(), m.formatSubAgentDoneNotice(msg))
	m.chatListScrollToBottom()

	// Force immediate strip refresh on completion (status changed).
	m.subAgentFollow.refreshSlots(m.subAgentMgr)
	m.subAgentFollow.refreshSwarmSlots(m.swarmMgr)

	// Build prompt for the main agent.
	var agentHint string
	if msg.IsError {
		agentHint = fmt.Sprintf("%s failed with an error. Do NOT spawn sub-agents. Investigate or retry directly.", msg.AgentName)
	} else {
		agentHint = fmt.Sprintf("%s has completed its task. Do NOT spawn sub-agents. Use wait_agent to review the result, then continue your work directly.", msg.AgentName)
	}

	if !m.loading {
		// Agent is idle — start a new loop to process the notification.
		return m, m.submitText(agentHint, true)
	}
	// Agent is busy — queue for processing after current run.
	m.queuePendingSubmissionHidden(agentHint)
	return m, nil

}

// handleSubAgentFollowRefreshMsg handles the corresponding message case.
func (m Model) handleSubAgentFollowRefreshMsg(msg subAgentFollowRefreshMsg) (Model, tea.Cmd) {
	// Delayed rebuild after throttle window (for follow panel)
	if m.subAgentFollow.isActive() && m.subAgentFollow.shouldRebuild(m.subAgentFollow.activeID) {
		m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
	} else if m.subAgentFollow.isActive() && m.subAgentFollow.dirty[m.subAgentFollow.activeID] {
		// Still dirty but throttled — reschedule
		return m, tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
			return subAgentFollowRefreshMsg{}
		})
	}

	// Also handle deferred strip refresh
	if m.subAgentFollow.stripDirty {
		if m.subAgentFollow.refreshStripIfNeeded(m.subAgentMgr, m.swarmMgr) {
			// Refreshed now, but if still dirty, schedule next check
			if m.subAgentFollow.stripDirty {
				return m, tea.Tick(stripRefreshInterval, func(t time.Time) tea.Msg {
					return subAgentFollowRefreshMsg{}
				})
			}
		} else {
			// Still throttled — reschedule
			return m, tea.Tick(stripRefreshInterval, func(t time.Time) tea.Msg {
				return subAgentFollowRefreshMsg{}
			})
		}
	}

	return m, nil

}

// handleFollowGraceTickMsg handles the corresponding message case.
func (m Model) handleFollowGraceTickMsg(msg followGraceTickMsg) (Model, tea.Cmd) {
	// Re-evaluate grace period: refresh slots and remove expired terminal ones
	m.subAgentFollow.refreshSlots(m.subAgentMgr)
	m.subAgentFollow.refreshSwarmSlots(m.swarmMgr)
	m.subAgentFollow.cleanup(m.subAgentMgr, m.swarmMgr)

	// Auto-deactivate if the followed agent was removed
	if m.subAgentFollow.isActive() && m.subAgentFollow.currentSlotIndex() == -1 {
		m.subAgentFollow.deactivate()
	}

	// Continue ticking only while terminal slots still exist
	if m.subAgentFollow.hasTerminalSlots() {
		return m, tea.Tick(15*time.Second, func(t time.Time) tea.Msg {
			return followGraceTickMsg{}
		})
	}
	return m, nil

}
