package acp

import (
	"context"
	"fmt"
	"github.com/topcheer/ggcode/internal/debug"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/tool"
)

type MCPManager struct {
	clients  []*mcp.Client
	adapters []*mcp.Adapter
	registry *tool.Registry
}

func NewMCPManager(registry *tool.Registry) *MCPManager {
	return &MCPManager{
		registry: registry,
	}
}

func (m *MCPManager) ConnectServers(ctx context.Context, servers []MCPServer) error {
	var failures []string
	for _, srv := range servers {
		if err := m.connectServer(ctx, srv); err != nil {
			debug.Log("acp", "failed to connect MCP server %q: %v", srv.Name, err)
			failures = append(failures, fmt.Sprintf("%s: %v", srv.Name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("failed to connect %d MCP server(s): %s", len(failures), strings.Join(failures, "; "))
	}
	return nil
}

func (m *MCPManager) connectServer(ctx context.Context, srv MCPServer) error {
	cfgSrv := acpMCPServerToConfig(srv)

	client := mcp.NewClientFromConfig(cfgSrv)
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("starting MCP server %q: %w", srv.Name, err)
	}

	if _, err := client.Initialize(ctx); err != nil {
		client.Close()
		return fmt.Errorf("initializing MCP server %q: %w", srv.Name, err)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		debug.Log("acp", "failed to list tools from %q: %v", srv.Name, err)
	} else {
		var adapter *mcp.Adapter
		if srv.ReadOnly {
			adapter = mcp.NewReadOnlyAdapter(srv.Name, client, tools)
		} else {
			adapter = mcp.NewAdapter(srv.Name, client, tools)
		}
		adapter.SetServerInstructions(client.Instructions())
		if err := adapter.RegisterTools(m.registry); err != nil {
			debug.Log("acp", "failed to register tools from %q: %v", srv.Name, err)
		}
		m.adapters = append(m.adapters, adapter)
	}

	m.clients = append(m.clients, client)
	return nil
}

func (m *MCPManager) Close() error {
	var firstErr error
	for _, a := range m.adapters {
		for _, tn := range a.RegisteredNames() {
			m.registry.Unregister(tn)
		}
	}
	for _, c := range m.clients {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.clients = nil
	m.adapters = nil
	return firstErr
}

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
	if transportType == "" && srv.URL != "" {
		transportType = "http"
	}
	switch strings.ToLower(transportType) {
	case "sse", "streamable-http", "streamable_http", "https":
		transportType = "http"
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
