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
	return "Send a message or task to a running sub-agent or swarm teammate. " +
		"Messages are sent asynchronously — the tool returns immediately after delivery. " +
		"For swarm teammates, use teammate_results to collect the output when the teammate finishes. " +
		"Use to='*' to broadcast to all agents and teammates. " +
		"When sending to a swarm teammate (ID starts with 'tm-'), always provide team_id. " +
		"NOTE: For assigning tracked tasks to teammates, prefer swarm_task_create (which auto-delivers to the assignee's inbox). " +
		"Use send_message only for unstructured follow-ups, clarifications, or when no task tracking is needed."
}
func (t SendMessageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"to": {
			"type": "string",
			"description": "Recipient ID or '*' for broadcast. Swarm teammate IDs start with 'tm-' (e.g., 'tm-2'). Sub-agent IDs start with 'agent-'."
		},
		"message": {
			"type": "string",
			"description": "The message or task content to send"
		},
		"summary": {
			"type": "string",
			"description": "Optional short summary of the message"
		},
		"team_id": {
			"type": "string",
			"description": "REQUIRED for swarm teammates. The team ID (e.g., 'team-1'). Always include this when sending to teammates."
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
			return Result{Content: "No running agents or teammates to broadcast to.\n"}, nil
		}
		return Result{Content: fmt.Sprintf("Broadcast sent to %d recipient(s): %s\n", len(allSent), strings.Join(allSent, ", "))}, nil
	}

	if t.Manager != nil {
		if err := t.Manager.SendToAgent(args.To, msg); err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: fmt.Sprintf("Message sent to agent %s\n", args.To)}, nil
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
		return Result{Content: fmt.Sprintf("Broadcast sent to %d teammate(s): %s\n", len(sent), strings.Join(sent, ", "))}, nil
	}

	// Fire-and-forget: send message asynchronously. Teammate executes in its own goroutine.
	// Use teammate_results to collect the output later.
	if err := t.SwarmMgr.SendToTeammate(teamID, to, msg); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	return Result{Content: fmt.Sprintf("Message sent to teammate %s. Use teammate_results to collect the output when ready.\n", to)}, nil
}
