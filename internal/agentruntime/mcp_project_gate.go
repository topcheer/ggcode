package agentruntime

import (
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
)

func applyProjectMCPGate(servers []config.MCPServerConfig, workingDir string) ([]config.MCPServerConfig, []string) {
	allowed, _, warnings := mcp.GateProjectServers(servers, workingDir)
	return allowed, warnings
}
