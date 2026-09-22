// Package mcpserve implements the server side of the Model Context Protocol
// (MCP) for ggcode: it exposes ggcode itself as a tool provider over the
// stdio transport so other MCP-capable agents (Claude Code, Cursor, VS Code,
// ...) can drive ggcode as a coding sub-agent.
//
// This mirrors the "coding agent as an MCP server" pattern (claude mcp serve,
// codex mcp-server): a top-level orchestrator registers the coding agent as a
// tool and delegates bounded tasks to it, while the agent keeps its own
// config, model and permission policy.
//
// Transport: newline-delimited JSON-RPC 2.0 frames on stdin/stdout (one
// message per line, no embedded newlines per the MCP stdio spec). Only JSON
// frames are ever written to stdout; all diagnostics go to stderr via the
// debug log.
package mcpserve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// maxMessageBytes bounds a single inbound JSON-RPC frame. Tool arguments are
// small (a prompt plus options); anything near this bound is abuse.
const maxMessageBytes = 4 * 1024 * 1024

// supportedProtocolVersions lists the MCP protocol versions this server can
// speak, newest first. It mirrors the version set accepted by ggcode's own
// MCP client (internal/mcp) so both ends of the ecosystem stay consistent.
var supportedProtocolVersions = []string{
	"2025-11-25",
	"2025-06-18",
	"2025-03-26",
	"2024-11-05",
}

// JSON-RPC error codes (spec + MCP conventions).
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// rpcRequest is an inbound JSON-RPC 2.0 request or notification.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is an outbound JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message)
}

// contentBlock is an MCP tool result content entry. This server only emits
// text content.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolCallResult is the MCP tools/call result envelope.
type toolCallResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// toolAnnotations mirrors the MCP tool annotations object.
type toolAnnotations struct {
	ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`
}

// toolDef is a tool descriptor returned by tools/list.
type toolDef struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	InputSchema map[string]any   `json:"inputSchema"`
	Annotations *toolAnnotations `json:"annotations,omitempty"`
}

// initializeParams is the MCP initialize request payload.
type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	Capabilities    any    `json:"capabilities,omitempty"`
	ClientInfo      *struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo,omitempty"`
}

// Server is the ggcode MCP server. It is safe for use from a single Serve
// loop; responses are serialized through an internal mutex so tool handlers
// may run concurrently in the future without framing corruption.
type Server struct {
	version    string
	runner     RunTool
	sessions   SessionSource
	runTimeout time.Duration
	maxTimeout time.Duration

	outMu sync.Mutex
}

// Options configures a Server.
type Options struct {
	// Version is reported in serverInfo (e.g. version.Display()).
	Version string
	// Runner executes the ggcode_run tool. Defaults to execRunner.
	Runner RunTool
	// Sessions backs ggcode_session_list / ggcode_session_read. Defaults to
	// the local JSONL session store. Nil disables the session tools.
	Sessions SessionSource
	// RunTimeout is the default ggcode_run timeout. Defaults to 10m.
	RunTimeout time.Duration
	// MaxTimeout caps the caller-provided timeout. Defaults to 30m.
	MaxTimeout time.Duration
	// ExecPath is the ggcode binary used by the default runner. When empty
	// the runner falls back to os.Executable().
	ExecPath string
	// ConfigPath is forwarded to the child ggcode process via --config.
	ConfigPath string
}

// New builds a Server with defaults applied.
func New(opts Options) *Server {
	s := &Server{
		version:    opts.Version,
		runTimeout: opts.RunTimeout,
		maxTimeout: opts.MaxTimeout,
	}
	if s.runTimeout <= 0 {
		s.runTimeout = 10 * time.Minute
	}
	if s.maxTimeout <= 0 {
		s.maxTimeout = 30 * time.Minute
	}
	s.runner = opts.Runner
	if s.runner == nil {
		s.runner = newExecRunner(opts.ExecPath, opts.ConfigPath, s.maxTimeout)
	}
	s.sessions = opts.Sessions
	if s.sessions == nil {
		s.sessions = newJSONLSessionSource()
	}
	return s
}

// Serve runs the request loop until the input stream is exhausted (client
// closed stdin), returning nil on clean EOF. Each line is one JSON-RPC
// message; responses are written to w as single-line JSON frames.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		s.handleFrame(ctx, line, w)
	}
	return sc.Err()
}

// handleFrame parses and dispatches one inbound frame, writing zero or one
// response (notifications produce none).
func (s *Server) handleFrame(ctx context.Context, line []byte, w io.Writer) {
	var req rpcRequest
	dec := json.NewDecoder(bytes.NewReader(line))
	if err := dec.Decode(&req); err != nil {
		s.writeResponse(w, &rpcResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &rpcError{Code: errParse, Message: fmt.Sprintf("parse error: %v", err)},
		})
		return
	}
	isNotification := len(req.ID) == 0 || bytes.Equal(req.ID, []byte("null"))
	if req.JSONRPC != "2.0" {
		if isNotification {
			return
		}
		s.writeError(w, req.ID, errInvalidRequest, "jsonrpc must be exactly \"2.0\"")
		return
	}
	if isNotification {
		// notifications/initialized and friends: no response required.
		debug.Log("mcpserve", "notification: %s", req.Method)
		return
	}
	result, rpcErr := s.dispatch(ctx, req.Method, req.Params)
	if rpcErr != nil {
		s.writeError(w, req.ID, rpcErr.Code, rpcErr.Message)
		return
	}
	s.writeResponse(w, &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
}

// dispatch routes a request method to its handler.
func (s *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return s.handleInitialize(params)
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return map[string]any{"tools": s.toolDefs()}, nil
	case "tools/call":
		return s.handleToolsCall(ctx, params)
	case "resources/list":
		return map[string]any{"resources": []any{}}, nil
	case "prompts/list":
		return map[string]any{"prompts": []any{}}, nil
	default:
		debug.Log("mcpserve", "method not found: %s", method)
		return nil, &rpcError{Code: errMethodNotFound, Message: fmt.Sprintf("method not found: %s", method)}
	}
}

// handleInitialize performs protocol version negotiation. Per the MCP spec,
// the server responds with the client's requested version when supported,
// otherwise with its own latest version (the client then decides whether to
// continue).
func (s *Server) handleInitialize(params json.RawMessage) (any, *rpcError) {
	var p initializeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: fmt.Sprintf("invalid initialize params: %v", err)}
		}
	}
	negotiated := negotiateProtocolVersion(p.ProtocolVersion)
	client := ""
	if p.ClientInfo != nil {
		client = p.ClientInfo.Name
	}
	debug.Log("mcpserve", "initialize: client=%q requested=%q negotiated=%s", client, p.ProtocolVersion, negotiated)
	return map[string]any{
		"protocolVersion": negotiated,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "ggcode",
			"version": s.version,
		},
		"instructions": "ggcode coding agent exposed as an MCP server. Use the ggcode_run tool to delegate a self-contained task; the local ggcode configuration (model, vendor, permission policy) applies.",
	}, nil
}

// negotiateProtocolVersion echoes a supported client version or falls back to
// the server's latest supported version.
func negotiateProtocolVersion(requested string) string {
	for _, v := range supportedProtocolVersions {
		if requested == v {
			return requested
		}
	}
	return supportedProtocolVersions[0]
}

// handleToolsCall routes a tool invocation. Unknown tools and malformed
// arguments are protocol errors (-32602); execution failures are reported in
// the result with isError=true per the MCP spec.
func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: fmt.Sprintf("invalid tools/call params: %v", err)}
	}
	if len(p.Arguments) == 0 {
		p.Arguments = json.RawMessage("{}")
	}
	switch p.Name {
	case "ggcode_run":
		return s.callGgcodeRun(ctx, p.Arguments)
	case "ggcode_session_list":
		return s.callSessionList(p.Arguments)
	case "ggcode_session_read":
		return s.callSessionRead(p.Arguments)
	default:
		return nil, &rpcError{Code: errInvalidParams, Message: fmt.Sprintf("unknown tool: %s", p.Name)}
	}
}

// callGgcodeRun validates arguments and runs one headless agent turn.
func (s *Server) callGgcodeRun(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var req RunRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: fmt.Sprintf("invalid ggcode_run arguments: %v", err)}
	}
	if len(bytes.TrimSpace([]byte(req.Prompt))) == 0 {
		return nil, &rpcError{Code: errInvalidParams, Message: "ggcode_run: prompt must not be empty"}
	}
	timeout := s.runTimeout
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
		if timeout > s.maxTimeout {
			timeout = s.maxTimeout
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := s.runner.Run(callCtx, req)
	if err != nil {
		debug.Log("mcpserve", "ggcode_run failed: %v", err)
		return toolCallResult{
			Content: []contentBlock{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}
	return toolCallResult{
		Content: []contentBlock{{Type: "text", Text: res.Output}},
	}, nil
}

// callSessionList lists recent local sessions.
func (s *Server) callSessionList(args json.RawMessage) (any, *rpcError) {
	var req struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: fmt.Sprintf("invalid ggcode_session_list arguments: %v", err)}
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	if s.sessions == nil {
		return toolCallResult{Content: []contentBlock{{Type: "text", Text: "session tools unavailable"}}}, nil
	}
	summaries, err := s.sessions.List(limit)
	if err != nil {
		return toolCallResult{Content: []contentBlock{{Type: "text", Text: fmt.Sprintf("listing sessions: %v", err)}}, IsError: true}, nil
	}
	text := formatSessionSummaries(summaries)
	return toolCallResult{Content: []contentBlock{{Type: "text", Text: text}}}, nil
}

// callSessionRead returns a formatted transcript of one session.
func (s *Server) callSessionRead(args json.RawMessage) (any, *rpcError) {
	var req struct {
		SessionID   string `json:"session_id"`
		MaxMessages int    `json:"max_messages"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: fmt.Sprintf("invalid ggcode_session_read arguments: %v", err)}
	}
	if len(bytes.TrimSpace([]byte(req.SessionID))) == 0 {
		return nil, &rpcError{Code: errInvalidParams, Message: "ggcode_session_read: session_id must not be empty"}
	}
	maxMessages := req.MaxMessages
	if maxMessages <= 0 {
		maxMessages = 100
	}
	if maxMessages > 500 {
		maxMessages = 500
	}
	if s.sessions == nil {
		return toolCallResult{Content: []contentBlock{{Type: "text", Text: "session tools unavailable"}}}, nil
	}
	transcript, err := s.sessions.Read(req.SessionID, maxMessages)
	if err != nil {
		return toolCallResult{Content: []contentBlock{{Type: "text", Text: fmt.Sprintf("reading session: %v", err)}}, IsError: true}, nil
	}
	return toolCallResult{Content: []contentBlock{{Type: "text", Text: transcript}}}, nil
}

// toolDefs returns the advertised tool descriptors.
func (s *Server) toolDefs() []toolDef {
	readOnly := true
	writeish := false
	defs := []toolDef{
		{
			Name: "ggcode_run",
			Description: "Run the ggcode coding agent headlessly on a self-contained task and return its final answer. " +
				"Uses the local ggcode configuration (model, vendor, permission policy). " +
				"Prefer precise, single-goal prompts; the agent has file, shell and search tools available.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "The task for the agent. Must be self-contained: it runs without follow-up interaction.",
					},
					"working_dir": map[string]any{
						"type":        "string",
						"description": "Absolute working directory for the run. Defaults to the server's working directory.",
					},
					"timeout_seconds": map[string]any{
						"type":        "integer",
						"minimum":     10,
						"maximum":     1800,
						"description": "Wall-clock timeout in seconds. Defaults to 600; hard-capped at 1800.",
					},
				},
				"required": []string{"prompt"},
			},
			Annotations: &toolAnnotations{ReadOnlyHint: &writeish},
		},
	}
	if s.sessions != nil {
		defs = append(defs,
			toolDef{
				Name: "ggcode_session_list",
				Description: "List the most recent local ggcode sessions (id, title, preview, workspace, model). " +
					"Read-only. Use ggcode_session_read to inspect one.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"limit": map[string]any{
							"type":        "integer",
							"minimum":     1,
							"maximum":     100,
							"description": "How many sessions to return. Defaults to 10.",
						},
					},
				},
				Annotations: &toolAnnotations{ReadOnlyHint: &readOnly},
			},
			toolDef{
				Name: "ggcode_session_read",
				Description: "Read a ggcode session transcript (most recent messages first-tail, role-tagged, truncated per message). " +
					"Read-only.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": map[string]any{
							"type":        "string",
							"description": "Session id from ggcode_session_list.",
						},
						"max_messages": map[string]any{
							"type":        "integer",
							"minimum":     1,
							"maximum":     500,
							"description": "Maximum number of most-recent messages to include. Defaults to 100.",
						},
					},
					"required": []string{"session_id"},
				},
				Annotations: &toolAnnotations{ReadOnlyHint: &readOnly},
			},
		)
	}
	return defs
}

// writeResponse serializes one response frame (single line + newline).
func (s *Server) writeResponse(w io.Writer, resp *rpcResponse) {
	buf, err := json.Marshal(resp)
	if err != nil {
		debug.Log("mcpserve", "marshal response: %v", err)
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	if _, err := w.Write(append(buf, '\n')); err != nil {
		debug.Log("mcpserve", "write response: %v", err)
	}
}

// writeError writes a JSON-RPC error response.
func (s *Server) writeError(w io.Writer, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	s.writeResponse(w, &rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	})
}

// formatSessionSummaries renders the session list as compact text.
func formatSessionSummaries(summaries []SessionSummary) string {
	if len(summaries) == 0 {
		return "No sessions found."
	}
	var sb bytes.Buffer
	sb.WriteString(fmt.Sprintf("%d session(s):\n", len(summaries)))
	for _, s := range summaries {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		updated := ""
		if !s.UpdatedAt.IsZero() {
			updated = s.UpdatedAt.Format(time.RFC3339)
		}
		sb.WriteString(fmt.Sprintf("- %s | %s | workspace=%s | model=%s | updated=%s\n",
			s.ID, title, s.Workspace, s.Model, updated))
		if s.Preview != "" {
			sb.WriteString("  " + s.Preview + "\n")
		}
	}
	return sb.String()
}
