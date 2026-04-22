package acp

import (
	"context"
	"fmt"
	"os"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/tool"
)

// MCPManager manages dynamically connected MCP servers for ACP sessions.
// It connects servers, discovers their tools, registers them in the tool registry,
// and provides cleanup on Close().
type MCPManager struct {
	clients  []*mcp.Client
	adapters []*mcp.Adapter
	registry *tool.Registry
}

// NewMCPManager creates a new MCP manager.
func NewMCPManager(registry *tool.Registry) *MCPManager {
	return &MCPManager{
		registry: registry,
	}
}

// ConnectServers starts and connects the given MCP servers.
// Each server is initialized, its tools are discovered via tools/list,
// and registered in the tool registry with "mcp__" prefix.
func (m *MCPManager) ConnectServers(ctx context.Context, servers []MCPServer) error {
	for _, srv := range servers {
		if err := m.connectServer(ctx, srv); err != nil {
			fmt.Fprintf(os.Stderr, "acp: warning: failed to connect MCP server %q: %v\n", srv.Name, err)
			// Non-fatal: continue with other servers
		}
	}
	return nil
}

// connectServer connects a single MCP server, initializes it, and registers tools.
func (m *MCPManager) connectServer(ctx context.Context, srv MCPServer) error {
	cfgSrv := acpMCPServerToConfig(srv)

	client := mcp.NewClientFromConfig(cfgSrv)
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("starting MCP server %q: %w", srv.Name, err)
	}

	// Initialize the MCP session
	if _, err := client.Initialize(ctx); err != nil {
		client.Close()
		return fmt.Errorf("initializing MCP server %q: %w", srv.Name, err)
	}

	// Discover tools
	tools, err := client.ListTools(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acp: warning: failed to list tools from %q: %v\n", srv.Name, err)
		// Continue — server may not support tools
	} else {
		// Register tools via adapter
		adapter := mcp.NewAdapter(srv.Name, client, tools)
		if err := adapter.RegisterTools(m.registry); err != nil {
			fmt.Fprintf(os.Stderr, "acp: warning: failed to register tools from %q: %v\n", srv.Name, err)
		}
		m.adapters = append(m.adapters, adapter)
	}

	m.clients = append(m.clients, client)
	return nil
}

// Close shuts down all connected MCP servers.
func (m *MCPManager) Close() error {
	var firstErr error
	for _, c := range m.clients {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.clients = nil
	m.adapters = nil
	return firstErr
}

// acpMCPServerToConfig converts an ACP MCPServer to a config.MCPServerConfig.
func acpMCPServerToConfig(srv MCPServer) config.MCPServerConfig {
	env := make(map[string]string)
	for _, e := range srv.Env {
		env[e.Name] = e.Value
	}

	headers := make(map[string]string)
	for _, h := range srv.Headers {
		headers[h.Name] = h.Value
	}

	transportType := srv.Type
	if transportType == "" && srv.Command != "" {
		transportType = "stdio"
	}

	return config.MCPServerConfig{
		Name:    srv.Name,
		Command: srv.Command,
		Args:    srv.Args,
		Env:     env,
		URL:     srv.URL,
		Headers: headers,
		Type:    transportType,
	}
}
