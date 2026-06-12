package wailskit

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/plugin"
)

// MCPServerInfo is a frontend-friendly representation of an MCP server config.
type MCPServerInfo struct {
	Name          string            `json:"name"`
	Type          string            `json:"type,omitempty"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Status        string            `json:"status,omitempty"`
	Error         string            `json:"error,omitempty"`
	Disabled      bool              `json:"disabled,omitempty"`
	Connected     bool              `json:"connected,omitempty"`
	OAuthRequired bool              `json:"oauthRequired,omitempty"`
}

// ListMCPServers returns all configured MCP servers.
func ListMCPServers() ([]MCPServerInfo, error) {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if len(cfg.MCPServers) == 0 {
		return nil, nil
	}

	result := make([]MCPServerInfo, 0, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		result = append(result, MCPServerInfo{
			Name:     s.Name,
			Type:     s.Type,
			Command:  s.Command,
			Args:     s.Args,
			Env:      s.Env,
			URL:      s.URL,
			Headers:  s.Headers,
			Status:   "unknown",
			Disabled: plugin.MCPDisabled(s.Name),
		})
	}
	globalMu.RLock()
	chat := activeChatBridge
	globalMu.RUnlock()
	if chat == nil || chat.mcpManager == nil {
		return result, nil
	}
	snapshot := chat.mcpManager.Snapshot()
	byName := make(map[string]plugin.MCPServerInfo, len(snapshot))
	for _, info := range snapshot {
		byName[info.Name] = info
	}
	for i := range result {
		if info, ok := byName[result[i].Name]; ok {
			result[i].Status = string(info.Status)
			result[i].Error = info.Error
			result[i].Disabled = info.Disabled
			result[i].Connected = info.Status == plugin.MCPStatusConnected
			result[i].OAuthRequired = info.OAuthRequired
		}
	}
	return result, nil
}

func SetMCPServerEnabled(name string, enabled bool) bool {
	disabled := !enabled
	plugin.SetMCPDisabled(name, disabled)
	globalMu.RLock()
	chat := activeChatBridge
	globalMu.RUnlock()
	if chat == nil || chat.mcpManager == nil {
		return false
	}
	if disabled {
		return chat.mcpManager.Disconnect(name)
	}
	return chat.mcpManager.Reconnect(name)
}

func ReconnectMCPServer(name string) bool {
	globalMu.RLock()
	chat := activeChatBridge
	globalMu.RUnlock()
	if chat == nil || chat.mcpManager == nil {
		return false
	}
	return chat.mcpManager.Reconnect(name)
}

// AddMCPServer adds a new MCP server configuration.
// The values map may contain:
//   - "name" (required): server name
//   - "type": "stdio", "http", "ws" (default: "stdio")
//   - "command": executable command (for stdio type)
//   - "args": space-separated arguments (for stdio type)
//   - "url": server URL (for http/ws type)
//   - "headers_*": HTTP headers (keys like "headers_Authorization")
//   - "env_*": environment variables (keys like "env_KEY")
func AddMCPServer(values map[string]string) error {
	cfg, err := config.Load(config.ConfigPath())
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

	// Parse args from space-separated string
	if argsStr := values["args"]; argsStr != "" {
		serverCfg.Args = strings.Fields(argsStr)
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

	// Parse headers_ prefixed keys into headers map
	headers := make(map[string]string)
	for k, v := range values {
		if len(k) > 8 && k[:8] == "headers_" {
			headers[k[8:]] = v
		}
	}
	if len(headers) > 0 {
		serverCfg.Headers = headers
	}

	cfg.UpsertMCPServer(serverCfg)
	return cfg.Save()
}

// RemoveMCPServer removes an MCP server by name.
func RemoveMCPServer(name string) error {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.RemoveMCPServer(name) {
		return fmt.Errorf("MCP server %q not found", name)
	}
	return cfg.Save()
}
