package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
)

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
	return m, nil
}

func (m Model) handleSubAgentTunnelStreamTextMsg(msg subAgentTunnelStreamTextMsg) (Model, tea.Cmd) {
	if msg.AgentID != "" && msg.Text != "" {
		m.pushSubAgentTunnelStreamText(msg.AgentID, msg.Text)
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
	if swarmEventAffectsSidebar(msg.Event) {
		m.refreshSwarmTeamsSnapshot()
	}
	return m, nil
}

// swarmEventAffectsSidebar reports whether a swarm event changes what the
// sidebar Swarm Teams section shows (#5851). High-frequency stream events
// (teammate_text / teammate_reasoning / teammate_tool_call /
// teammate_tool_result) are deliberately excluded: they never change the
// roster or member status, and re-snapshotting on every chunk would churn
// the UI while teammates are actively streaming.
func swarmEventAffectsSidebar(ev swarm.Event) bool {
	switch ev.Type {
	case "team_created", "team_restored", "team_deleted", "team_board_updated",
		"teammate_spawned", "teammate_shutdown", "teammate_working",
		"teammate_idle", "teammate_error":
		return true
	}
	return false
}

// refreshSwarmTeamsSnapshot re-caches the active team snapshots for the
// sidebar panel. ListTeams takes a read lock and copies a handful of small
// snapshots, so this is cheap enough to run per lifecycle event.
func (m *Model) refreshSwarmTeamsSnapshot() {
	if m.swarmMgr == nil {
		return
	}
	m.swarmTeams = m.swarmMgr.ListTeams()
}

func (m Model) handleSubAgentDoneMsg(msg subAgentDoneMsg) (Model, tea.Cmd) {

	m.chatWriteSystem(nextSystemID(), m.formatSubAgentDoneNotice(msg))
	m.chatListFollowOutput()

	var agentHint string
	if msg.IsError {
		agentHint = fmt.Sprintf("%s failed with an error. Do NOT start another agent run yet. Investigate or retry directly.", msg.AgentName)
	} else {
		agentHint = fmt.Sprintf("%s has completed its task. Do NOT start another agent run yet. Use wait_agent to review the result, then continue your work directly.", msg.AgentName)
	}

	if !m.loading {

		if m.subAgentsCanceling {
			debug.Log("cancel", "handleSubAgentDoneMsg: suppressing new agent run (subAgentsCanceling=true) agent=%s", msg.AgentID)
			return m, nil
		}

		if msg.Kind != "teammate" && m.subAgentMgr != nil {
			// #5700 idle-report wake: drain reports that arrived after the
			// last run ended and wake the parent exactly once with a
			// batched digest. Reports already pending at run end stay
			// queued for the next run's in-run delivery (finished-parent
			// silence); the drain is consume-once, so N concurrent
			// completions coalesce into this single turn.
			if fresh := m.subAgentMgr.DrainIdleReportsSince(m.runEndedAt); len(fresh) > 0 {
				debug.Log("agent", "idle-report wake: %d fresh report(s), submitting one digest turn", len(fresh))
				return m, m.submitText(formatIdleReportDigest(fresh), true)
			}
			return m, nil
		}

		return m, m.submitText(agentHint, true)
	}

	return m, nil

}

// formatIdleReportDigest renders drained idle reports as the single
// synthetic user turn that wakes an idle parent (#5700). Batching keeps
// the wake cost at one model turn regardless of how many children
// finished while the parent was idle.
func formatIdleReportDigest(reports []subagent.IdleReport) string {
	var b strings.Builder
	if len(reports) == 1 {
		b.WriteString("A sub-agent run finished while you were idle:\n")
	} else {
		b.WriteString(fmt.Sprintf("%d sub-agent runs finished while you were idle:\n", len(reports)))
	}
	for _, r := range reports {
		name := r.Name
		if name == "" {
			name = r.ID
		}
		status := "completed"
		if r.Status != subagent.StatusCompleted {
			status = strings.ToUpper(string(r.Status))
		}
		line := fmt.Sprintf("- %s (%s): %s", r.ID, name, status)
		if s := strings.TrimSpace(r.Summary); s != "" {
			line += " - " + s
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("When a result is relevant to your current task, harvest the full output with wait_agent or list_agents; otherwise continue what you were doing. Do not re-spawn.")
	return b.String()
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
