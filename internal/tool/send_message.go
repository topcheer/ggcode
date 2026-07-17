package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
)

// SendMessageTool sends a message to one or all running sub-agents or swarm teammates.
type SendMessageTool struct {
	Manager  *subagent.Manager
	SwarmMgr *swarm.Manager
}

func (t SendMessageTool) Name() string { return "send_message" }
func (t SendMessageTool) Description() string {
	return "Send an async message or lightweight task to another worker. " +
		"For swarm teammates (tm-*): delivers to inbox; prefer swarm_task_create for tracked work. " +
		"For sub-agents (agent-*): unreliable for new work — spawn a new agent instead. " +
		"to='*' for best-effort broadcast (not tracked). Provide team_id when known."
}
func (t SendMessageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"to": {
			"type": "string",
			"description": "Recipient ID or '*' for best-effort broadcast. Swarm teammate IDs start with 'tm-' (e.g., 'tm-2') and receive task-like inbox work. Sub-agent IDs start with 'agent-' and only receive mailbox messages; do not rely on this for assigning new work."
		},
		"message": {
			"type": "string",
			"description": "The message or task content to send. For tracked teammate work, use swarm_task_create instead of send_message."
		},
		"summary": {
			"type": "string",
			"description": "Optional short summary of the message"
		},
		"team_id": {
			"type": "string",
			"description": "For swarm teammates, provide the team ID (e.g. 'team-1') when known. If omitted for a tm-* recipient, the tool searches all teams for that teammate."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"to",
		"message",
		"description"
	]
}`)
}
func (t SendMessageTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil && t.SwarmMgr == nil {
		return Result{IsError: true, Content: "send_message: no message routing available (no sub-agent or swarm manager)"}, nil
	}
	var args struct {
		To      string `json:"to"`
		Message string `json:"message"`
		Summary string `json:"summary"`
		TeamID  string `json:"team_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.To) == "" {
		return Result{IsError: true, Content: "to is required"}, nil
	}
	if strings.TrimSpace(args.Message) == "" {
		return Result{IsError: true, Content: "message is required"}, nil
	}

	// Route to swarm teammate if team_id is provided
	if args.TeamID != "" && t.SwarmMgr != nil {
		return t.sendToSwarm(args.To, args.Message, args.Summary, args.TeamID)
	}

	// Auto-route: if to looks like a teammate ID (tm-*) and SwarmMgr is available,
	// search all teams to find the teammate even without explicit team_id.
	if args.TeamID == "" && t.SwarmMgr != nil && strings.HasPrefix(args.To, "tm-") {
		for _, team := range t.SwarmMgr.ListTeams() {
			for _, tm := range team.Teammates {
				if tm.ID == args.To {
					return t.sendToSwarm(args.To, args.Message, args.Summary, team.ID)
				}
			}
		}
		return Result{IsError: true, Content: fmt.Sprintf("Teammate %q not found in any team", args.To)}, nil
	}

	msg := subagent.AgentMessage{
		From:    "main",
		Message: args.Message,
		Summary: args.Summary,
	}

	if args.To == "*" {
		var allSent []string
		// Broadcast to sub-agents
		if t.Manager != nil {
			sent := t.Manager.Broadcast(msg)
			allSent = append(allSent, sent...)
		}
		// Also broadcast to all swarm teammates
		if t.SwarmMgr != nil {
			for _, team := range t.SwarmMgr.ListTeams() {
				swarmMsg := swarm.MailMessage{
					From:    "leader",
					Content: args.Message,
					Summary: args.Summary,
					Type:    "message",
				}
				sent := t.SwarmMgr.BroadcastToTeam(team.ID, swarmMsg)
				allSent = append(allSent, sent...)
			}
		}
		if len(allSent) == 0 {
			return Result{Content: "No running sub-agents or active teammates to broadcast to. Broadcast is best-effort and not tracked.\n"}, nil
		}
		return Result{Content: fmt.Sprintf("Best-effort broadcast sent to %d recipient(s): %s. Use teammate_results only for teammate outputs; sub-agent mailbox messages may not produce a response.\n", len(allSent), strings.Join(allSent, ", "))}, nil
	}

	if t.Manager != nil {
		if err := t.Manager.SendToAgent(args.To, msg); err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: fmt.Sprintf("Mailbox message sent to sub-agent %s. This is best-effort only; spawned sub-agents are one-shot runs and may not consume mailbox messages or return a response. Use wait_agent for the original run result, or spawn_agent with a new full task for follow-up work.\n", args.To)}, nil
	}

	return Result{IsError: true, Content: fmt.Sprintf("Agent %q not found (no sub-agent manager configured)", args.To)}, nil
}

func (t SendMessageTool) sendToSwarm(to, message, summary, teamID string) (Result, error) {
	msg := swarm.MailMessage{
		From:    "leader",
		Content: message,
		Summary: summary,
		Type:    "task",
	}

	if to == "*" {
		sent := t.SwarmMgr.BroadcastToTeam(teamID, msg)
		if len(sent) == 0 {
			return Result{Content: "No active teammates to broadcast to.\n"}, nil
		}
		return Result{Content: fmt.Sprintf("Best-effort broadcast sent to %d teammate(s): %s. This is not tracked on the task board; use teammate_results for latest outputs.\n", len(sent), strings.Join(sent, ", "))}, nil
	}

	// Fire-and-forget: send message asynchronously. Teammate executes in its own goroutine.
	// Use teammate_results to collect the output later.
	if err := t.SwarmMgr.SendToTeammate(teamID, to, msg); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	return Result{Content: fmt.Sprintf("Task-like message sent to teammate %s. This is not tracked on the task board; use teammate_results to collect the latest output when ready. For tracked work, use swarm_task_create instead.\n", to)}, nil
}
