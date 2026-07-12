package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/lanchat"
)

// LanChatTool lets the agent interact with the LAN Chat system:
// list participants, send messages, read history, and manage pending approvals.
type LanChatTool struct {
	Hub         *lanchat.Hub
	rateLimiter *agentRateLimiter
	dmCooldown  time.Duration // per-recipient agent DM cooldown
}

// NewLanChatTool creates a LanChatTool with an initialized agent rate limiter.
// The rate limiter prevents LLM-originated message storms that can cause
// cascading noise across all agents on the LAN.
//
// If cfg provides a non-zero dm_cooldown, that value is used instead of the default.
func NewLanChatTool(hub *lanchat.Hub, cfg config.LanChatConfig) LanChatTool {
	t := LanChatTool{
		Hub:         hub,
		rateLimiter: newAgentRateLimiter(),
		dmCooldown:  cfg.EffectiveDMCooldown(),
	}
	// Register inbound DM callback so the rate limiter automatically resets
	// the self→sender DM cooldown when a non-broadcast message arrives.
	// This allows the local agent to reply immediately without being blocked.
	if hub != nil {
		hub.SetOnInboundDM(func(fromNodeID string) {
			t.OnInboundDM(fromNodeID)
		})
	}
	return t
}

// agentRateLimiter prevents LLM message storms by rate-limiting agent-originated
// lanchat messages. This is critical because each agent message may trigger
// another agent's LLM inference cycle (expensive, slow, and noisy).
//
// Rate limiting is per-recipient for all send paths (send, broadcast,
// broadcast_all, send_team). Each sender→receiver pair has a configurable
// cooldown (default 150s, set via lanchat.dm_cooldown), allowing the same agent
// to message different peers in parallel while preventing repeated spam to the
// same peer.
//
// Only applies when asAgent=true (agent is the sender). Human-originated
// messages are never rate-limited.
type agentRateLimiter struct {
	mu         sync.Mutex
	dmLastSent map[string]time.Time // key: "sender→receiver"
}

// defaultAgentDMCooldown is used when no config is provided (e.g. in tests).
const defaultAgentDMCooldown = 150 * time.Second

func newAgentRateLimiter() *agentRateLimiter {
	return &agentRateLimiter{
		dmLastSent: make(map[string]time.Time),
	}
}

// checkDM returns empty string if allowed, or an error message if rate-limited.
// The key is "sender→receiver" so both parties are explicitly recorded.
// cooldown defaults to defaultAgentDMCooldown when zero.
func (r *agentRateLimiter) checkDM(sender, recipient string, cooldown time.Duration) string {
	if cooldown <= 0 {
		cooldown = defaultAgentDMCooldown
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	key := sender + "\u2192" + recipient // "sender→recipient"
	last, ok := r.dmLastSent[key]
	if !ok {
		return ""
	}
	elapsed := time.Since(last)
	if elapsed >= cooldown {
		return ""
	}
	remaining := cooldown - elapsed
	return fmt.Sprintf(
		"rate-limited (%s remaining) — use sleep tool with seconds=%d to defer retry",
		formatCooldown(remaining),
		int(remaining.Seconds())+1,
	)
}

// recordDM marks a DM from sender to recipient as sent at the current time.
func (r *agentRateLimiter) recordDM(sender, recipient string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := sender + "\u2192" + recipient
	r.dmLastSent[key] = time.Now()
}

// resetDMForPeer clears the DM cooldown for the self→peer direction.
// Called when a non-broadcast message arrives from peer, allowing
// the local agent to reply immediately without waiting for cooldown.
func (r *agentRateLimiter) resetDMForPeer(selfID, peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := selfID + "\u2192" + peerID
	delete(r.dmLastSent, key)
}

// sharedWorkspaceHint returns a concise collaboration warning string when any
// of the given recipient node IDs share the same workspace path as this node.
// This is injected into lanchat tool results so the LLM knows to include
// workspace-safety instructions when delegating work to same-workspace peers.
//
// Returns "" when no recipient shares the workspace (cross-workspace DMs,
// or recipients whose workspace is unknown).
func (t *LanChatTool) sharedWorkspaceHint(recipientNodeIDs []string) string {
	if t.Hub == nil {
		return ""
	}
	selfWS := t.Hub.SelfParticipant().Workspace
	if selfWS == "" {
		return ""
	}

	participants := t.Hub.Participants()
	recipientSet := make(map[string]bool, len(recipientNodeIDs))
	for _, id := range recipientNodeIDs {
		recipientSet[id] = true
	}

	var sameWSNames []string
	for _, p := range participants {
		if !recipientSet[p.NodeID] {
			continue
		}
		if p.Workspace == selfWS {
			name := p.AgentNick
			if name == "" {
				name = p.HumanNick
			}
			if name == "" {
				name = p.NodeID
			}
			sameWSNames = append(sameWSNames, name)
		}
	}

	if len(sameWSNames) == 0 {
		return ""
	}

	sort.Strings(sameWSNames)
	return fmt.Sprintf(
		"\n⚠ Shared workspace: %s work in %s too. When instructing them: "+
			"(1) no git stash/checkout/reset (clobbers others' changes), "+
			"(2) only stage your own files, "+
			"(3) don't fix other agents' code — DM the owner, "+
			"(4) use worktree for isolated changes.",
		strings.Join(sameWSNames, ", "), selfWS)
}

// OnInboundDM resets the DM cooldown for the sender when a non-broadcast
// agent message arrives, so the local agent can reply without being
// rate-limited. Called from the TUI's lanchat message callback.
func (t *LanChatTool) OnInboundDM(fromNodeID string) {
	if t.rateLimiter == nil || t.Hub == nil {
		return
	}
	selfID := t.Hub.NodeID()
	t.rateLimiter.resetDMForPeer(selfID, fromNodeID)
}

// displayName returns the best human-readable name for a nodeID.
// Prefers agent nick, then human nick, then falls back to the nodeID itself.
func (t *LanChatTool) displayName(nodeID string) string {
	if t.Hub == nil {
		return nodeID
	}
	for _, p := range t.Hub.Participants() {
		if p.NodeID == nodeID {
			if p.AgentNick != "" {
				return p.AgentNick
			}
			if p.HumanNick != "" {
				return p.HumanNick
			}
			break
		}
	}
	// Node not in participants list — show a short prefix instead of the full long ID
	if len(nodeID) > 16 {
		return nodeID[:16] + "..."
	}
	return nodeID
}

// formatCooldown formats a duration as a human-readable string.
func formatCooldown(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) - mins*60
	if secs == 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dm %ds", mins, secs)
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
				"enum": ["list", "send", "broadcast", "broadcast_all", "send_team", "history", "pending", "approve", "reject", "set_identity"],
				"description": "list=discover participants; send=DM one or more participants (requires 'to', supports comma-separated); broadcast=send to YOUR team members (team-scoped default); broadcast_all=send to ALL participants on the LAN; send_team=send to a specific team (requires 'team'); history=recent messages; pending=list @agent approvals; approve/reject a pending message (requires 'message_id'); set_identity=change your own nick, role, and/or team (requires at least one of 'nick', 'role', 'team'). Choose by scope: DM=send, your team=broadcast, specific team=send_team, everyone=broadcast_all."
			},
			"message": {
				"type": "string",
				"description": "Message text to send (required for 'send', 'broadcast', 'broadcast_all', 'send_team')."
			},
			"to": {
				"type": "string",
				"description": "Recipient node_id(s) for 'send' action (DM). Single recipient: \"node-id\". Multiple recipients: comma-separated \"id1,id2,id3\". Using to='*' is equivalent to action='broadcast_all'. Find via action='list'. Ignored by broadcast/broadcast_all/send_team actions."
			},
			"nick": {
				"type": "string",
				"description": "New nickname for set_identity (e.g. 'alice', 'bob'). Combined with role to form the composite nick 'nick_role'. Optional — only provided fields are changed."
			},
			"role": {
				"type": "string",
				"description": "New role for set_identity (e.g. 'developer', 'frontend', 'devops'). Optional."
			},
			"team": {
				"type": "string",
				"description": "Team name. Required for 'send_team'. Also used as an optional filter for 'list' to show only members of a specific team. For 'set_identity', changes your team membership. Ignored by send/broadcast/broadcast_all."
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
		Nick      string          `json:"nick"`
		Role      string          `json:"role"`
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
	case "set_identity":
		return t.doSetIdentity(args.Nick, args.Role, args.Team)
	case "reject":
		return t.doReject(args.MessageID, args.Reason)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action: %s (valid: list, send, broadcast, broadcast_all, send_team, history, pending, approve, reject, set_identity)", args.Action)}, nil
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

	// Rate-limit check for agent-originated messages (anti-storm protection).
	// Each sender→recipient pair has an independent cooldown (default 150s, configurable via lanchat.dm_cooldown).
	var rateLimited []string
	if asAgent && t.rateLimiter != nil {
		selfID := t.Hub.NodeID()
		var allowed []string
		for _, nodeID := range resolved {
			if msg := t.rateLimiter.checkDM(selfID, nodeID, t.dmCooldown); msg != "" {
				rateLimited = append(rateLimited, fmt.Sprintf("%s: %s", t.displayName(nodeID), msg))
			} else {
				allowed = append(allowed, nodeID)
			}
		}
		if len(allowed) == 0 {
			// All recipients are rate-limited
			return Result{IsError: true, Content: fmt.Sprintf(
				"All recipients are rate-limited:\n%s", strings.Join(rateLimited, "\n"))}, nil
		}
		resolved = allowed
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
			errors = append(errors, fmt.Sprintf("%s: %v", t.displayName(nodeID), err))
		} else {
			sent++
			// Record rate-limit timestamp for successful sends
			if asAgent && t.rateLimiter != nil {
				selfID := t.Hub.NodeID()
				t.rateLimiter.recordDM(selfID, nodeID)
			}
		}
	}

	role := "human"
	if asAgent {
		role = "agent"
	}

	if sent == 0 {
		errMsg := fmt.Sprintf("failed to send to any recipient: %s", strings.Join(errors, "; "))
		if asAgent {
			errMsg += t.sharedWorkspaceHint(resolved)
		}
		return Result{IsError: true, Content: errMsg}, nil
	}

	// Build display names for the success message
	var displayTargets []string
	for _, id := range toNodeIDs {
		displayTargets = append(displayTargets, t.displayName(id))
	}
	target := displayTargets[0]
	if len(displayTargets) > 1 {
		target = fmt.Sprintf("%s (%d recipients)", strings.Join(displayTargets, ", "), len(displayTargets))
	}

	result := fmt.Sprintf("Message sent to %s as %s", target, role)
	if len(errors) > 0 {
		result += fmt.Sprintf(" (errors: %s)", strings.Join(errors, "; "))
	}
	if len(rateLimited) > 0 {
		result += fmt.Sprintf("\nRate-limited recipients skipped:\n%s", strings.Join(rateLimited, "\n"))
	}
	if asAgent {
		result += t.sharedWorkspaceHint(resolved)
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
// Uses per-peer DM cooldowns (not a single broadcast cooldown) so that peers
// who recently messaged us (cooldown reset) can still receive the broadcast.
func (t LanChatTool) doBroadcastAll(ctx context.Context, content string, asAgent bool) (Result, error) {
	if content == "" {
		return Result{IsError: true, Content: "message content is required for broadcast_all"}, nil
	}

	// Collect all non-self participant node IDs
	selfID := t.Hub.NodeID()
	var allPeers []string
	for _, p := range t.Hub.Participants() {
		if p.NodeID != selfID && p.Online {
			allPeers = append(allPeers, p.NodeID)
		}
	}

	if len(allPeers) == 0 {
		return Result{IsError: true, Content: "no online participants to broadcast to"}, nil
	}

	toRole := lanchat.RoleAgent

	// Per-peer rate-limit check for agent-originated messages
	var allowed []string
	var rateLimited []string
	if asAgent && t.rateLimiter != nil {
		for _, nodeID := range allPeers {
			if msg := t.rateLimiter.checkDM(selfID, nodeID, t.dmCooldown); msg != "" {
				rateLimited = append(rateLimited, fmt.Sprintf("%s: %s", t.displayName(nodeID), msg))
			} else {
				allowed = append(allowed, nodeID)
			}
		}
		if len(allowed) == 0 {
			return Result{IsError: true, Content: fmt.Sprintf(
				"All broadcast recipients are rate-limited:\n%s", strings.Join(rateLimited, "\n"))}, nil
		}
	} else {
		allowed = allPeers
	}

	// Send to each allowed recipient individually
	var errors []string
	sent := 0
	for _, nodeID := range allowed {
		var err error
		if asAgent {
			err = t.Hub.SendAsAgent(ctx, nodeID, toRole, content)
		} else {
			err = t.Hub.SendDirect(ctx, nodeID, toRole, content, nil)
		}
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", t.displayName(nodeID), err))
		} else {
			sent++
			if asAgent && t.rateLimiter != nil {
				t.rateLimiter.recordDM(selfID, nodeID)
			}
		}
	}

	role := "human"
	if asAgent {
		role = "agent"
	}
	if sent == 0 {
		errMsg := fmt.Sprintf("Failed to broadcast to any recipient: %s", strings.Join(errors, "; "))
		if asAgent {
			errMsg += t.sharedWorkspaceHint(allowed)
		}
		return Result{IsError: true, Content: errMsg}, nil
	}

	result := fmt.Sprintf("Broadcast sent to %d/%d participants as %s.", sent, len(allPeers), role)
	if len(rateLimited) > 0 {
		result += fmt.Sprintf("\nRate-limited recipients skipped:\n%s", strings.Join(rateLimited, "\n"))
	}
	if len(errors) > 0 {
		result += fmt.Sprintf(" (errors: %s)", strings.Join(errors, "; "))
	}
	result += "\n"
	if asAgent {
		result += t.sharedWorkspaceHint(allowed)
	}
	return Result{Content: result}, nil
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

	// Per-peer rate-limit check for agent-originated team sends (anti-storm)
	var allowed []string
	var rateLimited []string
	if asAgent && t.rateLimiter != nil {
		for _, nodeID := range members {
			if msg := t.rateLimiter.checkDM(selfNodeID, nodeID, t.dmCooldown); msg != "" {
				rateLimited = append(rateLimited, fmt.Sprintf("%s: %s", t.displayName(nodeID), msg))
			} else {
				allowed = append(allowed, nodeID)
			}
		}
		if len(allowed) == 0 {
			return Result{IsError: true, Content: fmt.Sprintf(
				"All team members are rate-limited:\n%s", strings.Join(rateLimited, "\n"))}, nil
		}
		members = allowed
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
			errors = append(errors, fmt.Sprintf("%s: %v", t.displayName(nodeID), err))
		} else {
			sent++
			if asAgent && t.rateLimiter != nil {
				t.rateLimiter.recordDM(selfNodeID, nodeID)
			}
		}
	}

	if sent == 0 {
		errMsg := fmt.Sprintf(
			"Failed to send to any member of team %q: %s",
			team, strings.Join(errors, "; "),
		)
		if asAgent {
			errMsg += t.sharedWorkspaceHint(members)
		}
		return Result{IsError: true, Content: errMsg}, nil
	}

	result := fmt.Sprintf("Sent to %d/%d members of team %q", sent, len(members), team)
	if len(rateLimited) > 0 {
		result += fmt.Sprintf("\nRate-limited members skipped:\n%s", strings.Join(rateLimited, "\n"))
	}
	if len(errors) > 0 {
		result += fmt.Sprintf(" (errors: %s)", strings.Join(errors, "; "))
	}
	if asAgent {
		result += t.sharedWorkspaceHint(members)
	}
	return Result{Content: result + ".\n"}, nil
}

// summarizeContent truncates a message body to a single-line summary.
func summarizeContent(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 120 {
		runes := []rune(s)
		return string(runes[:117]) + "..."
	}
	return s
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

	var lines []string
	for _, m := range recent {
		ts := time.UnixMilli(m.Timestamp).Format("15:04:05")
		from := m.FromNick // FromNick already includes role suffix (e.g. "CleverOtter_developer_agent")
		body := summarizeContent(m.Content)
		if m.ToNodeID != "" {
			lines = append(lines, fmt.Sprintf("  [%s] %s → (DM) %s", ts, from, body))
		} else {
			lines = append(lines, fmt.Sprintf("  [%s] %s → (team) %s", ts, from, body))
		}
	}

	return Result{Content: fmt.Sprintf("History (%d messages):\n%s\n", len(recent), strings.Join(lines, "\n"))}
}

func (t LanChatTool) doPending() Result {
	pending := t.Hub.PendingApprovals()
	if len(pending) == 0 {
		return Result{Content: "No pending @agent approvals.\n"}
	}

	var lines []string
	for _, p := range pending {
		from := p.Message.FromNick // already includes role suffix
		age := time.Since(p.Received).Round(time.Second).String() + " ago"
		lines = append(lines, fmt.Sprintf("  • [%s] from %s (%s): %s", p.Message.ID, from, age, summarizeContent(p.Message.Content)))
	}

	return Result{Content: fmt.Sprintf("Pending approvals (%d):\n%s\n", len(pending), strings.Join(lines, "\n"))}
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

func (t LanChatTool) doSetIdentity(nick, role, team string) (Result, error) {
	if nick == "" && role == "" && team == "" {
		return Result{IsError: true, Content: "at least one of 'nick', 'role', or 'team' is required for set_identity"}, nil
	}

	// Merge with current values for fields not provided
	curNick := t.Hub.HumanNick()
	curRole := t.Hub.Role()
	curTeam := t.Hub.Team()

	// HumanNick is a composite "name_role" — extract the name part
	if nick == "" {
		if idx := strings.LastIndex(curNick, "_"); idx >= 0 {
			nick = curNick[:idx]
		} else {
			nick = curNick
		}
	}
	if role == "" {
		role = curRole
	}
	if team == "" {
		team = curTeam
	}

	if err := t.Hub.SetNickRoleTeam(nick, role, team); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to set identity: %v", err)}, nil
	}

	composite := nick + "_" + role
	return Result{Content: fmt.Sprintf("Identity updated: nick=%s, role=%s, team=%s (composite: %s)\n", nick, role, team, composite)}, nil
}
