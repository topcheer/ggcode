package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
)

func scheduleFollowGraceTick(hasTerminal bool) tea.Cmd {
	if !hasTerminal {
		return nil
	}
	return tea.Tick(15*time.Second, func(t time.Time) tea.Msg {
		return followGraceTickMsg{}
	})
}

func (m Model) handleSubAgentUpdateMsg(msg subAgentUpdateMsg) (Model, tea.Cmd) {
	if msg.AgentID != "" && m.subAgentMgr != nil {
		if snap, ok := m.subAgentMgr.Snapshot(msg.AgentID); ok {
			sa := &subagent.SubAgent{
				ID:          snap.ID,
				Name:        snap.Name,
				Task:        snap.Task,
				Status:      snap.Status,
				CurrentTool: snap.CurrentTool,
				Result:      snap.Result,
			}
			if snap.Error != "" {
				sa.Error = fmt.Errorf("%s", snap.Error)
			}
			m.pushSubAgentTunnelEvent(sa)
		}
	}

	m.subAgentFollow.markDirty(msg.AgentID)

	if m.subAgentFollow.isActive() {

		if msg.AgentID == m.subAgentFollow.activeID && m.subAgentFollow.shouldRebuild(m.subAgentFollow.activeID) {
			m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
			m.chatListFollowOutput()
		} else if msg.AgentID == m.subAgentFollow.activeID {

			return m, tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
				return subAgentFollowRefreshMsg{}
			})
		}

		m.subAgentFollow.markStripDirty()
	} else {

		m.subAgentFollow.markStripDirty()
		if !m.subAgentFollow.refreshStripIfNeeded(m.subAgentMgr, m.swarmMgr) {

			return m, tea.Tick(stripRefreshInterval, func(t time.Time) tea.Msg {
				return subAgentFollowRefreshMsg{}
			})
		}
	}

	if m.subAgentFollow.isActive() && m.subAgentFollow.currentSlotIndex() == -1 {
		m.subAgentFollow.deactivate()
	}
	return m, scheduleFollowGraceTick(m.subAgentFollow.hasTerminalSlots())

}

func (m Model) handleSubAgentTunnelStreamTextMsg(msg subAgentTunnelStreamTextMsg) (Model, tea.Cmd) {
	if msg.AgentID != "" && msg.Text != "" {
		m.pushSubAgentTunnelStreamText(msg.AgentID, msg.Text)
		m.extPaneWriteText(msg.AgentID, msg.Text)
	}
	return m, nil
}

func (m Model) handleSubAgentTunnelReasoningMsg(msg subAgentTunnelReasoningMsg) (Model, tea.Cmd) {
	if msg.AgentID != "" && msg.Text != "" {
		m.pushSubAgentTunnelReasoning(msg.AgentID, msg.Text)
	}
	return m, nil
}

func (m Model) handleSubAgentTunnelToolCallMsg(msg subAgentTunnelToolCallMsg) (Model, tea.Cmd) {
	m.extPaneWriteToolCall(msg.AgentID, msg.ToolName, msg.Detail)
	if msg.AgentID != "" {
		displayName := msg.DisplayName
		if displayName == "" {
			displayName = toolCallDisplayName(msg.ToolName, msg.Args)
		}
		detail := msg.Detail
		if detail == "" {
			detail = describeTool(LangEnglish, msg.ToolName, msg.Args).Detail
		}
		m.pushSubAgentTunnelToolCall(
			msg.AgentID,
			msg.ToolID,
			msg.ToolName,
			displayName,
			msg.Args,
			detail,
		)
	}
	return m, nil
}

func (m Model) handleSubAgentTunnelToolResultMsg(msg subAgentTunnelToolResultMsg) (Model, tea.Cmd) {
	m.extPaneWriteToolResult(msg.AgentID, msg.ToolName, msg.Result, msg.IsError)
	if msg.AgentID != "" {
		m.pushSubAgentTunnelToolResult(
			msg.AgentID,
			msg.ToolID,
			msg.ToolName,
			msg.DisplayName,
			msg.Detail,
			msg.Result,
			msg.IsError,
		)
	}
	return m, nil
}

func (m Model) handleSwarmTunnelEventMsg(msg swarmTunnelEventMsg) (Model, tea.Cmd) {
	m.pushSwarmTunnelEvent(msg.Event)
	m.extPaneHandleSwarmEvent(msg.Event)
	return m, nil
}

func (m Model) handleSubAgentDoneMsg(msg subAgentDoneMsg) (Model, tea.Cmd) {

	m.chatWriteSystem(nextSystemID(), m.formatSubAgentDoneNotice(msg))
	m.chatListFollowOutput()
	m.extPaneHandleDone(msg)

	m.subAgentFollow.refreshSlots(m.subAgentMgr)
	m.subAgentFollow.refreshSwarmSlots(m.swarmMgr)
	graceCmd := scheduleFollowGraceTick(m.subAgentFollow.hasTerminalSlots())

	var agentHint string
	if msg.IsError {
		agentHint = fmt.Sprintf("%s failed with an error. Do NOT start another agent run yet. Investigate or retry directly.", msg.AgentName)
	} else {
		agentHint = fmt.Sprintf("%s has completed its task. Do NOT start another agent run yet. Use wait_agent to review the result, then continue your work directly.", msg.AgentName)
	}

	if !m.loading {

		if m.subAgentsCanceling {
			debug.Log("cancel", "handleSubAgentDoneMsg: suppressing new agent run (subAgentsCanceling=true) agent=%s", msg.AgentID)
			return m, graceCmd
		}

		return m, tea.Batch(graceCmd, m.submitText(agentHint, true))
	}

	return m, graceCmd

}

func (m Model) handleSubAgentFollowRefreshMsg(msg subAgentFollowRefreshMsg) (Model, tea.Cmd) {

	if m.subAgentFollow.isActive() && m.subAgentFollow.shouldRebuild(m.subAgentFollow.activeID) {
		m.subAgentFollow.rebuildActiveView(m.subAgentMgr, m.swarmMgr, m.chatStyles)
	} else if m.subAgentFollow.isActive() && m.subAgentFollow.dirty[m.subAgentFollow.activeID] {

		return m, tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
			return subAgentFollowRefreshMsg{}
		})
	}

	if m.subAgentFollow.stripDirty {
		if m.subAgentFollow.refreshStripIfNeeded(m.subAgentMgr, m.swarmMgr) {

			if m.subAgentFollow.stripDirty {
				return m, tea.Tick(stripRefreshInterval, func(t time.Time) tea.Msg {
					return subAgentFollowRefreshMsg{}
				})
			}
		} else {

			return m, tea.Tick(stripRefreshInterval, func(t time.Time) tea.Msg {
				return subAgentFollowRefreshMsg{}
			})
		}
	}

	return m, nil

}

func (m Model) handleFollowGraceTickMsg(msg followGraceTickMsg) (Model, tea.Cmd) {

	m.subAgentFollow.refreshSlots(m.subAgentMgr)
	m.subAgentFollow.refreshSwarmSlots(m.swarmMgr)
	m.subAgentFollow.cleanup(m.subAgentMgr, m.swarmMgr)

	if m.subAgentFollow.isActive() && m.subAgentFollow.currentSlotIndex() == -1 {
		m.subAgentFollow.deactivate()
	}

	return m, scheduleFollowGraceTick(m.subAgentFollow.hasTerminalSlots())

}

func (m Model) extPaneResolveName(agentID string) (name, kind string) {
	for _, slot := range m.subAgentFollow.slots {
		if slot.ID == agentID {
			k := "subagent"
			if slot.Kind == followSlotTeammate {
				k = "teammate"
			}
			return slot.Name, k
		}
	}
	return agentID, "subagent"
}

func (m Model) extPaneWriteText(agentID, text string) {
	if !m.extPaneMgr.Available() {
		return
	}
	name, kind := m.extPaneResolveName(agentID)
	m.extPaneMgr.EnsurePane(agentID, name, kind)
	m.extPaneMgr.WriteText(agentID, text)
}

func (m Model) extPaneWriteToolCall(agentID, toolName, detail string) {
	if !m.extPaneMgr.Available() {
		return
	}
	name, kind := m.extPaneResolveName(agentID)
	m.extPaneMgr.EnsurePane(agentID, name, kind)
	m.extPaneMgr.WriteToolCall(agentID, toolName, detail)
}

func (m Model) extPaneWriteToolResult(agentID, toolName, result string, isError bool) {
	if !m.extPaneMgr.Available() {
		return
	}
	m.extPaneMgr.WriteToolResult(agentID, toolName, result, isError)
}

func (m Model) extPaneHandleSwarmEvent(ev swarm.Event) {
	if !m.extPaneMgr.Available() {
		return
	}

	teammateID := ev.TeammateID
	name := ev.TeammateName
	if name == "" {
		name, _ = m.extPaneResolveName(teammateID)
	}
	if teammateID == "" {
		return
	}
	switch ev.Type {
	case "teammate_spawned", "teammate_working":
		m.extPaneMgr.EnsurePane(teammateID, name, "teammate")
		m.extPaneMgr.UpdateStatus(teammateID, name, "teammate", ev.Type)
	case "teammate_text":
		m.extPaneMgr.EnsurePane(teammateID, name, "teammate")
		m.extPaneMgr.WriteText(teammateID, ev.Result)
	case "teammate_tool_call":
		m.extPaneMgr.EnsurePane(teammateID, name, "teammate")
		m.extPaneMgr.WriteToolCall(teammateID, ev.CurrentTool, ev.ToolArgs)
	case "teammate_tool_result":

		m.extPaneMgr.WriteToolResult(teammateID, ev.CurrentTool, ev.ToolArgs, ev.IsError)
	case "teammate_done":
		m.extPaneMgr.HandleDone(teammateID, name, false)
	case "teammate_idle":
		m.extPaneMgr.UpdateStatus(teammateID, name, "teammate", "idle")
	case "teammate_shutdown":
		m.extPaneMgr.HandleDone(teammateID, name, false)
	case "teammate_error":
		m.extPaneMgr.HandleDone(teammateID, name, true)
	}
}

func (m Model) extPaneHandleDone(msg subAgentDoneMsg) {
	if !m.extPaneMgr.Available() {
		return
	}
	name, _ := m.extPaneResolveName(msg.AgentID)
	m.extPaneMgr.HandleDone(msg.AgentID, name, msg.IsError)
}

func (m *Model) handleSubAgentSystemMsg(msg subAgentSystemMsg) (Model, tea.Cmd) {
	if m.saSysItemIDs == nil {
		m.saSysItemIDs = make(map[string]string)
	}
	itemID := m.saSysItemIDs[msg.AgentID]

	if strings.Contains(msg.Text, "[Retry 1/") || itemID == "" {
		itemID = nextSystemID()
		m.saSysItemIDs[msg.AgentID] = itemID
		m.chatWriteSystem(itemID, msg.Text)
	} else {

		if item := m.chatList.FindByID(itemID); item != nil {
			if sys, ok := item.(*chat.SystemItem); ok {
				sys.SetText(msg.Text)
				m.chatListFollowOutput()
			} else {
				m.chatWriteSystem(itemID, msg.Text)
			}
		} else {
			m.chatWriteSystem(itemID, msg.Text)
		}
	}
	return *m, nil
}
