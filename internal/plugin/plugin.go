package plugin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/tool"
)

// Plugin is the interface that external tool plugins must implement.
// Plugins provide one or more tools that get registered in the tool registry.
type Plugin interface {
	// Name returns a unique identifier for this plugin.
	Name() string

	// Tools returns the list of tools this plugin provides.
	Tools() []tool.Tool

	// Init initializes the plugin with optional configuration.
	// Called once after loading, before tools are registered.
	Init(config map[string]interface{}) error
}

// LoadResult describes the outcome of loading a single plugin.
type LoadResult struct {
	Name    string
	Success bool
	Tools   []string
	Error   error
}

// Manager handles loading, initializing, and tracking plugins.
type Manager struct {
	plugins []Plugin
	results []LoadResult
}

// NewManager creates a new plugin manager.
func NewManager() *Manager {
	return &Manager{}
}

// Plugins returns all successfully loaded plugins.
func (m *Manager) Plugins() []Plugin {
	return m.plugins
}

// Results returns the load results for all plugins (including failures).
func (m *Manager) Results() []LoadResult {
	return m.results
}

// AddPlugin adds a pre-initialized plugin to the manager.
func (m *Manager) AddPlugin(p Plugin) {
	m.plugins = append(m.plugins, p)
	toolNames := make([]string, len(p.Tools()))
	for i, t := range p.Tools() {
		toolNames[i] = t.Name()
	}
	m.results = append(m.results, LoadResult{
		Name: p.Name(), Success: true, Tools: toolNames,
	})
}

// RegisterTools registers all plugin tools into the given registry.
func (m *Manager) RegisterTools(registry *tool.Registry) error {
	for _, p := range m.plugins {
		for _, t := range p.Tools() {
			if err := registry.Register(t); err != nil {
				return err
			}
		}
	}
	return nil
}

// CommandTool wraps an external command as a tool.Tool.
type CommandTool struct {
	name        string
	description string
	execute     string
	args        []string
}

// NewCommandTool creates a tool that runs an external command.
func NewCommandTool(name, description, execute string, args []string) *CommandTool {
	return &CommandTool{
		name:        name,
		description: description,
		execute:     execute,
		args:        args,
	}
}

func (c *CommandTool) Name() string        { return c.name }
func (c *CommandTool) Description() string { return c.description }
func (c *CommandTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"args":{"type":"string","description":"Arguments to pass to the command"}},"required":[]}`)
}

func (c *CommandTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	// Input is optional; tool can work with no input
	return tool.Result{
		Content: fmt.Sprintf("Command tool %q executed (placeholder)", c.name),
		IsError: false,
	}, nil
}

// commandPlugin is a plugin that wraps one or more CommandTools.
type commandPlugin struct {
	name  string
	tools []tool.Tool
}

func (p *commandPlugin) Name() string                   { return p.name }
func (p *commandPlugin) Tools() []tool.Tool              { return p.tools }
func (p *commandPlugin) Init(config map[string]interface{}) error { return nil }
