package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

type toolCaller interface {
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error)
}

type taskCaller interface {
	CallToolAsTask(ctx context.Context, name string, args map[string]interface{}, opts TaskRequestOptions) (*CallToolResult, *Task, error)
	HasTasks() bool
}

type Adapter struct {
	serverName string
	caller     toolCaller
	tools      []ToolDefinition
	readOnly   bool
	mu         sync.Mutex

	registeredTools []string

	breaker *serverBreaker

	serverInstructions string
}

func NewAdapter(serverName string, caller toolCaller, tools []ToolDefinition) *Adapter {
	return &Adapter{
		serverName: serverName,
		caller:     caller,
		tools:      tools,
		breaker:    newServerBreaker(serverName),
	}
}

func NewReadOnlyAdapter(serverName string, caller toolCaller, tools []ToolDefinition) *Adapter {
	return &Adapter{
		serverName: serverName,
		caller:     caller,
		tools:      tools,
		readOnly:   true,
		breaker:    newServerBreaker(serverName),
	}
}

const serverInstructionsMaxRunes = 1200

func (a *Adapter) SetServerInstructions(instructions string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.serverInstructions = strings.TrimSpace(instructions)
}

func (a *Adapter) IsReadOnly() bool { return a.readOnly }

func (a *Adapter) RegisterTools(registry *tool.Registry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.registeredTools = a.registeredTools[:0]

	notes := a.serverInstructions
	for _, td := range a.tools {
		name := fmt.Sprintf("mcp__%s__%s", a.serverName, td.Name)
		desc := td.Description
		blocked := a.readOnly && isWriteToolName(td.Name)
		if blocked && td.annotationAllowsReadOnly() {

			blocked = false
			debug.Log("mcp", "read-only server %q: tool %q allowed via readOnlyHint annotation despite write-like name", a.serverName, td.Name)
		}
		if a.readOnly {
			desc = desc + " (read-only)"
		} else if td.DeclaredReadOnly() {

			desc = desc + " (server declares read-only)"
		}
		if notes != "" {

			desc = desc + "\n\nServer usage notes: " + util.Truncate(notes, serverInstructionsMaxRunes)
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
			breaker:  a.breaker,
		}
		if err := registry.Register(t); err != nil {

			debug.Log("mcp", "tool %q from server %q conflicts with existing tool, skipping: %v", name, a.serverName, err)
			continue
		}
		a.registeredTools = append(a.registeredTools, name)
	}
	return nil
}

func (a *Adapter) RegisteredNames() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.registeredTools))
	copy(out, a.registeredTools)
	return out
}

func (a *Adapter) ToolNames() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	names := make([]string, len(a.tools))
	for i, td := range a.tools {
		names[i] = fmt.Sprintf("mcp__%s__%s", a.serverName, td.Name)
	}
	return names
}

func (a *Adapter) ServerName() string { return a.serverName }

func (a *Adapter) ToolCount() int { return len(a.tools) }

type mcpTool struct {
	name     string
	caller   toolCaller
	toolName string
	desc     string
	schema   json.RawMessage
	readOnly bool
	blocked  bool
	srvName  string

	breaker *serverBreaker

	ContextFill float64
	fillMu      sync.Mutex
}

const maxMCPResultBytes = 50 * 1024

func (t *mcpTool) SetContextFill(fill float64) {
	t.fillMu.Lock()
	t.ContextFill = fill
	t.fillMu.Unlock()
}

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
		breaker:  t.breaker,
	}
}

func (t *mcpTool) SandboxSafe() bool { return t.readOnly }

func (t *mcpTool) Name() string        { return t.name }
func (t *mcpTool) Description() string { return t.desc }
func (t *mcpTool) Parameters() json.RawMessage {
	if len(t.schema) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return t.schema
}

func (t *mcpTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {

	if t.breaker != nil {
		if blocked, msg := t.breaker.gate(); blocked {
			return tool.Result{Content: msg, IsError: true}, nil
		}
	}
	if t.blocked {

		if t.breaker != nil {
			t.breaker.abandonProbe()
		}
		return tool.Result{
			Content: fmt.Sprintf("MCP server '%s' is in read-only mode, tool '%s' is not allowed", t.srvName, t.toolName),
			IsError: true,
		}, nil
	}
	var args map[string]interface{}
	if input != nil && string(input) != "" {
		if err := json.Unmarshal(input, &args); err != nil {

			if t.breaker != nil {
				t.breaker.abandonProbe()
			}
			return tool.Result{Content: fmt.Sprintf("mcp[%s]: parsing tool arguments: %v", t.srvName, err), IsError: true}, nil
		}
	}
	if t.caller == nil {

		err := error(errNotConnected{server: t.srvName})
		if t.breaker != nil {
			t.breaker.recordFailure(err)
		}
		return tool.Result{
			Content: fmt.Sprintf("mcp[%s]: tool '%s' is not connected (server may have crashed or not started)", t.srvName, t.toolName),
			IsError: true,
		}, nil
	}
	var result *CallToolResult
	var err error
	if tc, ok := t.caller.(taskCaller); ok && tc.HasTasks() {

		result, _, err = tc.CallToolAsTask(ctx, t.toolName, args, TaskRequestOptions(`true`))
	} else {
		result, err = t.caller.CallTool(ctx, t.toolName, args)
	}
	if err != nil {

		if t.breaker != nil {
			if errors.Is(err, context.Canceled) {

				t.breaker.abandonProbe()
			} else if isInfraError(err) {
				t.breaker.recordFailure(err)
			} else {
				t.breaker.recordSuccess()
			}
		}
		return tool.Result{
			Content: fmt.Sprintf("mcp[%s]: %s → %v", t.srvName, t.toolName, err),
			IsError: true,
		}, nil
	}

	if t.breaker != nil {
		t.breaker.recordSuccess()
	}

	var parts []string
	for _, c := range result.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
			continue
		}

		parts = append(parts, fmt.Sprintf("[%s content omitted by MCP adapter]", c.Type))
	}

	if len(parts) == 0 && len(result.StructuredContent) > 0 {
		parts = append(parts, string(result.StructuredContent))
	}

	content := strings.Join(parts, "\n")

	if result.IsError {
		content = fmt.Sprintf("mcp[%s]: %s", t.srvName, content)
	}

	maxBytes := maxMCPResultBytes
	t.fillMu.Lock()
	fill := t.ContextFill
	t.fillMu.Unlock()
	switch {
	case fill >= 0.75:
		maxBytes = 9 * 1024
	case fill >= 0.65:
		maxBytes = 19 * 1024
	case fill >= 0.50:
		maxBytes = 39 * 1024
	}
	if len(content) > maxBytes {

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
