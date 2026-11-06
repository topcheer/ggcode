package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/lanchat"
)

type LanChatTool struct {
	Hub         *lanchat.Hub
	rateLimiter *agentRateLimiter
	dmCooldown  time.Duration
}

func NewLanChatTool(hub *lanchat.Hub, cfg config.LanChatConfig) LanChatTool {
	t := LanChatTool{
		Hub:         hub,
		rateLimiter: newAgentRateLimiter(),
		dmCooldown:  cfg.EffectiveDMCooldown(),
	}

	if hub != nil {
		hub.SetOnInboundDM(func(fromNodeID string) {
			t.OnInboundDM(fromNodeID)
		})
	}
	return t
}

type agentRateLimiter struct {
	mu         sync.Mutex
	dmLastSent map[string]time.Time
}

const defaultAgentDMCooldown = 150 * time.Second

func newAgentRateLimiter() *agentRateLimiter {
	return &agentRateLimiter{
		dmLastSent: make(map[string]time.Time),
	}
}

func (r *agentRateLimiter) checkDM(sender, recipient string, cooldown time.Duration) string {
	if cooldown <= 0 {
		cooldown = defaultAgentDMCooldown
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	key := sender + "\u2192" + recipient
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
		"rate-limited (%s remaining) - use sleep tool with seconds=%d to defer retry",
		formatCooldown(remaining),
		int(remaining.Seconds())+1,
	)
}

func (r *agentRateLimiter) recordDM(sender, recipient string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := sender + "\u2192" + recipient
	r.dmLastSent[key] = time.Now()
}

func (r *agentRateLimiter) resetDMForPeer(selfID, peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := selfID + "\u2192" + peerID
	delete(r.dmLastSent, key)
}

// #5117: a resolved-but-offline recipient (stale node_id after a seat
// restarts) turns every send into a ~10s unicast timeout that callers
// retry 2-3 times as a network transient before discovering the truth
// via action=list. Warn up front in the send result so the caller can
// refresh addressing immediately instead of burning retries.
func (t *LanChatTool) staleRecipientHint(recipientNodeIDs []string) string {
	if t.Hub == nil {
		return ""
	}
	recipientSet := make(map[string]bool, len(recipientNodeIDs))
	for _, id := range recipientNodeIDs {
		recipientSet[id] = true
	}
	var stale []string
	for _, p := range t.Hub.Participants() {
		if !recipientSet[p.NodeID] || p.Online {
			continue
		}
		name := p.AgentNick
		if name == "" {
			name = p.HumanNick
		}
		if name == "" {
			name = p.NodeID
		}
		age := ""
		if p.LastSeen > 0 {
			age = fmt.Sprintf(", last seen %s ago", time.Since(time.Unix(p.LastSeen, 0)).Round(time.Second))
		}
		stale = append(stale, fmt.Sprintf("%s (%s%s)", name, p.NodeID, age))
	}
	if len(stale) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"\nWarning: %d recipient(s) are currently OFFLINE in the hub: %s. "+
			"node_ids from earlier sessions are invalid after restart - "+
			"call action='list' and re-address with a current node_id instead of retrying this send.",
		len(stale), strings.Join(stale, ", "))
}

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
			"(3) don't fix other agents' code - DM the owner, "+
			"(4) use worktree for isolated changes.",
		strings.Join(sameWSNames, ", "), selfWS)
}

func (t *LanChatTool) OnInboundDM(fromNodeID string) {
	if t.rateLimiter == nil || t.Hub == nil {
		return
	}
	selfID := t.Hub.NodeID()
	t.rateLimiter.resetDMForPeer(selfID, fromNodeID)
}

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

	if len(nodeID) > 16 {
		return string([]rune(nodeID)[:16]) + "..."
	}
	return nodeID
}

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
		"PRIMARY tool for real-time collaboration with other users and agents on the LAN. " +
		"Do NOT use send_message (for swarm teammates) or a2a_remote (for headless code-edit delegation).\n\n" +
		"ANTINOISE RULES:\n" +
		"- Prefer targeted DMs (action='send') over broadcasts. Only broadcast when the user explicitly asks.\n" +
		"- Do NOT send acknowledgments (\"got it\", \"thanks\"). Do the work silently.\n" +
		"- Check action='list' for agent_busy status before messaging. Don't message busy agents unless urgent.\n" +
		"- One message per task. No follow-up pings - check action='list' or wait.\n\n" +
		"Actions: list, send (DM), broadcast (your team), broadcast_all (everyone), send_team, history, pending, approve, reject, set_identity. " +
		"Nick format: <name>_<role>. Use node_id from list as 'to' field, not nick."
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
				"description": "New nickname for set_identity (e.g. 'alice', 'bob'). Combined with role to form the composite nick 'nick_role'. Optional - only provided fields are changed."
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
				"description": "Direct messages only. 'agent' = deliver to recipient's agent (triggers their approval + agent loop). 'human' = show in recipient's chat panel for the human to read. Default follows as_agent: 'agent' when as_agent=true, 'human' when as_agent=false."
			},
			"as_agent": {
				"type": "boolean",
				"description": "Sender identity. true = send as agent (fromRole=agent). false = send as the local human user. Default: true."
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

	asAgent := true
	if args.AsAgent != nil {
		asAgent = *args.AsAgent
	}

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

func parseNodeIDs(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return dedupStrings(arr)
	}

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

func joinNodeIDs(set map[string]bool) string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

func (t LanChatTool) resolveRecipients(ids []string) ([]string, []string) {
	resolved, ambiguous, _ := t.resolveRecipientsFull(ids)
	return resolved, ambiguous
}

func (t LanChatTool) resolveRecipientsFull(ids []string) ([]string, []string, []string) {
	participants := t.Hub.Participants()

	byNodeID := make(map[string]string)

	byNick := make(map[string]map[string]bool)
	byPrefix := make([]struct{ nick, id string }, 0)

	byTeamRole := make(map[string]map[string]bool)

	for _, p := range participants {
		if p.NodeID == t.Hub.NodeID() {
			continue
		}
		byNodeID[strings.ToLower(p.NodeID)] = p.NodeID
		for _, nick := range []string{p.HumanNick, p.AgentNick} {
			if nick == "" {
				continue
			}
			lower := strings.ToLower(nick)
			if byNick[lower] == nil {
				byNick[lower] = make(map[string]bool)
			}
			byNick[lower][p.NodeID] = true
			byPrefix = append(byPrefix, struct{ nick, id string }{lower, p.NodeID})
		}
		if p.Team != "" && p.Role != "" {
			key := p.Team + "|" + p.Role

			if byTeamRole[key] == nil {
				byTeamRole[key] = make(map[string]bool)
			}
			byTeamRole[key][p.NodeID] = true
		}
	}

	var unresolved []string
	var resolved []string
	var ambiguous []string
	seen := make(map[string]bool)
	for _, raw := range ids {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		lower := strings.ToLower(raw)

		if id, ok := byNodeID[lower]; ok {
			if !seen[id] {
				resolved = append(resolved, id)
				seen[id] = true
			}
			continue
		}

		if idSet, ok := byNick[lower]; ok {
			if len(idSet) > 1 {
				ambiguous = append(ambiguous, fmt.Sprintf("%q matches %d peers with the same nick: %s - address by node_id instead",
					raw, len(idSet), joinNodeIDs(idSet)))
				continue
			}
			for id := range idSet {
				if !seen[id] {
					resolved = append(resolved, id)
					seen[id] = true
				}
			}
			continue
		}
		var prefixHits []string
		scanDedup := map[string]bool{}
		for _, entry := range byPrefix {
			if strings.HasPrefix(entry.nick, lower) && !scanDedup[entry.id] {
				scanDedup[entry.id] = true
				prefixHits = append(prefixHits, entry.id)
			}
		}
		if len(prefixHits) == 1 {
			if !seen[prefixHits[0]] {
				resolved = append(resolved, prefixHits[0])
				seen[prefixHits[0]] = true
			}
			continue
		}
		if len(prefixHits) > 1 {
			ambiguous = append(ambiguous, fmt.Sprintf("%q is a prefix of %d nicknames: %s - use a longer prefix or a node_id",
				raw, len(prefixHits), strings.Join(prefixHits, ", ")))
			continue
		}

		unresolved = append(unresolved, raw)
	}

	var stillUnresolved []string
	for _, raw := range unresolved {

		ap := t.Hub.LookupArchiveByNodeID(raw)
		if ap == nil {

			ap = t.Hub.LookupArchiveByNick(raw)
		}

		if ap != nil && ap.Team != "" && ap.Role != "" {

			key := ap.Team + "|" + ap.Role
			if ids, ok := byTeamRole[key]; ok {
				if len(ids) > 1 {
					ambiguous = append(ambiguous, fmt.Sprintf("archived %q maps to team|role %s shared by %d active peers: %s - address by node_id instead",
						raw, key, len(ids), joinNodeIDs(ids)))
					continue
				}
				for id := range ids {
					if !seen[id] {
						resolved = append(resolved, id)
						seen[id] = true
					}
				}
				continue
			}
		}

		stillUnresolved = append(stillUnresolved, raw)
	}

	return resolved, ambiguous, stillUnresolved
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
		AgentStatus string   `json:"agent_status,omitempty"`
		Model       string   `json:"model,omitempty"`
		Self        bool     `json:"self"`
	}

	var peers []peerInfo
	for _, p := range participants {

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
			AgentStatus: p.AgentStatus,
			Model:       p.Model,
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

func (t LanChatTool) doSend(ctx context.Context, content string, toNodeIDs []string, asAgent bool, toRole string) (Result, error) {
	if content == "" {
		return Result{IsError: true, Content: "message content is required for send"}, nil
	}
	if len(toNodeIDs) == 0 {
		return Result{IsError: true, Content: "recipient 'to' is required for send (use action='list' to find node_id)"}, nil
	}

	for _, id := range toNodeIDs {
		if id == "*" {
			return t.doBroadcastAll(ctx, content, asAgent)
		}
	}

	resolved, ambiguous, unresolvedInputs := t.resolveRecipientsFull(toNodeIDs)
	if len(ambiguous) > 0 {

		hint := t.doList("")
		return Result{IsError: true, Content: fmt.Sprintf(
			"ambiguous recipient(s):\n%s\nUse action='list' and address by node_id.\n%s",
			strings.Join(ambiguous, "\n"), hint.Content)}, nil
	}
	if len(resolved) == 0 {

		hint := t.doList("")
		return Result{IsError: true, Content: fmt.Sprintf(
			"no recipient found for: %s. Use action='list' to see valid node_ids and nicks.\n%s",
			strings.Join(toNodeIDs, ", "), hint.Content)}, nil
	}

	if toRole == "" {
		if asAgent {
			toRole = lanchat.RoleAgent
		} else {
			toRole = lanchat.RoleHuman
		}
	}

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

			return Result{IsError: true, Content: fmt.Sprintf(
				"All recipients are rate-limited:\n%s", strings.Join(rateLimited, "\n"))}, nil
		}
		resolved = allowed
	}

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
		if len(rateLimited) > 0 {
			errMsg += fmt.Sprintf("\nRate-limited recipients skipped:\n%s", strings.Join(rateLimited, "\n"))
		}
		if asAgent {
			errMsg += t.sharedWorkspaceHint(resolved)
		}
		errMsg += t.staleRecipientHint(resolved)
		return Result{IsError: true, Content: errMsg}, nil
	}

	var displayTargets []string
	for _, id := range resolved {
		displayTargets = append(displayTargets, t.displayName(id))
	}
	target := displayTargets[0]
	if len(displayTargets) > 1 {
		target = fmt.Sprintf("%s (%d recipients)", strings.Join(displayTargets, ", "), len(displayTargets))
	}

	result := fmt.Sprintf("Message sent to %s as %s", target, role)

	if len(unresolvedInputs) > 0 {
		result += fmt.Sprintf(" (%d recipient(s) could not be resolved and were skipped: %s)",
			len(unresolvedInputs), strings.Join(unresolvedInputs, ", "))
	}
	if len(errors) > 0 {
		result += fmt.Sprintf(" (errors: %s)", strings.Join(errors, "; "))
	}
	if len(rateLimited) > 0 {
		result += fmt.Sprintf("\nRate-limited recipients skipped:\n%s", strings.Join(rateLimited, "\n"))
	}
	if asAgent {
		result += t.sharedWorkspaceHint(resolved)
	}
	result += t.staleRecipientHint(resolved)
	return Result{Content: result + ".\n"}, nil
}

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

func isUnknownDeliveryErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

const outcomeListCap = 8

func appendOutcomeLines(b *strings.Builder, entries []string) {
	for i, e := range entries {
		if i >= outcomeListCap {
			fmt.Fprintf(b, "  ...and %d more\n", len(entries)-outcomeListCap)
			return
		}
		b.WriteString("  " + e + "\n")
	}
}

func appendUnknownAndFailed(b *strings.Builder, unknown, failed []string, total int) {
	if len(unknown) > 0 {
		fmt.Fprintf(b, "Delivery UNKNOWN for %d/%d recipients (timeout is neither failure nor confirmed success):\n", len(unknown), total)
		appendOutcomeLines(b, unknown)
	}
	if len(failed) > 0 {
		fmt.Fprintf(b, "Definitely failed %d/%d:\n", len(failed), total)
		appendOutcomeLines(b, failed)
	}
}

func (t LanChatTool) doBroadcastAll(ctx context.Context, content string, asAgent bool) (Result, error) {
	if content == "" {
		return Result{IsError: true, Content: "message content is required for broadcast_all"}, nil
	}

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

	toRole := lanchat.RoleHuman
	if asAgent {
		toRole = lanchat.RoleAgent
	}

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

	var queued []string
	var unknown, failed []string
	for _, nodeID := range allowed {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			unknown = append(unknown, fmt.Sprintf("%s: deadline hit before send", t.displayName(nodeID)))
			continue
		}
		var err error
		if asAgent {
			err = t.Hub.SendAsAgent(ctx, nodeID, toRole, content)
		} else {
			err = t.Hub.SendDirect(ctx, nodeID, toRole, content, nil)
		}
		switch {
		case err == nil:
			queued = append(queued, nodeID)
			if asAgent && t.rateLimiter != nil {
				t.rateLimiter.recordDM(selfID, nodeID)
			}
		case isUnknownDeliveryErr(err):
			unknown = append(unknown, fmt.Sprintf("%s: %v", t.displayName(nodeID), err))
		default:
			failed = append(failed, fmt.Sprintf("%s: %v", t.displayName(nodeID), err))
		}
	}

	role := "human"
	if asAgent {
		role = "agent"
	}
	if len(queued) == 0 && len(unknown) == 0 {
		errMsg := fmt.Sprintf("Failed to broadcast to any recipient: %s", strings.Join(failed, "; "))
		if len(rateLimited) > 0 {
			errMsg += fmt.Sprintf("\nRate-limited recipients skipped:\n%s", strings.Join(rateLimited, "\n"))
		}
		if asAgent {
			errMsg += t.sharedWorkspaceHint(allowed)
		}
		return Result{IsError: true, Content: errMsg}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Broadcast queued to %d/%d participants as %s (queued = handed to transport; lanchat is fire-and-forget, no delivery receipt yet).\n", len(queued), len(allPeers), role)
	appendUnknownAndFailed(&b, unknown, failed, len(allPeers))
	if len(rateLimited) > 0 {
		fmt.Fprintf(&b, "Rate-limited recipients skipped:\n%s\n", strings.Join(rateLimited, "\n"))
	}
	result := b.String()
	if asAgent {
		result += t.sharedWorkspaceHint(allowed)
	}
	return Result{Content: result}, nil
}

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
			continue
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

	toRole := lanchat.RoleAgent
	if !asAgent {
		toRole = lanchat.RoleHuman
	}
	var queued []string
	var unknown, failed []string
	for _, nodeID := range members {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			unknown = append(unknown, fmt.Sprintf("%s: deadline hit before send", t.displayName(nodeID)))
			continue
		}
		var err error
		if asAgent {
			err = t.Hub.SendAsAgent(ctx, nodeID, toRole, content)
		} else {
			err = t.Hub.SendDirect(ctx, nodeID, toRole, content, nil)
		}
		switch {
		case err == nil:
			queued = append(queued, nodeID)
			if asAgent && t.rateLimiter != nil {
				t.rateLimiter.recordDM(selfNodeID, nodeID)
			}
		case isUnknownDeliveryErr(err):
			unknown = append(unknown, fmt.Sprintf("%s: %v", t.displayName(nodeID), err))
		default:
			failed = append(failed, fmt.Sprintf("%s: %v", t.displayName(nodeID), err))
		}
	}

	if len(queued) == 0 && len(unknown) == 0 {
		errMsg := fmt.Sprintf(
			"Failed to send to any member of team %q: %s",
			team, strings.Join(failed, "; "),
		)
		if len(rateLimited) > 0 {
			errMsg += fmt.Sprintf("\nRate-limited members skipped:\n%s", strings.Join(rateLimited, "\n"))
		}
		if asAgent {
			errMsg += t.sharedWorkspaceHint(members)
		}
		return Result{IsError: true, Content: errMsg}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Sent (queued, no delivery receipt yet) to %d/%d members of team %q\n", len(queued), len(members), team)
	appendUnknownAndFailed(&b, unknown, failed, len(members))
	if len(rateLimited) > 0 {
		fmt.Fprintf(&b, "Rate-limited members skipped:\n%s\n", strings.Join(rateLimited, "\n"))
	}
	result := b.String()
	if asAgent {
		result += t.sharedWorkspaceHint(members)
	}
	return Result{Content: result}, nil
}

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

	start := len(messages) - limit
	if start < 0 {
		start = 0
	}
	recent := messages[start:]

	var lines []string
	for _, m := range recent {
		ts := time.UnixMilli(m.Timestamp).Format("15:04:05")
		from := m.FromNick
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
		from := p.Message.FromNick
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

	curNick := t.Hub.HumanNick()
	curRole := t.Hub.Role()
	curTeam := t.Hub.Team()

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
