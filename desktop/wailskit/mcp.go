package wailskit

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/config"
)

// MCPServerInfo is a frontend-friendly representation of an MCP server config.
type MCPServerInfo struct {
	Name    string            `json:"name"`
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ListMCPServers returns all configured MCP servers.
func ListMCPServers() ([]MCPServerInfo, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if len(cfg.MCPServers) == 0 {
		return nil, nil
	}

	result := make([]MCPServerInfo, 0, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		result = append(result, MCPServerInfo{
			Name:    s.Name,
			Type:    s.Type,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			URL:     s.URL,
			Headers: s.Headers,
		})
	}
	return result, nil
}

// AddMCPServer adds a new MCP server configuration.
// The cfg map may contain:
//   - "name" (required): server name
//   - "type": "stdio" or "sse" (default: "stdio")
//   - "command": executable command (for stdio type)
//   - "url": server URL (for sse/streamable type)
//   - "args_*": positional arguments (keys like "args_0", "args_1")
//   - "env_*": environment variables (keys like "env_KEY=VALUE")
func AddMCPServer(values map[string]string) error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	name := values["name"]
	if name == "" {
		return fmt.Errorf("name is required")
	}

	serverType := values["type"]
	if serverType == "" {
		serverType = "stdio"
	}

	serverCfg := config.MCPServerConfig{
		Name:    name,
		Type:    serverType,
		Command: values["command"],
		URL:     values["url"],
	}

	// Parse env_ prefixed keys into env map
	env := make(map[string]string)
	for k, v := range values {
		if len(k) > 4 && k[:4] == "env_" {
			env[k[4:]] = v
		}
	}
	if len(env) > 0 {
		serverCfg.Env = env
	}

	cfg.UpsertMCPServer(serverCfg)
	return cfg.Save()
}

// RemoveMCPServer removes an MCP server by name.
func RemoveMCPServer(name string) error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.RemoveMCPServer(name) {
		return fmt.Errorf("MCP server %q not found", name)
	}
	return cfg.Save()
}
