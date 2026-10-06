package agentruntime

import (
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
)

// applyProjectMCPGate filters workspace .mcp.json servers (Source
// "claude-project") down to the user-approved set for this workspace.
// Blocked entries come back as human-readable warnings; callers log or
// surface them. See internal/mcp/project_gate.go for the approval store
// and the GGCODE_ALLOW_PROJECT_MCP bypass.
func applyProjectMCPGate(servers []config.MCPServerConfig, workingDir string) ([]config.MCPServerConfig, []string) {
	allowed, _, warnings := mcp.GateProjectServers(servers, workingDir)
	return allowed, warnings
}
