package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// LanChatTool lets the agent interact with the LAN Chat system:
// list participants, send messages, read history, and manage pending approvals.
type LanChatTool struct {
	Hub *lanchat.Hub
}

func (t LanChatTool) Name() string { return "lanchat" }

func (t LanChatTool) Description() string {
	return "Send and receive messages on the LAN Chat network connecting ggcode instances on the local network. " +
		"This is the PRIMARY tool for real-time collaboration with other ggcode users and their agents on the LAN — " +
		"use this FIRST (before a2a_remote or delegate) when you need to ask, notify, or check on another ggcode instance or user. " +
		"Triggers: user mentions another participant by name or nick (e.g. \"check what mdns is doing\", \"ask ggai about...\"), " +
		"messages prefixed with [LAN Chat from <nick>], or any reference to LAN participants.\n" +
		"Do NOT use send_message (that is for swarm teammates) or delegate/a2a_remote (those are for headless code-edit delegation to other workspaces, not for asking questions).\n" +
		"Nick format: nicks are composed as <name>_<role> (e.g. 'alice_frontend', 'mdns_developer'). " +
		"When a user says 'ask mdns', match the participant whose nick starts with 'mdns' — the full nick is 'mdns_developer' but you should use the node_id from list, not the nick, as the 'to' field.\n" +
		"Actions: 'list' to discover participants, their node IDs, roles, and teams (e.g. frontend, backend, devops / platform, mobile) so you can route tasks to the right agent; " +
		"'send' to message a participant (use to='*' to broadcast to ALL participants); 'broadcast' to send to ALL participants (use as_agent=true to broadcast as agent to all agents); " +
		"'history' to read recent messages; " +
		"'pending'/'approve'/'reject' to manage @agent approvals.\n" +
		"\nTeam awareness: each participant has a 'team' field. When the user mentions a team (e.g. 'ask the platform team'), use action='list' to find participants with a matching team, then collaborate with them.\n" +
		"\nTypical workflow to collaborate with another instance:\n" +
		"1. Call lanchat(action='list') to find the target's node_id, role, and team\n" +
		"2. Call lanchat(action='send', to=<node_id>, to_role='agent', as_agent=true, message='What are you working on?') to reach their agent\n" +
		"   Use to_role='human' to message the human user instead of their agent.\n" +
		"   Use to='*' to send the same message to ALL participants (group broadcast).\n" +
		"3. The response will appear as a [LAN Chat from <nick>] message in subsequent turns."
}

func (t LanChatTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["list", "send", "broadcast", "history", "pending", "approve", "reject"],
				"description": "list=discover participants and their node_id; send=send a DM; broadcast=send to all participants (use as_agent=true for agent broadcast); history=recent messages; pending/list @agent approvals; approve/reject a pending message"
			},
			"message": {
				"type": "string",
				"description": "Message text to send (required for 'send')."
			},
			"to": {
				"type": "string",
				"description": "Recipient node_id for direct message. Use '*' to broadcast to ALL participants. Omit for default broadcast. Find node_id via action='list' first."
			},
			"to_role": {
				"type": "string",
				"enum": ["human", "agent"],
				"description": "Direct messages only. 'agent' = deliver to recipient's agent (triggers their approval + agent loop). 'human' = show in recipient's chat panel for the human to read. Default: 'human'."
			},
			"as_agent": {
				"type": "boolean",
				"description": "Sender identity. true = send as agent (fromRole=agent). false = send as the local human user. Default: false."
			},
			"message_id": {
				"type": "string",
				"description": "ID of the pending message to approve or reject."
			},
			"reason": {
				"type": "string",
				"description": "Optional rejection reason."
			},
			"limit": {
				"type": "integer",
				"description": "Max messages for 'history'. Default: 20."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["action", "description"]
	}`)
}

func (t LanChatTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Hub == nil {
		return Result{IsError: true, Content: "lanchat: hub not available (LAN discovery may be disabled)"}, nil
	}

	var args struct {
		Action    string `json:"action"`
		Message   string `json:"message"`
		To        string `json:"to"`
		AsAgent   bool   `json:"as_agent"`
		ToRole    string `json:"to_role"`
		MessageID string `json:"message_id"`
		Reason    string `json:"reason"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	switch args.Action {
	case "list":
		return t.doList(), nil
	case "send":
		return t.doSend(ctx, args.Message, args.To, args.AsAgent, args.ToRole)
	case "broadcast":
		return t.doBroadcast(ctx, args.Message, args.AsAgent)
	case "history":
		return t.doHistory(args.Limit), nil
	case "pending":
		return t.doPending(), nil
	case "approve":
		return t.doApprove(ctx, args.MessageID)
	case "reject":
		return t.doReject(args.MessageID, args.Reason)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action: %s (valid: list, send, history, pending, approve, reject)", args.Action)}, nil
	}
}

func (t LanChatTool) doList() Result {
	participants := t.Hub.Participants()
	selfNodeID := t.Hub.NodeID()

	type peerInfo struct {
		NodeID    string `json:"node_id"`
		HumanNick string `json:"human_nick"`
		AgentNick string `json:"agent_nick"`
		Role      string `json:"role"`
		Team      string `json:"team"`
		Mode      string `json:"mode"`
		Online    bool   `json:"online"`
		LastSeen  string `json:"last_seen"`
		Self      bool   `json:"self"`
	}

	var peers []peerInfo
	for _, p := range participants {
		lastSeen := "never"
		if p.LastSeen > 0 {
			lastSeen = time.Since(time.Unix(p.LastSeen, 0)).Round(time.Second).String() + " ago"
		}
		peers = append(peers, peerInfo{
			NodeID:    p.NodeID,
			HumanNick: p.HumanNick,
			AgentNick: p.AgentNick,
			Role:      p.Role,
			Team:      p.Team,
			Mode:      p.Mode,
			Online:    p.Online,
			LastSeen:  lastSeen,
			Self:      p.NodeID == selfNodeID,
		})
	}

	if len(peers) == 0 {
		return Result{Content: "No LAN Chat participants discovered.\n"}
	}

	out, _ := json.MarshalIndent(peers, "", "  ")
	return Result{Content: fmt.Sprintf("Participants (%d):\n%s\n", len(peers), string(out))}
}

func (t LanChatTool) doSend(ctx context.Context, content, toNodeID string, asAgent bool, toRole string) (Result, error) {
	if content == "" {
		return Result{IsError: true, Content: "message content is required for send"}, nil
	}

	// '*' means broadcast to all participants
	if toNodeID == "*" {
		toNodeID = ""
	}

	// Default toRole to human for direct messages
	if toRole == "" {
		toRole = lanchat.RoleHuman
	}

	var err error
	if toNodeID != "" {
		// Direct message — choose sender method by asAgent
		if asAgent {
			err = t.Hub.SendAsAgent(ctx, toNodeID, toRole, content)
		} else {
			err = t.Hub.SendDirect(ctx, toNodeID, toRole, content, nil)
		}
	} else {
		// Broadcast — toRole is meaningless for broadcasts
		if asAgent {
			err = t.Hub.SendAsAgent(ctx, "", "", content)
		} else {
			err = t.Hub.SendBroadcast(ctx, content, nil)
		}
	}

	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to send: %v", err)}, nil
	}

	target := "everyone (broadcast)"
	if toNodeID != "" {
		target = toNodeID
	}
	role := "human"
	if asAgent {
		role = "agent"
	}
	return Result{Content: fmt.Sprintf("Message sent to %s as %s.\n", target, role)}, nil
}

func (t LanChatTool) doBroadcast(ctx context.Context, content string, asAgent bool) (Result, error) {
	if content == "" {
		return Result{IsError: true, Content: "message content is required for broadcast"}, nil
	}
	var err error
	if asAgent {
		err = t.Hub.BroadcastAsAgent(ctx, content)
	} else {
		err = t.Hub.SendBroadcast(ctx, content, nil)
	}
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to broadcast: %v", err)}, nil
	}
	role := "human"
	if asAgent {
		role = "agent"
	}
	return Result{Content: fmt.Sprintf("Broadcast sent to all participants as %s.\n", role)}, nil
}

func (t LanChatTool) doHistory(limit int) Result {
	if limit <= 0 {
		limit = 20
	}

	messages := t.Hub.Messages()
	if len(messages) == 0 {
		return Result{Content: "No messages in history.\n"}
	}

	// Get last N messages
	start := len(messages) - limit
	if start < 0 {
		start = 0
	}
	recent := messages[start:]

	type msgInfo struct {
		From   string `json:"from"`
		Role   string `json:"role"`
		To     string `json:"to"`
		Body   string `json:"content"`
		Time   string `json:"time"`
		Direct bool   `json:"direct"`
	}

	var msgs []msgInfo
	for _, m := range recent {
		target := "all"
		direct := false
		if m.ToNodeID != "" {
			target = m.ToNodeID
			direct = true
		}
		msgs = append(msgs, msgInfo{
			From:   m.FromNick,
			Role:   m.FromRole,
			To:     target,
			Body:   m.Content,
			Time:   time.UnixMilli(m.Timestamp).Format("15:04:05"),
			Direct: direct,
		})
	}

	out, _ := json.MarshalIndent(msgs, "", "  ")
	return Result{Content: fmt.Sprintf("History (%d messages):\n%s\n", len(msgs), string(out))}
}

func (t LanChatTool) doPending() Result {
	pending := t.Hub.PendingApprovals()
	if len(pending) == 0 {
		return Result{Content: "No pending @agent approvals.\n"}
	}

	type pendingInfo struct {
		ID       string `json:"message_id"`
		From     string `json:"from"`
		FromRole string `json:"from_role"`
		Content  string `json:"content"`
		Received string `json:"received"`
	}

	var items []pendingInfo
	for _, p := range pending {
		items = append(items, pendingInfo{
			ID:       p.Message.ID,
			From:     p.Message.FromNick,
			FromRole: p.Message.FromRole,
			Content:  p.Message.Content,
			Received: time.Since(p.Received).Round(time.Second).String() + " ago",
		})
	}

	out, _ := json.MarshalIndent(items, "", "  ")
	return Result{Content: fmt.Sprintf("Pending approvals (%d):\n%s\n", len(items), string(out))}
}

func (t LanChatTool) doApprove(ctx context.Context, messageID string) (Result, error) {
	if messageID == "" {
		return Result{IsError: true, Content: "message_id is required for approve"}, nil
	}

	msg, err := t.Hub.ApproveMessage(messageID)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to approve: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Approved message from %s: %s\n", msg.FromNick, msg.Content)}, nil
}

func (t LanChatTool) doReject(messageID, reason string) (Result, error) {
	if messageID == "" {
		return Result{IsError: true, Content: "message_id is required for reject"}, nil
	}

	if err := t.Hub.RejectMessage(messageID, reason); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to reject: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Rejected message %s", messageID)}, nil
}
