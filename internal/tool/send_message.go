package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	return "Send a message to a running sub-agent or swarm teammate. " +
		"Use to='*' to broadcast to all agents and teammates. " +
		"IMPORTANT: When sending to a swarm teammate (ID starts with 'tm-'), always provide team_id. " +
		"If team_id is omitted and to looks like a teammate ID, the system will try to find it automatically."
}
func (t SendMessageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"to": {"type": "string", "description": "Recipient ID or '*' for broadcast. Swarm teammate IDs start with 'tm-' (e.g., 'tm-2'). Sub-agent IDs start with 'agent-'."},
				"message": {"type": "string", "description": "The message or task content to send"},
				"summary": {"type": "string", "description": "Optional short summary of the message"},
				"team_id": {"type": "string", "description": "REQUIRED for swarm teammates. The team ID (e.g., 'team-1'). Always include this when sending to teammates."}
			},
			"required": ["to", "message"]
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

	// For targeted messages, block until the teammate finishes and return its result.
	replyCh := make(chan swarm.TaskResult, 1)
	msg.ReplyTo = replyCh

	if err := t.SwarmMgr.SendToTeammate(teamID, to, msg); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	// Wait up to 5 minutes for the teammate to finish.
	select {
	case result := <-replyCh:
		if result.Error != nil {
			return Result{IsError: true, Content: fmt.Sprintf("Teammate %s error: %v\n", to, result.Error)}, nil
		}
		return Result{Content: result.Output}, nil
	case <-time.After(5 * time.Minute):
		return Result{Content: fmt.Sprintf("Message sent to teammate %s (still executing after 5 min timeout)\n", to)}, nil
	}
}
