package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

type toolCaller interface {
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error)
}

// Adapter wraps MCP tools into ggcode's Tool interface.
type Adapter struct {
	serverName string
	caller     toolCaller
	tools      []ToolDefinition
	mu         sync.Mutex
}

// NewAdapter creates an MCP adapter from server config and tool definitions.
func NewAdapter(serverName string, caller toolCaller, tools []ToolDefinition) *Adapter {
	return &Adapter{
		serverName: serverName,
		caller:     caller,
		tools:      tools,
	}
}

// RegisterTools registers all MCP tools into the registry with "mcp__" prefix.
func (a *Adapter) RegisterTools(registry *tool.Registry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, td := range a.tools {
		name := fmt.Sprintf("mcp__%s__%s", a.serverName, td.Name)
		t := &mcpTool{
			name:     name,
			caller:   a.caller,
			toolName: td.Name,
			desc:     td.Description,
			schema:   td.InputSchema,
		}
		if err := registry.Register(t); err != nil {
			// Log but continue — name collision is non-fatal
			debug.Log("mcp", "warning: %v", err)
		}
	}
	return nil
}

// ToolNames returns the full ggcode tool names for all MCP tools.
func (a *Adapter) ToolNames() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	names := make([]string, len(a.tools))
	for i, td := range a.tools {
		names[i] = fmt.Sprintf("mcp__%s__%s", a.serverName, td.Name)
	}
	return names
}

// ServerName returns the MCP server name.
func (a *Adapter) ServerName() string { return a.serverName }

// ToolCount returns the number of tools from this server.
func (a *Adapter) ToolCount() int { return len(a.tools) }

// mcpTool implements tool.Tool for a single MCP tool.
type mcpTool struct {
	name     string
	caller   toolCaller
	toolName string
	desc     string
	schema   json.RawMessage
}

func (t *mcpTool) Name() string        { return t.name }
func (t *mcpTool) Description() string { return t.desc }
func (t *mcpTool) Parameters() json.RawMessage {
	if len(t.schema) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return t.schema
}

func (t *mcpTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var args map[string]interface{}
	if input != nil && string(input) != "" {
		if err := json.Unmarshal(input, &args); err != nil {
			return tool.Result{IsError: true}, fmt.Errorf("parsing tool arguments: %w", err)
		}
	}
	if t.caller == nil {
		return tool.Result{IsError: true}, fmt.Errorf("mcp tool %s is not connected", t.toolName)
	}
	result, err := t.caller.CallTool(ctx, t.toolName, args)
	if err != nil {
		return tool.Result{IsError: true}, err
	}

	// Extract text from content blocks
	var parts []string
	for _, c := range result.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}

	return tool.Result{
		Content: strings.Join(parts, "\n"),
		IsError: result.IsError,
	}, nil
}
