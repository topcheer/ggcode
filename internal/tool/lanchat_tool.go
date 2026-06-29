package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
		"This is the PRIMARY tool for real-time collaboration with other ggcode users and their agents on the LAN. " +
		"Triggers: user mentions another participant by name or nick (e.g. \"check what mdns is doing\", \"ask ggai about...\"), " +
		"messages prefixed with [LAN Chat from <nick>], or any reference to LAN participants.\n" +
		"Do NOT use send_message (that is for swarm teammates) or delegate/a2a_remote (those are for headless code-edit delegation to other workspaces, not for asking questions).\n" +
		"\nANTINOISE RULES (critical for efficient collaboration):\n" +
		"- Do NOT broadcast unless the user explicitly asks to notify everyone. Broadcasts force ALL agents to process your message, creating noise cascades.\n" +
		"- Do NOT send confirmation/acknowledgment messages (\"got it\", \"will do\", \"thanks\"). Just do the work silently.\n" +
		"- Do NOT reply to a LAN Chat message unless you have meaningful information to share (results, answers, blocking issues). Acknowledgments are noise.\n" +
		"- Before sending, check action='list' for agent_busy status. Do NOT message busy agents unless it's urgent — they'll see your message after their current task.\n" +
		"- Send to the specific person you need (action='send' with their node_id or nick), not a team broadcast. Only broadcast for announcements that truly need everyone.\n" +
		"- One message per task. Do NOT send follow-up pings asking \"are you done?\" — check back with action='list' or wait for their response.\n" +
		"- If you receive a broadcast or team message that is not directed at you specifically, do NOT reply unless you have actionable information.\n" +
		"\nNick format: nicks are composed as <name>_<role> (e.g. 'alice_frontend', 'mdns_developer'). " +
		"When a user says 'ask mdns', match the participant whose nick starts with 'mdns' — the full nick is 'mdns_developer' but you should use the node_id from list, not the nick, as the 'to' field.\n" +
		"\nMessaging actions (choose by scope):\n" +
		"- 'send' (to=<node_id OR nick>): DM one or more participants. You can use the participant's nick (e.g. \"dd_dev_agent\" or prefix \"dd\") or node_id. Multiple recipients: comma-separated.\n" +
		"- 'broadcast': broadcast to members of YOUR OWN team. Use ONLY for announcements that need everyone's attention.\n" +
		"- 'broadcast_all': broadcast to ALL participants on the entire LAN. Almost NEVER appropriate — use only for critical announcements.\n" +
		"- 'send_team' (team=<name>): broadcast to all members of a SPECIFIC team. Prefer targeted DMs over this.\n" +
		"\nAgent availability: each participant in the 'list' output has an 'agent_busy' field (true/false). " +
		"Always check this before messaging. Prefer idle agents (agent_busy=false). " +
		"If the only relevant agent is busy, send your message once and wait — do not re-send or ping.\n" +
		"\nWhen to use lanchat vs a2a_remote:\n" +
		"- lanchat: real-time communication — asking a specific question, coordinating a specific task, reporting results.\n" +
		"- a2a_remote: headless code-editing delegation — fire-and-forget tasks with a specific code instruction.\n" +
		"- When in doubt, use a targeted DM (action='send') to the specific person you need."
}

func (t LanChatTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["list", "send", "broadcast", "broadcast_all", "send_team", "history", "pending", "approve", "reject"],
				"description": "list=discover participants; send=DM one or more participants (requires 'to', supports comma-separated); broadcast=send to YOUR team members (team-scoped default); broadcast_all=send to ALL participants on the LAN; send_team=send to a specific team (requires 'team'); history=recent messages; pending=list @agent approvals; approve/reject a pending message (requires 'message_id'). Choose by scope: DM=send, your team=broadcast, specific team=send_team, everyone=broadcast_all."
			},
			"message": {
				"type": "string",
				"description": "Message text to send (required for 'send', 'broadcast', 'broadcast_all', 'send_team')."
			},
			"to": {
				"type": "string",
				"description": "Recipient node_id(s) for 'send' action (DM). Single recipient: \"node-id\". Multiple recipients: comma-separated \"id1,id2,id3\". Using to='*' is equivalent to action='broadcast_all'. Find via action='list'. Ignored by broadcast/broadcast_all/send_team actions."
			},
			"team": {
				"type": "string",
				"description": "Team name. Required for 'send_team'. Also used as an optional filter for 'list' to show only members of a specific team. Ignored by send/broadcast/broadcast_all."
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

	// 'to' can be a JSON array of strings or a single string
	var args struct {
		Action    string          `json:"action"`
		Message   string          `json:"message"`
		Team      string          `json:"team"`
		AsAgent   *bool           `json:"as_agent"`
		ToRole    string          `json:"to_role"`
		MessageID string          `json:"message_id"`
		Reason    string          `json:"reason"`
		Limit     int             `json:"limit"`
		RawTo     json.RawMessage `json:"to"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	// as_agent defaults to true — this tool is almost always called by the
	// agent, so messages should come from the agent identity. Only explicitly
	// set as_agent=false should override this.
	asAgent := true
	if args.AsAgent != nil {
		asAgent = *args.AsAgent
	}

	// Parse 'to' which may be a string or an array of strings
	toNodeIDs := parseNodeIDs(args.RawTo)

	switch args.Action {
	case "list":
		return t.doList(args.Team), nil
	case "send":
		return t.doSend(ctx, args.Message, toNodeIDs, asAgent, args.ToRole)
	case "broadcast":
		return t.doBroadcastTeam(ctx, args.Message, asAgent)
	case "broadcast_all":
		return t.doBroadcastAll(ctx, args.Message, asAgent)
	case "send_team":
		return t.doSendTeam(ctx, args.Message, args.Team, asAgent)
	case "history":
		return t.doHistory(args.Limit), nil
	case "pending":
		return t.doPending(), nil
	case "approve":
		return t.doApprove(ctx, args.MessageID)
	case "reject":
		return t.doReject(args.MessageID, args.Reason)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action: %s (valid: list, send, broadcast, broadcast_all, send_team, history, pending, approve, reject)", args.Action)}, nil
	}
}

// parseNodeIDs accepts 'to' as a string ("id1") , comma-separated ("id1,id2"),
// or a JSON array (["id1","id2"]). Returns a deduplicated list of node IDs.
func parseNodeIDs(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	// Try JSON array first
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return dedupStrings(arr)
	}

	// Fall back to string (may be comma-separated)
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	return dedupStrings(parts)
}

func dedupStrings(in []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// resolveRecipients maps user-supplied identifiers (node IDs, full nicks,
// or partial nick prefixes) to actual node IDs from the known peers list.
//
// Resolution order per identifier:
//  1. Exact node_id match in active peers
//  2. Exact human_nick or agent_nick match in active peers (case-insensitive)
//  3. Unique prefix match on human_nick or agent_nick
//  4. Archive fallback: look up the identifier in the archive ring buffer.
//     If found, use the archived peer's team+role to find a currently
//     active peer (the peer may have restarted with a new NodeID).
//
// If no match is found for any identifier, returns nil.
func (t LanChatTool) resolveRecipients(ids []string) []string {
	participants := t.Hub.Participants()

	// Build lookup maps (case-insensitive)
	byNodeID := make(map[string]string) // lower(nodeID) -> nodeID
	byNick := make(map[string]string)   // lower(nick) -> nodeID
	byPrefix := make([]struct{ nick, id string }, 0)

	// Also build team+role index for archive fallback
	byTeamRole := make(map[string]string) // "team|role" -> nodeID

	for _, p := range participants {
		if p.NodeID == t.Hub.NodeID() {
			continue // skip self
		}
		byNodeID[strings.ToLower(p.NodeID)] = p.NodeID
		for _, nick := range []string{p.HumanNick, p.AgentNick} {
			if nick == "" {
				continue
			}
			lower := strings.ToLower(nick)
			byNick[lower] = p.NodeID
			byPrefix = append(byPrefix, struct{ nick, id string }{lower, p.NodeID})
		}
		if p.Team != "" && p.Role != "" {
			key := p.Team + "|" + p.Role
			if _, exists := byTeamRole[key]; !exists {
				byTeamRole[key] = p.NodeID
			}
		}
	}

	var unresolved []string
	var resolved []string
	seen := make(map[string]bool)
	for _, raw := range ids {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		lower := strings.ToLower(raw)

		// 1. Exact node_id
		if id, ok := byNodeID[lower]; ok {
			if !seen[id] {
				resolved = append(resolved, id)
				seen[id] = true
			}
			continue
		}
		// 2. Exact nick
		if id, ok := byNick[lower]; ok {
			if !seen[id] {
				resolved = append(resolved, id)
				seen[id] = true
			}
			continue
		}
		// 3. Prefix match (first wins)
		found := false
		for _, entry := range byPrefix {
			if strings.HasPrefix(entry.nick, lower) {
				if !seen[entry.id] {
					resolved = append(resolved, entry.id)
					seen[entry.id] = true
				}
				found = true
				break
			}
		}
		if found {
			continue
		}

		// Not found in active peers — try archive fallback
		unresolved = append(unresolved, raw)
	}

	// 4. Archive fallback: for each unresolved identifier, search the
	//    archive ring buffer. If found, use team+role to find the peer's
	//    current NodeID in the active peers map.
	for _, raw := range unresolved {
		// Try archive by node_id
		ap := t.Hub.LookupArchiveByNodeID(raw)
		if ap == nil {
			// Try archive by nick
			ap = t.Hub.LookupArchiveByNick(raw)
		}

		if ap != nil && ap.Team != "" && ap.Role != "" {
			// Found in archive — try to find a currently active peer
			// with the same team+role (the peer may have restarted)
			key := ap.Team + "|" + ap.Role
			if id, ok := byTeamRole[key]; ok {
				if !seen[id] {
					resolved = append(resolved, id)
					seen[id] = true
				}
				continue
			}
		}
		// If nothing matched, skip silently — caller handles empty result
	}

	return resolved
}

func (t LanChatTool) doList(teamFilter string) Result {
	participants := t.Hub.Participants()
	selfNodeID := t.Hub.NodeID()

	type peerInfo struct {
		NodeID      string   `json:"node_id"`
		HumanNick   string   `json:"human_nick"`
		AgentNick   string   `json:"agent_nick"`
		Role        string   `json:"role"`
		Team        string   `json:"team"`
		Mode        string   `json:"mode"`
		Online      bool     `json:"online"`
		LastSeen    string   `json:"last_seen"`
		Endpoint    string   `json:"endpoint,omitempty"`
		Workspace   string   `json:"workspace,omitempty"`
		ProjectName string   `json:"project_name,omitempty"`
		Languages   []string `json:"languages,omitempty"`
		AgentBusy   bool     `json:"agent_busy"`
		Self        bool     `json:"self"`
	}

	var peers []peerInfo
	for _, p := range participants {
		// Apply team filter if specified
		if teamFilter != "" && !strings.EqualFold(p.Team, teamFilter) {
			continue
		}
		lastSeen := "never"
		if p.LastSeen > 0 {
			lastSeen = time.Since(time.Unix(p.LastSeen, 0)).Round(time.Second).String() + " ago"
		}
		peers = append(peers, peerInfo{
			NodeID:      p.NodeID,
			HumanNick:   p.HumanNick,
			AgentNick:   p.AgentNick,
			Role:        p.Role,
			Team:        p.Team,
			Mode:        p.Mode,
			Online:      p.Online,
			LastSeen:    lastSeen,
			Endpoint:    p.Endpoint,
			Workspace:   p.Workspace,
			ProjectName: p.ProjectName,
			Languages:   p.Languages,
			AgentBusy:   p.AgentBusy,
			Self:        p.NodeID == selfNodeID,
		})
	}

	label := ""
	if teamFilter != "" {
		label = fmt.Sprintf(" (team: %s)", teamFilter)
	}

	if len(peers) == 0 {
		if teamFilter != "" {
			return Result{Content: fmt.Sprintf("No participants in team '%s'.\n", teamFilter)}
		}
		return Result{Content: "No LAN Chat participants discovered.\n"}
	}

	out, _ := json.MarshalIndent(peers, "", "  ")
	return Result{Content: fmt.Sprintf("Participants (%d)%s:\n%s\n", len(peers), label, string(out))}
}

// doSend sends a direct message to one or more recipients.
// toNodeIDs supports multiple recipients (comma-separated or JSON array).
// A "*" in the list triggers a global broadcast (broadcast_all semantics).
func (t LanChatTool) doSend(ctx context.Context, content string, toNodeIDs []string, asAgent bool, toRole string) (Result, error) {
	if content == "" {
		return Result{IsError: true, Content: "message content is required for send"}, nil
	}
	if len(toNodeIDs) == 0 {
		return Result{IsError: true, Content: "recipient 'to' is required for send (use action='list' to find node_id)"}, nil
	}

	// Check for wildcard broadcast
	if len(toNodeIDs) == 1 && toNodeIDs[0] == "*" {
		return t.doBroadcastAll(ctx, content, asAgent)
	}

	// Resolve nicks to node IDs. The LLM naturally uses nicks (e.g.
	// "dd_dev_agent") or partial nicks (e.g. "dd") as the 'to' value.
	// We try exact node_id match first, then exact nick match, then
	// prefix match (nick starts with the given string).
	resolved := t.resolveRecipients(toNodeIDs)
	if len(resolved) == 0 {
		// No matches at all — show available participants to help
		hint := t.doList("")
		return Result{IsError: true, Content: fmt.Sprintf(
			"no recipient found for: %s. Use action='list' to see valid node_ids and nicks.\n%s",
			strings.Join(toNodeIDs, ", "), hint.Content)}, nil
	}

	// Default toRole: when sending as agent, target the recipient's agent;
	// when sending as human, target the human chat panel.
	if toRole == "" {
		if asAgent {
			toRole = lanchat.RoleAgent
		} else {
			toRole = lanchat.RoleHuman
		}
	}

	// Send to each recipient individually
	var errors []string
	sent := 0
	for _, nodeID := range resolved {
		var err error
		if asAgent {
			err = t.Hub.SendAsAgent(ctx, nodeID, toRole, content)
		} else {
			err = t.Hub.SendDirect(ctx, nodeID, toRole, content, nil)
		}
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", nodeID, err))
		} else {
			sent++
		}
	}

	role := "human"
	if asAgent {
		role = "agent"
	}

	if sent == 0 {
		return Result{IsError: true, Content: fmt.Sprintf("failed to send to any recipient: %s", strings.Join(errors, "; "))}, nil
	}

	target := toNodeIDs[0]
	if len(toNodeIDs) > 1 {
		target = fmt.Sprintf("%s (%d recipients)", strings.Join(toNodeIDs, ", "), len(toNodeIDs))
	}

	result := fmt.Sprintf("Message sent to %s as %s", target, role)
	if len(errors) > 0 {
		result += fmt.Sprintf(" (errors: %s)", strings.Join(errors, "; "))
	}
	return Result{Content: result + ".\n"}, nil
}

// doBroadcastTeam broadcasts to members of the sender's own team.
// This is the default broadcast behavior — it respects team isolation.
func (t LanChatTool) doBroadcastTeam(ctx context.Context, content string, asAgent bool) (Result, error) {
	if content == "" {
		return Result{IsError: true, Content: "message content is required for broadcast"}, nil
	}

	myTeam := t.Hub.Team()
	if myTeam == "" {
		myTeam = "dev-team"
	}

	return t.doSendTeam(ctx, content, myTeam, asAgent)
}

// doBroadcastAll sends to every participant on the LAN, regardless of team.
func (t LanChatTool) doBroadcastAll(ctx context.Context, content string, asAgent bool) (Result, error) {
	if content == "" {
		return Result{IsError: true, Content: "message content is required for broadcast_all"}, nil
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
	return Result{Content: fmt.Sprintf("Broadcast sent to ALL participants on the LAN as %s.\n", role)}, nil
}

// doSendTeam sends a message to all participants belonging to a specific team.
// It iterates participants, filters by the team field, and sends individual DMs
// to each matching member. If no team matches, returns an error listing valid teams.
func (t LanChatTool) doSendTeam(ctx context.Context, content, team string, asAgent bool) (Result, error) {
	if content == "" {
		return Result{IsError: true, Content: "message content is required for send_team"}, nil
	}
	if team == "" {
		return Result{IsError: true, Content: "team is required for send_team (use action='list' to see valid teams)"}, nil
	}

	participants := t.Hub.Participants()
	selfNodeID := t.Hub.NodeID()

	var members []string
	var teamsSeen = make(map[string]bool)
	for _, p := range participants {
		if p.Team != "" {
			teamsSeen[p.Team] = true
		}
		if p.NodeID == selfNodeID {
			continue // skip self
		}
		if !p.Online {
			continue
		}
		if p.Team == team {
			members = append(members, p.NodeID)
		}
	}

	if len(members) == 0 {
		validTeams := make([]string, 0, len(teamsSeen))
		for tk := range teamsSeen {
			validTeams = append(validTeams, tk)
		}
		return Result{IsError: true, Content: fmt.Sprintf(
			"No online participants in team %q. Valid teams: %s",
			team, strings.Join(validTeams, ", "),
		)}, nil
	}

	// Send to each team member
	toRole := lanchat.RoleAgent // default: reach their agent for team coordination
	var errors []string
	sent := 0
	for _, nodeID := range members {
		var err error
		if asAgent {
			err = t.Hub.SendAsAgent(ctx, nodeID, toRole, content)
		} else {
			err = t.Hub.SendDirect(ctx, nodeID, toRole, content, nil)
		}
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", nodeID, err))
		} else {
			sent++
		}
	}

	if sent == 0 {
		return Result{IsError: true, Content: fmt.Sprintf(
			"Failed to send to any member of team %q: %s",
			team, strings.Join(errors, "; "),
		)}, nil
	}

	result := fmt.Sprintf("Sent to %d/%d members of team %q", sent, len(members), team)
	if len(errors) > 0 {
		result += fmt.Sprintf(" (errors: %s)", strings.Join(errors, "; "))
	}
	return Result{Content: result + ".\n"}, nil
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
