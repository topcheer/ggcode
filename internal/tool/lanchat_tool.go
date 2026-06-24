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
	return "Interact with the LAN Chat system to communicate with other ggcode users and agents on the local network. " +
		"Actions: 'list' participants, 'send' a message (broadcast or direct), 'history' of recent messages, " +
		"'pending' message approvals, 'approve' or 'reject' a pending @agent message."
}

func (t LanChatTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["list", "send", "history", "pending", "approve", "reject"],
				"description": "Action to perform: list=show online participants, send=send a message, history=recent messages, pending=pending @agent approvals, approve/reject=manage pending messages"
			},
			"message": {
				"type": "string",
				"description": "Message text to send (for 'send' action). Required for send."
			},
			"to": {
				"type": "string",
				"description": "Recipient node ID for direct messages (for 'send' action). Omit for broadcast to all."
			},
			"as_agent": {
				"type": "boolean",
				"description": "If true, send as the agent role instead of human (for 'send' action). Default: false."
			},
			"to_role": {
				"type": "string",
				"enum": ["human", "agent"],
				"description": "Direct messages only: target the recipient's human user or their agent. Messages to 'agent' trigger the recipient's approval flow. Default: 'human'. Ignored for broadcasts."
			},
			"message_id": {
				"type": "string",
				"description": "Message ID to approve or reject (for 'approve'/'reject' actions)"
			},
			"reason": {
				"type": "string",
				"description": "Rejection reason (for 'reject' action)"
			},
			"limit": {
				"type": "integer",
				"description": "Max number of history messages to return (for 'history' action). Default: 20"
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
		Mode      string `json:"mode"`
		Online    bool   `json:"online"`
		LastSeen  string `json:"last_seen"`
		Self      bool   `json:"self"`
	}

	var peers []peerInfo
	for _, p := range participants {
		lastSeen := "never"
		if p.LastSeen > 0 {
			lastSeen = time.Since(time.UnixMilli(p.LastSeen)).Round(time.Second).String() + " ago"
		}
		peers = append(peers, peerInfo{
			NodeID:    p.NodeID,
			HumanNick: p.HumanNick,
			AgentNick: p.AgentNick,
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
