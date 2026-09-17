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
	"github.com/topcheer/ggcode/internal/util"
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
	// registeredTools are the names THIS adapter actually owns in the
	// registry (collision-skips excluded, #1594-A).
	registeredTools []string
	// breaker is the per-server circuit breaker (sa-21) shared by every
	// mcpTool this adapter registers. One dead server must fast-fail all
	// of its tools, so the state lives here, not per tool.
	breaker *serverBreaker
	// serverInstructions carries the MCP initialize-result instructions
	// (server-authored usage guidance, 2025-03-26+ spec). Surfaced by
	// RegisterTools on every tool description (sa-44).
	serverInstructions string
}

// NewAdapter creates an MCP adapter from server config and tool definitions.
func NewAdapter(serverName string, caller toolCaller, tools []ToolDefinition) *Adapter {
	return &Adapter{
		serverName: serverName,
		caller:     caller,
		tools:      tools,
		breaker:    newServerBreaker(serverName),
	}
}

// NewReadOnlyAdapter creates an MCP adapter that blocks write-type tools.
func NewReadOnlyAdapter(serverName string, caller toolCaller, tools []ToolDefinition) *Adapter {
	return &Adapter{
		serverName: serverName,
		caller:     caller,
		tools:      tools,
		readOnly:   true,
		breaker:    newServerBreaker(serverName),
	}
}

// serverInstructionsMaxRunes bounds how much server-authored instruction
// text is surfaced per tool description (keeps model context compact even
// if a server embeds a whole README).
const serverInstructionsMaxRunes = 1200

// SetServerInstructions records the MCP server's initialize instructions
// so RegisterTools can surface them to the model. Call before
// RegisterTools; safe for concurrent use.
func (a *Adapter) SetServerInstructions(instructions string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.serverInstructions = strings.TrimSpace(instructions)
}

// IsReadOnly returns true if this adapter is in read-only mode.
func (a *Adapter) IsReadOnly() bool { return a.readOnly }

// RegisterTools registers all MCP tools into the registry with "mcp__" prefix.
// #1594-A: the subset that ACTUALLY registered (collision-skips excluded) is
// recorded - Close must only unregister what this adapter owns; unregistering
// ToolNames() wholesale cross-killed a concurrent same-named session's tools
// in the shared registry (the collision-skip made the OTHER session the
// owner of those names).
func (a *Adapter) RegisterTools(registry *tool.Registry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.registeredTools = a.registeredTools[:0]
	// sa-44: read server instructions once here, already under mu.
	// SetServerInstructions is documented to be called before
	// RegisterTools, so this snapshot is the final value; locking
	// a.mu again inside the loop below would self-deadlock (Go
	// mutexes are not reentrant - this exact bug hung the first
	// sa-44 test run).
	notes := a.serverInstructions
	for _, td := range a.tools {
		name := fmt.Sprintf("mcp__%s__%s", a.serverName, td.Name)
		desc := td.Description
		blocked := a.readOnly && isWriteToolName(td.Name)
		if blocked && td.annotationAllowsReadOnly() {
			// 2025-06-18 tool annotations: the server's own readOnlyHint
			// declaration overrides the name heuristic (see annotations.go).
			// Contradicting destructiveHint=true keeps the block (the more
			// dangerous hint wins).
			blocked = false
			debug.Log("mcp", "read-only server %q: tool %q allowed via readOnlyHint annotation despite write-like name", a.serverName, td.Name)
		}
		if a.readOnly {
			desc = desc + " (read-only)"
		} else if td.DeclaredReadOnly() {
			// Risk vocabulary for the model: surface the server's own
			// declaration so the agent can weight side effects correctly.
			desc = desc + " (server declares read-only)"
		}
		if notes != "" {
			// MCP spec (2025-03-26+): the initialize result MAY carry
			// server-authored usage instructions; clients SHOULD surface
			// them to the model. The tool-description channel is
			// protocol-safe on every provider, so append them here,
			// rune-bounded (sa-44).
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
			breaker:  a.breaker, // shared per-server state (sa-21)
		}
		if err := registry.Register(t); err != nil {
			// Log but continue — name collision is non-fatal. Whoever already
			// holds the name stays the owner; we must not unregister it on Close.
			debug.Log("mcp", "tool %q from server %q conflicts with existing tool, skipping: %v", name, a.serverName, err)
			continue
		}
		a.registeredTools = append(a.registeredTools, name)
	}
	return nil
}

// RegisteredNames returns the ggcode tool names this adapter ACTUALLY owns
// in the registry (post-collision subset, #1594-A).
func (a *Adapter) RegisteredNames() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.registeredTools))
	copy(out, a.registeredTools)
	return out
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

	// breaker is the per-server circuit breaker (sa-21). Shared by ALL
	// tools of the same server - including across Registry.Clone() copies
	// (Clone copies the pointer, so swarm teammates share outage state;
	// that is correct: a dead server is dead for every agent). Nil in
	// hand-built test fixtures → breaker disabled, zero behavior change.
	breaker *serverBreaker

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
		breaker:  t.breaker,
	}
}

// SandboxSafe (tool.SandboxSafe, sa-19) reports whether this tool may be
// invoked from the code_execution sandbox. It mirrors the server-level
// read_only contract: a read_only server's tools are already enforced
// against write-shaped names at registration time, so exposing them to
// the sandbox adds no new capability. Tools from servers without
// read_only are never sandbox-safe — their Execute may have side effects.
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
	// sa-21: per-server circuit breaker gate. When OPEN, fail fast WITHOUT
	// a transport attempt - a dead server cannot succeed, and every retry
	// burns a full LLM round-trip (often 120s+ of stdio timeout).
	if t.breaker != nil {
		if blocked, msg := t.breaker.gate(); blocked {
			return tool.Result{Content: msg, IsError: true}, nil
		}
	}
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
		// Never-connected counts as infrastructure: no tool on this server
		// can work until it is (re)started, so it feeds the breaker.
		err := error(errNotConnected{server: t.srvName})
		if t.breaker != nil {
			t.breaker.recordFailure(err)
		}
		return tool.Result{
			Content: fmt.Sprintf("mcp[%s]: tool '%s' is not connected (server may have crashed or not started)", t.srvName, t.toolName),
			IsError: true,
		}, nil
	}
	result, err := t.caller.CallTool(ctx, t.toolName, args)
	if err != nil {
		// Classify before surfacing: only transport-level failures feed the
		// breaker. Semantic errors (server answered) never trip it.
		if t.breaker != nil {
			if isInfraError(err) {
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
	// The server answered (even isError=true results mean it is reachable).
	if t.breaker != nil {
		t.breaker.recordSuccess()
	}

	// Extract text from content blocks
	var parts []string
	for _, c := range result.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
			continue
		}
		// #1588-A: a browser/fetch-class server can return ONLY image or
		// resource blocks - the text-only loop produced Content="" with
		// IsError=false, a clueless empty success. Emit a typed placeholder
		// so the agent knows non-text content arrived (and roughly how
		// much), instead of nothing.
		// ToolContent carries only Type/Text today - describe the kind and
		// count so the agent knows non-text content arrived.
		parts = append(parts, fmt.Sprintf("[%s content omitted by MCP adapter]", c.Type))
	}

	// #1644 case 6: a spec-legal 2025-06-18 server may return ONLY
	// structuredContent with zero content blocks - the loop above ran zero
	// times and produced Content="" with IsError=false, a clueless empty
	// success. Render the structured payload as JSON text so the agent
	// sees the data instead of nothing.
	if len(parts) == 0 && len(result.StructuredContent) > 0 {
		parts = append(parts, string(result.StructuredContent))
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
