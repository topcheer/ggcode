package tui

import (
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/plugin"
)

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
		out = append(out, truncateString(formatToolInline(toolDisplayName(ts), toolDetail(ts)), max(12, m.sidebarWidth()-6)))
	}
	return out
}
