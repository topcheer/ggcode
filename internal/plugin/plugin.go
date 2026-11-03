package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/topcheer/ggcode/internal/tool"
)

type Plugin interface {
	Name() string

	Tools() []tool.Tool

	Init(config map[string]interface{}) error
}

type LoadResult struct {
	Name    string
	Success bool
	Tools   []string
	Error   error
}

type Manager struct {
	plugins []Plugin
	results []LoadResult
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Plugins() []Plugin {
	return m.plugins
}

func (m *Manager) Results() []LoadResult {
	return m.results
}

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

type CommandTool struct {
	name        string
	description string
	execute     string
	args        []string
}

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
	var params struct {
		Args string `json:"args"`
	}
	if err := json.Unmarshal(input, &params); err != nil && len(input) > 0 {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}

	args := append([]string(nil), c.args...)
	if params.Args != "" {
		args = append(args, strings.Fields(params.Args)...)
	}

	cmd := exec.CommandContext(ctx, c.execute, args...)

	setupProcessGroupCancel(cmd)

	var out []byte
	const maxPluginOutput = 1 << 20
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("Command %q failed to create pipe: %v", c.name, err), IsError: true}, nil
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return tool.Result{
			Content: fmt.Sprintf("Command %q failed to start: %v", c.name, err),
			IsError: true,
		}, nil
	}
	out, err = io.ReadAll(io.LimitReader(pipe, maxPluginOutput+1))
	cmdErr := cmd.Wait()
	if int64(len(out)) > maxPluginOutput {
		out = append(out[:maxPluginOutput], []byte("\n...[output truncated at 1MB]")...)
	}
	if cmdErr != nil {
		return tool.Result{
			Content: fmt.Sprintf("Command %q failed: %s\n%s", c.name, cmdErr, string(out)),
			IsError: true,
		}, nil
	}
	return tool.Result{
		Content: string(out),
		IsError: false,
	}, nil
}

type commandPlugin struct {
	name  string
	tools []tool.Tool
}

func (p *commandPlugin) Name() string                             { return p.name }
func (p *commandPlugin) Tools() []tool.Tool                       { return p.tools }
func (p *commandPlugin) Init(config map[string]interface{}) error { return nil }
