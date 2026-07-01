package tui

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// lanchatPeerConflictWarning checks if there are lanchat peers in the same
// workspace on the same machine with a busy agent. If so, returns a system
// prompt suffix warning the LLM to coordinate before editing files.
func lanchatPeerConflictWarning(hub *lanchat.Hub, workspace string) string {
	if hub == nil || workspace == "" {
		return ""
	}

	participants := hub.Participants()

	// Find busy peers in the same workspace (excluding self).
	var busyPeers []string
	selfNodeID := hub.NodeID()
	for _, p := range participants {
		if p.NodeID == selfNodeID {
			continue
		}
		if !p.Online {
			continue
		}
		// Same workspace path = potential file conflict
		if p.Workspace != workspace {
			continue
		}
		// Same machine: node IDs share the same hostname prefix
		// (format: hostname-...). We check by comparing the machine
		// identifier portion of the node ID.
		if !sameMachine(p.NodeID, selfNodeID) {
			continue
		}
		if p.AgentBusy {
			name := p.AgentNick
			if name == "" {
				name = p.HumanNick
			}
			busyPeers = append(busyPeers, name)
		}
	}

	if len(busyPeers) == 0 {
		return ""
	}

	return fmt.Sprintf(
		"⚠️ COLLABORATION WARNING: %d agent(s) are currently active in the same workspace: %s\n"+
			"Before writing or editing any file, use the lanchat tool to DM the active agent(s) "+
			"and confirm which files they are modifying to avoid conflicting edits.",
		len(busyPeers), strings.Join(busyPeers, ", "),
	)
}

// sameMachine checks if two node IDs belong to the same physical machine.
// Node ID format: "hostname-pid-random" — we compare the hostname portion.
func sameMachine(nodeA, nodeB string) bool {
	a := machineName(nodeA)
	b := machineName(nodeB)
	return a != "" && a == b
}

// machineName extracts the hostname from a node ID.
// Node ID format examples:
//
//	ggcode-Mac-Studio-18001.local-4938-1782899234709588000
//	fluui-Mac-Studio-18001.local-12297-1782905015801622000
//
// The hostname is everything before the last two dash-separated numeric segments.
func machineName(nodeID string) string {
	if nodeID == "" {
		return ""
	}
	// Split on "-" and rejoin everything except the last 2 segments
	// (pid + random number). This handles hostnames that contain dashes.
	parts := strings.Split(nodeID, "-")
	if len(parts) <= 2 {
		return nodeID
	}
	return strings.Join(parts[:len(parts)-2], "-")
}
