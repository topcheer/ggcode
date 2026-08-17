package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

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
	readOnly   bool
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

// NewReadOnlyAdapter creates an MCP adapter that blocks write-type tools.
func NewReadOnlyAdapter(serverName string, caller toolCaller, tools []ToolDefinition) *Adapter {
	return &Adapter{
		serverName: serverName,
		caller:     caller,
		tools:      tools,
		readOnly:   true,
	}
}

// IsReadOnly returns true if this adapter is in read-only mode.
func (a *Adapter) IsReadOnly() bool { return a.readOnly }

// RegisterTools registers all MCP tools into the registry with "mcp__" prefix.
func (a *Adapter) RegisterTools(registry *tool.Registry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, td := range a.tools {
		name := fmt.Sprintf("mcp__%s__%s", a.serverName, td.Name)
		desc := td.Description
		blocked := a.readOnly && isWriteToolName(td.Name)
		if a.readOnly {
			desc = desc + " (read-only)"
		}
		t := &mcpTool{
			name:     name,
			caller:   a.caller,
			toolName: td.Name,
			desc:     desc,
			schema:   td.InputSchema,
			readOnly: a.readOnly,
			blocked:  blocked,
			srvName:  a.serverName,
		}
		if err := registry.Register(t); err != nil {
			// Log but continue — name collision is non-fatal
			debug.Log("mcp", "tool %q from server %q conflicts with existing tool, skipping: %v", name, a.serverName, err)
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
	readOnly bool
	blocked  bool
	srvName  string

	// ContextFill mirrors the agent guard's fill ratio (current tokens /
	// compaction threshold, 0.0-1.0+). When ≥0.50 the result cap shrinks to
	// stay under the guard's corresponding limit, avoiding a second
	// middle-cutting truncation of an already head-only result (#365).
	// Guarded by fillMu: SetContextFill runs on the agent loop goroutine
	// while Execute reads it on the safeExecute worker goroutine.
	ContextFill float64
	fillMu      sync.Mutex
}

// maxMCPResultBytes caps MCP tool results at the agent-tool layer (50KB,
// matching web_fetch).
const maxMCPResultBytes = 50 * 1024

// SetContextFill receives the agent guard's fill ratio before each
// execution so the result cap can shrink under context pressure (#365).
// The agent side injects it via the fillAwareTool interface assertion
// (agent_tool.go safeExecute) — assigning nothing here would leave the
// fill-aware cap permanently dead code (#369).
func (t *mcpTool) SetContextFill(fill float64) {
	t.fillMu.Lock()
	t.ContextFill = fill
	t.fillMu.Unlock()
}

// Clone returns an independent copy of this tool (tool.Cloner, #645).
// mcpTool holds mutable per-agent state — ContextFill is injected by each
// agent's guard before every execution (agent_tool.go safeExecute). The tool
// registry contract (internal/tool/tool.go) says tools with mutable state MUST
// implement Cloner, otherwise Registry.Clone shares this instance and the main
// agent's high fill (e.g. 0.75 → 9KB cap) bleeds into concurrent swarm
// teammates' MCP results — truncation semantics cross agents. The clone starts
// at fill 0 (unknown → full cap); each agent re-injects its own fill.
// caller is an interface value copied by value, and toolName/srvName strings
// are immutable — a shallow struct copy with a fresh mutex is a correct deep
// copy. schema (json.RawMessage) is read-only by contract (Parameters).
func (t *mcpTool) Clone() tool.Tool {
	return &mcpTool{
		name:     t.name,
		caller:   t.caller,
		toolName: t.toolName,
		desc:     t.desc,
		schema:   append(json.RawMessage(nil), t.schema...),
		readOnly: t.readOnly,
		blocked:  t.blocked,
		srvName:  t.srvName,
	}
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
	if t.blocked {
		return tool.Result{
			Content: fmt.Sprintf("MCP server '%s' is in read-only mode, tool '%s' is not allowed", t.srvName, t.toolName),
			IsError: true,
		}, nil
	}
	var args map[string]interface{}
	if input != nil && string(input) != "" {
		if err := json.Unmarshal(input, &args); err != nil {
			return tool.Result{Content: fmt.Sprintf("mcp[%s]: parsing tool arguments: %v", t.srvName, err), IsError: true}, nil
		}
	}
	if t.caller == nil {
		return tool.Result{
			Content: fmt.Sprintf("mcp[%s]: tool '%s' is not connected (server may have crashed or not started)", t.srvName, t.toolName),
			IsError: true,
		}, nil
	}
	result, err := t.caller.CallTool(ctx, t.toolName, args)
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("mcp[%s]: %s → %v", t.srvName, t.toolName, err),
			IsError: true,
		}, nil
	}

	// Extract text from content blocks
	var parts []string
	for _, c := range result.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}

	content := strings.Join(parts, "\n")
	// When the MCP server itself reports an error (IsError=true), prefix
	// the content with the server name so the agent knows which server failed.
	if result.IsError {
		content = fmt.Sprintf("mcp[%s]: %s", t.srvName, content)
	}

	// Cap result size to protect the agent's context window. MCP servers
	// can return arbitrary content (database dumps, large file contents,
	// API responses) that could flood the context. 50KB matches web_fetch.
	//
	// Under high context fill the agent-level guard (tool_output_guard.go)
	// would re-truncate this 50KB head-only result down to 20-10KB, cutting
	// the middle a second time. Shrinking our own cap to stay under the
	// guard's smallest limit keeps head-only truncation the single cut
	// (#365). ContextFill is the same ratio the guard uses (current tokens
	// / compaction threshold); zero means unknown → use the full cap.
	maxBytes := maxMCPResultBytes
	t.fillMu.Lock()
	fill := t.ContextFill
	t.fillMu.Unlock()
	switch {
	case fill >= 0.75:
		maxBytes = 9 * 1024 // under the guard's 10KB critical limit
	case fill >= 0.65:
		maxBytes = 19 * 1024 // under the guard's 20KB high limit
	case fill >= 0.50:
		maxBytes = 39 * 1024 // under the guard's 40KB moderate limit
	}
	if len(content) > maxBytes {
		// Byte-slicing can split a multi-byte UTF-8 rune (Chinese text hits
		// this often), producing invalid UTF-8 downstream. Back up to
		// the nearest rune boundary before truncating (same pattern as
		// internal/util/truncate.go, fix #262).
		end := maxBytes
		for end > 0 && !utf8.RuneStart(content[end]) {
			end--
		}
		content = content[:end] +
			fmt.Sprintf("\n\n[... MCP result truncated: %d bytes total, showing first %d ...]",
				len(content), end)
		debug.Log("mcp", "result truncated: server=%s tool=%s total=%d cap=%d fill=%.2f",
			t.srvName, t.toolName, len(content), end, fill)
	}

	return tool.Result{
		Content: content,
		IsError: result.IsError,
	}, nil
}
