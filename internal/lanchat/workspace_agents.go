package lanchat

import (
	"fmt"
	"sort"
	"strings"
)

// maxWorkspaceAgentLines bounds the injected list so very large fleets
// cannot balloon the system prompt.
const maxWorkspaceAgentLines = 16

// FormatWorkspaceAgents renders a concise, deterministic system-prompt
// section listing OTHER online agents working in the same workspace.
//
// Prompt-cache stability contract (violating any bullet produces
// per-rebuild prompt drift and invalidates the provider KV-cache prefix):
//   - the same input set always renders byte-identical text regardless of
//     slice order: entries are sorted by AgentNick, then NodeID (byte order);
//   - fixed line shape and field order; no timestamps, durations, or
//     sequence numbers - only fields that are semantically stable;
//   - same-workspace means an exact match or a "/"-boundary prefix in
//     either direction, so agents working in `.ggcode/worktrees/*`
//     beneath this checkout count as same workspace;
//   - the local node (selfNodeID) and offline participants are excluded;
//   - at most maxWorkspaceAgentLines entries; the overflow collapses into
//     a single stable summary line.
//
// It returns "" when no same-workspace peers are online; the caller then
// injects nothing, so solo workspaces see zero prompt drift.
func FormatWorkspaceAgents(participants []Participant, workspace, selfNodeID string) string {
	peers := make([]Participant, 0, len(participants))
	for _, p := range participants {
		if p.NodeID == selfNodeID || !p.Online {
			continue
		}
		nick := strings.TrimSpace(p.AgentNick)
		if nick == "" || !sameWorkspacePath(p.Workspace, workspace) {
			continue
		}
		p.AgentNick = nick
		peers = append(peers, p)
	}
	if len(peers) == 0 {
		return ""
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].AgentNick != peers[j].AgentNick {
			return peers[i].AgentNick < peers[j].AgentNick
		}
		return peers[i].NodeID < peers[j].NodeID
	})

	var sb strings.Builder
	sb.WriteString("## Shared workspace agents\n\n")
	sb.WriteString("Other agents are active in this workspace (startup snapshot; use `lanchat action=list` for live state). Follow the Shared workspace safety rules: work in an isolated worktree for changes, stage only your own files, and DM the owner before touching their in-flight files.\n")
	shown := peers
	hidden := 0
	if len(peers) > maxWorkspaceAgentLines {
		shown = peers[:maxWorkspaceAgentLines]
		hidden = len(peers) - maxWorkspaceAgentLines
	}
	for _, p := range shown {
		status := "idle"
		if p.AgentBusy {
			status = "busy"
		}
		team := p.Team
		if team == "" {
			team = "no-team"
		}
		fmt.Fprintf(&sb, "- %s (%s): %s\n", p.AgentNick, team, status)
	}
	if hidden > 0 {
		fmt.Fprintf(&sb, "- (+%d more; see lanchat list)\n", hidden)
	}
	return sb.String()
}

// sameWorkspacePath reports whether two workspace paths denote the same
// workspace: identical, or one is a direct subdirectory of the other.
// Pure string comparison — deterministic, no filesystem access.
func sameWorkspacePath(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
