package acp

import (
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

func mcpServersFromConfig(configs []config.MCPServerConfig) []MCPServer {
	if len(configs) == 0 {
		return nil
	}
	servers := make([]MCPServer, 0, len(configs))
	for _, cfg := range configs {
		server, ok := configMCPServerToACP(cfg)
		if !ok {
			continue
		}
		servers = append(servers, server)
	}
	return servers
}

func configMCPServerToACP(cfg config.MCPServerConfig) (MCPServer, bool) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		debug.Log("acp-client", "skipping MCP passthrough server with empty name")
		return MCPServer{}, false
	}
	if cfg.OAuthClientID != "" || cfg.OAuthClientSecret != "" {
		debug.Log("acp-client", "skipping MCP passthrough server %q: OAuth config is not supported by ACP mcpServers", name)
		return MCPServer{}, false
	}

	transport := strings.ToLower(strings.TrimSpace(cfg.Type))
	if transport == "" {
		transport = "stdio"
	}

	server := MCPServer{
		Name:    name,
		Type:    transport,
		Command: cfg.Command,
		URL:     cfg.URL,
	}
	if len(cfg.Args) > 0 {
		server.Args = append([]string(nil), cfg.Args...)
	}
	if env := envVariablesFromMap(cfg.Env); len(env) > 0 {
		server.Env = env
	}
	if headers := httpHeadersFromMap(cfg.Headers); len(headers) > 0 {
		server.Headers = headers
	}

	switch transport {
	case "stdio":
		if strings.TrimSpace(server.Command) == "" {
			debug.Log("acp-client", "skipping MCP passthrough server %q: stdio transport requires command", name)
			return MCPServer{}, false
		}
	case "http", "sse":
		if strings.TrimSpace(server.URL) == "" {
			debug.Log("acp-client", "skipping MCP passthrough server %q: %s transport requires URL", name, transport)
			return MCPServer{}, false
		}
	case "ws", "websocket":
		debug.Log("acp-client", "skipping MCP passthrough server %q: ACP mcpServers does not support websocket transport", name)
		return MCPServer{}, false
	default:
		debug.Log("acp-client", "skipping MCP passthrough server %q: unsupported transport %q", name, transport)
		return MCPServer{}, false
	}

	return server, true
}

func envVariablesFromMap(values map[string]string) []EnvVariable {
	if len(values) == 0 {
		return nil
	}
	keys := sortedMapKeys(values)
	env := make([]EnvVariable, 0, len(keys))
	for _, key := range keys {
		env = append(env, EnvVariable{Name: key, Value: values[key]})
	}
	return env
}

func httpHeadersFromMap(values map[string]string) []HTTPHeader {
	if len(values) == 0 {
		return nil
	}
	keys := sortedMapKeys(values)
	headers := make([]HTTPHeader, 0, len(keys))
	for _, key := range keys {
		headers = append(headers, HTTPHeader{Name: key, Value: values[key]})
	}
	return headers
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
