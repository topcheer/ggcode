package tui

import (
	"github.com/topcheer/ggcode/internal/util"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/plugin"
)

// mcpServersUpdatedMsg refreshes the TUI's MCP server list after a
// background state change (connect/reconnect/disconnect). Without it the
// panel keeps showing a stale "reconnecting ..." message forever even when
// the server actually connected - the manager's emitUpdate had no listener
// in the TUI before this message existed.
type mcpServersUpdatedMsg struct {
	servers []MCPInfo
}

// applyMCPServersUpdate swaps in the fresh server list and, when the panel
// is waiting on a reconnect, upgrades the static "reconnecting" message to
// a terminal result the moment that server settles.
func (m *Model) applyMCPServersUpdate(msg mcpServersUpdatedMsg) {
	m.mcpServers = msg.servers
	if m.mcpPanel == nil || m.mcpPanel.pendingReconnect == "" {
		return
	}
	name := m.mcpPanel.pendingReconnect
	for _, srv := range msg.servers {
		if srv.Name != name {
			continue
		}
		switch {
		case srv.Connected:
			m.mcpPanel.message = m.t("panel.mcp.reconnected", name, len(srv.ToolNames))
			m.mcpPanel.pendingReconnect = ""
		case !srv.Pending && srv.Error != "":
			m.mcpPanel.message = m.t("panel.mcp.reconnect_error", name, srv.Error)
			m.mcpPanel.pendingReconnect = ""
		}
		// Still pending (dialing or awaiting OAuth): keep the reconnecting
		// message until a terminal state arrives.
		return
	}
}

func toMCPInfos(infos []plugin.MCPServerInfo) []MCPInfo {
	out := make([]MCPInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, MCPInfo{
			Name:          info.Name,
			ToolNames:     normalizeMCPToolNames(info.ToolNames),
			PromptNames:   append([]string(nil), info.PromptNames...),
			ResourceNames: append([]string(nil), info.ResourceNames...),
			Connected:     info.Status == plugin.MCPStatusConnected,
			Pending:       info.Status == plugin.MCPStatusPending,
			Error:         info.Error,
			Transport:     info.Transport,
			Migrated:      info.Migrated,
			Disabled:      info.Disabled,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func normalizeMCPToolNames(toolNames []string) []string {
	out := make([]string, 0, len(toolNames))
	for _, name := range toolNames {
		out = append(out, displayMCPToolName(name))
	}
	sort.Strings(out)
	return out
}

func displayMCPToolName(name string) string {
	if !strings.HasPrefix(name, "mcp__") {
		return name
	}
	parts := strings.SplitN(name, "__", 3)
	if len(parts) == 3 && parts[2] != "" {
		return parts[2]
	}
	return name
}

func (m *Model) updateActiveMCPTools(ts ToolStatusMsg) {
	if !strings.HasPrefix(ts.ToolName, "mcp__") {
		return
	}
	if m.activeMCPTools == nil {
		m.activeMCPTools = make(map[string]ToolStatusMsg)
	}
	key := ts.ToolName
	if ts.Running {
		m.activeMCPTools[key] = ts
		return
	}
	delete(m.activeMCPTools, key)
}

func (m Model) activeMCPToolSummaries() []string {
	if len(m.activeMCPTools) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m.activeMCPTools))
	for key := range m.activeMCPTools {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		ts := m.activeMCPTools[key]
		out = append(out, util.Truncate(formatToolInline(toolDisplayName(ts), toolDetail(ts)), max(12, m.sidebarWidth()-6)))
	}
	return out
}
