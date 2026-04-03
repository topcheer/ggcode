package plugin

import (
	"context"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/tool"
)

// MCPPlugin connects to an MCP server and registers its tools.
type MCPPlugin struct {
	name      string
	command   string
	args      []string
	env       map[string]string
	adapter   *mcp.Adapter
	mu        sync.RWMutex
	connected bool
}

// NewMCPPlugin creates a plugin from an MCP server configuration.
func NewMCPPlugin(cfg config.MCPServerConfig) *MCPPlugin {
	return &MCPPlugin{
		name:    cfg.Name,
		command: cfg.Command,
		args:    cfg.Args,
		env:     cfg.Env,
	}
}

func (m *MCPPlugin) Name() string { return m.name }

// Connect initializes the MCP server, discovers tools, and returns an adapter.
func (m *MCPPlugin) Connect(ctx context.Context) (*mcp.Adapter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.adapter != nil {
		return m.adapter, nil
	}

	client := mcp.NewClient(m.name, m.command, m.args)
	if err := client.Start(ctx); err != nil {
		return nil, err
	}

	initResult, err := client.Initialize(ctx)
	if err != nil {
		client.Close()
		return nil, err
	}
	_ = initResult

	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return nil, err
	}

	client.Close()

	m.adapter = mcp.NewAdapter(m.name, m.command, m.args, tools)
	m.connected = true
	return m.adapter, nil
}

// RegisterTools discovers MCP tools and registers them into the registry.
func (m *MCPPlugin) RegisterTools(ctx context.Context, registry *tool.Registry) error {
	adapter, err := m.Connect(ctx)
	if err != nil {
		return err
	}
	return adapter.RegisterTools(registry)
}

// Adapter returns the MCP adapter (nil if not connected).
func (m *MCPPlugin) Adapter() *mcp.Adapter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.adapter
}

// IsConnected returns whether the MCP server has been successfully contacted.
func (m *MCPPlugin) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected
}

// Tools returns the registered tool names (requires prior Connect).
func (m *MCPPlugin) Tools() []tool.Tool {
	return nil
}

func (m *MCPPlugin) Init(cfg map[string]interface{}) error {
	return nil
}
