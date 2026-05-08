package main

import (
	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/webui"
)

// mcpSnapshotToWebUI converts MCP server snapshots to webui status map.
func mcpSnapshotToWebUI(snapshot []plugin.MCPServerInfo) map[string]webui.MCPRuntimeStatus {
	m := make(map[string]webui.MCPRuntimeStatus, len(snapshot))
	for _, info := range snapshot {
		m[info.Name] = webui.MCPRuntimeStatus{
			Connected: string(info.Status) == "connected",
			Pending:   string(info.Status) == "pending",
			Disabled:  info.Disabled,
			Error:     info.Error,
			Tools:     info.ToolNames,
		}
	}
	return m
}

// a2aInstancesToWebUI converts A2A discovered instances to webui format.
func a2aInstancesToWebUI(instances []a2a.InstanceInfo) []webui.A2ADiscoveredInstance {
	result := make([]webui.A2ADiscoveredInstance, 0, len(instances))
	for _, inst := range instances {
		result = append(result, webui.A2ADiscoveredInstance{
			ID:        inst.ID,
			Workspace: inst.Workspace,
			Endpoint:  inst.Endpoint,
			Status:    inst.Status,
			StartedAt: inst.StartedAt,
		})
	}
	return result
}

// imSnapshotToWebUI converts IM adapter states to webui status list.
func imSnapshotToWebUI(snap im.StatusSnapshot, cfg *config.Config) []webui.IMRuntimeStatus {
	out := make([]webui.IMRuntimeStatus, 0, len(snap.Adapters))
	disabledSet := map[string]bool{}
	for _, b := range snap.DisabledBindings {
		disabledSet[b.Adapter] = true
	}
	// Config is the source of truth for disabled state.
	configDisabledSet := map[string]bool{}
	if cfg != nil {
		for name, acfg := range cfg.IM.Adapters {
			if !acfg.Enabled {
				configDisabledSet[name] = true
			}
		}
	}
	mutedSet := map[string]bool{}
	for _, b := range snap.MutedBindings {
		mutedSet[b.Adapter] = true
	}
	for _, a := range snap.Adapters {
		out = append(out, webui.IMRuntimeStatus{
			Adapter:   a.Name,
			Platform:  string(a.Platform),
			Healthy:   a.Healthy,
			Status:    a.Status,
			LastError: a.LastError,
			Muted:     !configDisabledSet[a.Name] && mutedSet[a.Name],
			Disabled:  configDisabledSet[a.Name] || disabledSet[a.Name],
		})
	}
	return out
}
