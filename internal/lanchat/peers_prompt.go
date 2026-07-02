package lanchat

import (
	"fmt"
	"sort"
	"strings"
)

// lanchatPeersInfo builds a dynamic system prompt section listing all online
// lanchat peers. It shows both busy and idle agents, and specially marks peers
// in the same workspace to highlight collaboration opportunities and
// file-edit conflict risks.
func FormatPeersInfo(hub *Hub, workspace string) string {
	if hub == nil || workspace == "" {
		return ""
	}

	participants := hub.Participants()
	selfNodeID := hub.NodeID()

	type peerEntry struct {
		name      string
		workspace string
		role      string
		team      string
		languages string
		busy      bool
		sameWS    bool
	}

	var peers []peerEntry
	for _, p := range participants {
		if p.NodeID == selfNodeID || !p.Online {
			continue
		}
		name := p.AgentNick
		if name == "" {
			name = p.HumanNick
		}
		if name == "" {
			name = p.NodeID
		}
		peers = append(peers, peerEntry{
			name:      name,
			workspace: p.Workspace,
			role:      p.Role,
			team:      p.Team,
			languages: strings.Join(p.Languages, "+"),
			busy:      p.AgentBusy,
			sameWS:    p.Workspace == workspace,
		})
	}

	if len(peers) == 0 {
		return ""
	}

	// Sort: same-workspace first, then by name.
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].sameWS != peers[j].sameWS {
			return peers[i].sameWS
		}
		return peers[i].name < peers[j].name
	})

	var sb strings.Builder
	sb.WriteString("## LAN Chat Peers\n\n")
	sb.WriteString("The following ggcode instance(s) are online. Use the `lanchat` tool to communicate with them.\n\n")

	formatEntry := func(p peerEntry) string {
		status := "ready"
		if p.busy {
			status = "busy"
		}
		langs := p.languages
		if langs == "" {
			langs = "-"
		}
		return fmt.Sprintf("- %s (%s) — %s [team=%s, role=%s, langs=%s]",
			p.name, p.workspace, status, p.team, p.role, langs)
	}

	var sameWSBusy, sameWSIdle, others []peerEntry
	for _, p := range peers {
		if p.sameWS {
			if p.busy {
				sameWSBusy = append(sameWSBusy, p)
			} else {
				sameWSIdle = append(sameWSIdle, p)
			}
		} else {
			others = append(others, p)
		}
	}

	if len(sameWSBusy) > 0 {
		sb.WriteString("⚠️ ACTIVE in YOUR workspace — coordinate before editing files:\n")
		for _, p := range sameWSBusy {
			sb.WriteString(formatEntry(p))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(sameWSIdle) > 0 {
		sb.WriteString("Available in your workspace (idle, safe for parallel work):\n")
		for _, p := range sameWSIdle {
			sb.WriteString(formatEntry(p))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(others) > 0 {
		sb.WriteString("Other workspaces:\n")
		for _, p := range others {
			sb.WriteString(formatEntry(p))
			sb.WriteString("\n")
		}
	}

	// Collaboration rules — only shown when peers exist.
	sb.WriteString("\nCollaboration rules:\n")
	if len(sameWSBusy) > 0 || len(sameWSIdle) > 0 {
		sb.WriteString("- Before git commit/push: lanchat action='list' to check no teammate is mid-commit — concurrent commits cause ref lock failures.\n")
		sb.WriteString("- Never git stash/checkout/reset in a shared workspace — it clobbers others' uncommitted changes.\n")
		sb.WriteString("- Only edit files you own in this session; if another agent's file blocks your build, DM them instead of fixing it yourself.\n")
		sb.WriteString("- For 3+ independent tasks: distribute to idle teammates (lanchat DM) rather than doing everything sequentially.\n")
		sb.WriteString("- When you finish a task someone asked for: DM them the result (one concise message, no \"done?\" pings).\n")
	} else {
		sb.WriteString("- For cross-workspace questions: DM the specific person (action='send'). For task delegation: use a2a_remote.\n")
		sb.WriteString("- Check agent_busy before messaging — busy agents will see your message after their current task.\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}
