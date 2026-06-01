package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
)

// PromptResult is the aggregated result of a prompt execution.
type PromptResult struct {
	Text       string            // agent's text response
	StopReason StopReason        // why the agent stopped
	ToolCalls  []ToolCallSummary // summary of tool calls made
}

// ToolCallSummary is a simplified view of a tool call for display.
type ToolCallSummary struct {
	Name   string
	Title  string
	Status string // "completed", "failed"
}

// PermissionHandler is called when the agent requests permission.
// Return the response to send back to the agent.
type PermissionHandler func(ctx context.Context, req RequestPermissionRequest) (RequestPermissionResponse, error)

// ApprovalHandler is the host-side interactive approval bridge used when ACP
// requests need to flow through ggcode's existing approval UX.
type ApprovalHandler func(ctx context.Context, toolName string, input string) permission.Decision

// Client manages a single ACP agent process.
// It handles lifecycle (start/stop), session management, and prompt execution.
type Client struct {
	def        DiscoveredAgent
	workingDir string
	policy     permission.PermissionPolicy
	mcpServers []MCPServer

	// Process management
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	transport  *Transport
	cancelProc context.CancelFunc

	// State
	mu          sync.Mutex
	setupMu     sync.Mutex
	execMu      sync.Mutex
	initialized bool
	caps        AgentCapabilities
	authMethods []AuthMethod
	agentInfo   ImplementationInfo
	sessionID   string
	sessionCWD  string
	running     bool

	// Permission handling
	onPermission PermissionHandler
	onApproval   ApprovalHandler

	// Read loop management
	cancelRead context.CancelFunc
	done       chan struct{}

	// Prompt execution state
	promptMu       sync.Mutex
	promptText     strings.Builder
	promptTools    []ToolCallSummary
	activePromptID string
	promptDone     chan PromptResponse
}

// NewClient creates a new ACP client for the given discovered agent.
func NewClient(agent DiscoveredAgent, workingDir string, policy permission.PermissionPolicy, mcpServers []MCPServer) *Client {
	return &Client{
		def:        agent,
		workingDir: workingDir,
		policy:     policy,
		mcpServers: cloneMCPServers(mcpServers),
		done:       make(chan struct{}),
	}
}

// SetPermissionHandler sets the handler for agent permission requests.
func (c *Client) SetPermissionHandler(h PermissionHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onPermission = h
}

func (c *Client) SetApprovalHandler(h ApprovalHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onApproval = h
}

func (c *Client) SetWorkingDir(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workingDir = dir
	if c.cmd != nil {
		c.cmd.Dir = dir
	}
}

// Name returns the canonical agent name.
func (c *Client) Name() string { return c.def.Def.Name }

// Title returns the display name.
func (c *Client) Title() string { return c.def.Def.Title }

// Description returns the agent description.
func (c *Client) Description() string { return c.def.Def.Description }

// Capabilities returns the agent's declared capabilities (after initialize).
func (c *Client) Capabilities() AgentCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

// Start launches the agent process and performs ACP initialize handshake.
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	workingDir := c.workingDir
	c.mu.Unlock()

	debug.Log("acp-client", "starting agent %q: %s %s", c.def.Def.Name, c.def.Path, strings.Join(c.def.Def.ACPCommand, " "))

	args := make([]string, len(c.def.Def.ACPCommand))
	copy(args, c.def.Def.ACPCommand)

	procCtx, cancelProc := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, c.def.Path, args...)
	cmd.Dir = workingDir
	cmd.Stderr = os.Stderr // let agent stderr pass through for debug

	// Wire stdin/stdout
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancelProc()
		return fmt.Errorf("creating stdin pipe for %s: %w", c.def.Def.Name, err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		cancelProc()
		return fmt.Errorf("creating stdout pipe for %s: %w", c.def.Def.Name, err)
	}

	if err := cmd.Start(); err != nil {
		stdinPipe.Close()
		cancelProc()
		return fmt.Errorf("starting process %s: %w", c.def.Def.Name, err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.stdin = stdinPipe
	c.transport = NewTransport(stdoutPipe, stdinPipe)
	c.cancelProc = cancelProc

	// Start read loop
	readCtx, cancelRead := context.WithCancel(context.Background())
	c.cancelRead = cancelRead
	c.done = make(chan struct{})
	c.mu.Unlock()
	go c.readLoop(readCtx)

	// Perform initialize handshake
	if err := c.initialize(ctx); err != nil {
		cancelRead()
		cancelProc()
		cmd.Process.Kill()
		cmd.Wait()
		c.mu.Lock()
		c.cancelRead = nil
		c.cancelProc = nil
		c.cmd = nil
		c.stdin = nil
		c.transport = nil
		c.mu.Unlock()
		return fmt.Errorf("initialize handshake with %s: %w", c.def.Def.Name, err)
	}

	c.mu.Lock()
	c.running = true
	c.mu.Unlock()

	debug.Log("acp-client", "agent %q started successfully (protocol=%d, loadSession=%v)",
		c.def.Def.Name, ProtocolVersion, c.caps.LoadSession)

	return nil
}

func (c *Client) EnsureReady(ctx context.Context) error {
	c.setupMu.Lock()
	defer c.setupMu.Unlock()

	if err := c.Start(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	sessionID := c.sessionID
	sessionCWD := c.sessionCWD
	workingDir := c.workingDir
	c.mu.Unlock()
	if sessionID != "" && sessionCWD == workingDir {
		return nil
	}
	if sessionID != "" {
		c.closeSession(sessionID)
		c.mu.Lock()
		if c.sessionID == sessionID {
			c.sessionID = ""
			c.sessionCWD = ""
		}
		c.mu.Unlock()
	}
	return c.NewSession(ctx, workingDir)
}

// initialize sends the ACP initialize request and waits for the response.
// The read loop goroutine must already be running to deliver the response.
func (c *Client) initialize(ctx context.Context) error {
	initParams := InitializeRequest{
		ProtocolVersion: ProtocolVersion,
		ClientCapabilities: ClientCapabilities{
			FS: &FSCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
			Terminal: false,
		},
		ClientInfo: &ImplementationInfo{
			Name:    "ggcode",
			Title:   "ggcode ACP Host",
			Version: "1.0",
		},
	}

	result, err := c.sendRequest("initialize", initParams, 30*time.Second)
	if err != nil {
		return fmt.Errorf("initialize request: %w", err)
	}

	var resp InitializeResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("parsing initialize response: %w", err)
	}

	c.mu.Lock()
	c.caps = resp.AgentCapabilities
	c.authMethods = resp.AuthMethods
	c.agentInfo = resp.AgentInfo
	c.initialized = true
	c.mu.Unlock()

	return nil
}

// NewSession creates a new session on the agent.
func (c *Client) NewSession(ctx context.Context, cwd string) error {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return fmt.Errorf("agent %q is not running", c.def.Def.Name)
	}
	mcpServers := cloneMCPServers(c.mcpServers)
	c.mu.Unlock()

	params := NewSessionRequest{
		CWD:        cwd,
		MCPServers: mcpServers,
	}

	result, err := c.sendRequest("session/new", params, 30*time.Second)
	if err != nil {
		return fmt.Errorf("session/new for %s: %w", c.def.Def.Name, err)
	}

	var resp NewSessionResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("parsing session/new response: %w", err)
	}

	c.mu.Lock()
	c.sessionID = resp.SessionID
	c.sessionCWD = cwd
	c.mu.Unlock()

	debug.Log("acp-client", "created session %s on agent %q", resp.SessionID, c.def.Def.Name)
	return nil
}

func cloneMCPServers(servers []MCPServer) []MCPServer {
	if len(servers) == 0 {
		return []MCPServer{}
	}
	cloned := make([]MCPServer, len(servers))
	for i, server := range servers {
		cloned[i] = server
		if len(server.Args) > 0 {
			cloned[i].Args = append([]string(nil), server.Args...)
		}
		if len(server.Env) > 0 {
			cloned[i].Env = append([]EnvVariable(nil), server.Env...)
		}
		if len(server.Headers) > 0 {
			cloned[i].Headers = append([]HTTPHeader(nil), server.Headers...)
		}
	}
	return cloned
}

// promptInternal sends a prompt and collects the full response.
// Blocks until the agent completes (end_turn, error, etc.).
func (c *Client) promptInternal(ctx context.Context, prompt string) (*PromptResult, error) {
	c.execMu.Lock()
	defer c.execMu.Unlock()

	c.mu.Lock()
	sessionID := c.sessionID
	if !c.running {
		c.mu.Unlock()
		return nil, fmt.Errorf("agent %q is not running", c.def.Def.Name)
	}
	c.mu.Unlock()

	// Reset prompt state
	c.promptMu.Lock()
	c.promptText.Reset()
	c.promptTools = nil
	c.activePromptID = sessionID
	c.promptDone = make(chan PromptResponse, 1)
	c.promptMu.Unlock()

	promptReq := PromptRequest{
		SessionID: sessionID,
		Prompt:    []ContentBlock{{Type: "text", Text: prompt}},
	}

	if _, err := c.sendRequest("session/prompt", promptReq, 30*time.Second); err != nil {
		c.promptMu.Lock()
		c.activePromptID = ""
		c.promptDone = nil
		c.promptMu.Unlock()
		return nil, err
	}

	select {
	case resp := <-c.promptDone:
		c.promptMu.Lock()
		pr := &PromptResult{
			Text:       c.promptText.String(),
			StopReason: resp.StopReason,
			ToolCalls:  c.promptTools,
		}
		c.activePromptID = ""
		c.promptDone = nil
		c.promptMu.Unlock()
		return pr, nil

	case <-ctx.Done():
		c.sendCancel(sessionID)
		c.promptMu.Lock()
		c.activePromptID = ""
		c.promptDone = nil
		c.promptMu.Unlock()
		return nil, ctx.Err()

	case <-c.done:
		c.promptMu.Lock()
		c.activePromptID = ""
		c.promptDone = nil
		c.promptMu.Unlock()
		return nil, fmt.Errorf("agent %q process exited unexpectedly", c.def.Def.Name)
	}
}

// Close sends session/close (if session exists) and kills the process.
func (c *Client) Close() error {
	c.mu.Lock()
	sessionID := c.sessionID
	running := c.running
	cancelRead := c.cancelRead
	cancelProc := c.cancelProc
	cmd := c.cmd
	c.mu.Unlock()

	if running && sessionID != "" {
		c.closeSession(sessionID)
	}

	if cancelRead != nil {
		cancelRead()
	}

	if cancelProc != nil {
		cancelProc()
	}

	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}

	c.mu.Lock()
	c.running = false
	c.initialized = false
	c.cancelRead = nil
	c.cancelProc = nil
	c.cmd = nil
	c.stdin = nil
	c.transport = nil
	c.sessionID = ""
	c.sessionCWD = ""
	c.mu.Unlock()

	return nil
}

// sendRequest sends a JSON-RPC request via the transport and waits for response.
func (c *Client) sendRequest(method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	return c.transport.SendRequest(method, params, timeout)
}

// sendCancel sends a session/cancel notification.
func (c *Client) sendCancel(sessionID string) {
	_ = c.transport.WriteNotification("session/cancel", CancelNotification{
		SessionID: sessionID,
	})
}

func (c *Client) closeSession(sessionID string) {
	if c.transport == nil || sessionID == "" {
		return
	}
	_, _ = c.sendRequest("session/close", CloseSessionRequest{SessionID: sessionID}, 5*time.Second)
}

// ---------- read loop ----------

func (c *Client) readLoop(ctx context.Context) {
	defer close(c.done)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		req, resp, err := c.transport.ReadAnyMessage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				debug.Log("acp-client", "agent %q process EOF", c.def.Def.Name)
				return
			}
			debug.Log("acp-client", "agent %q read error: %v", c.def.Def.Name, err)
			continue
		}

		// Response to our pending request
		if resp != nil {
			c.transport.DeliverResponse(resp)
			continue
		}

		// Request/notification FROM the agent
		if req != nil {
			c.handleAgentRequest(ctx, req)
		}
	}
}

func (c *Client) handleAgentRequest(ctx context.Context, req *JSONRPCRequest) {
	switch req.Method {
	case "session/update":
		c.handleSessionUpdate(req)

	case "fs/read_text_file":
		c.handleFSRead(ctx, req)

	case "fs/write_text_file":
		c.handleFSWrite(ctx, req)

	case "session/prompt_complete":
		c.handlePromptComplete(req)

	case "session/request_permission":
		c.handlePermission(ctx, req)

	case "terminal/create":
		c.handleTerminalCreate(req)

	case "terminal/output":
		c.handleTerminalOutput(req)

	case "terminal/wait_for_exit":
		c.handleTerminalWaitForExit(req)

	case "terminal/kill":
		c.handleTerminalKill(req)

	case "terminal/release":
		c.handleTerminalRelease(req)

	default:
		if req.ID != nil {
			_ = c.transport.WriteError(req.ID, -32601, "host does not support: "+req.Method)
		}
	}
}

// ---------- session/update ----------

func (c *Client) handleSessionUpdate(req *JSONRPCRequest) {
	var notif SessionNotification
	if err := json.Unmarshal(req.Params, &notif); err != nil {
		debug.Log("acp-client", "parse session/update: %v", err)
		return
	}

	c.promptMu.Lock()
	defer c.promptMu.Unlock()

	switch notif.Update.Type {
	case UpdateAgentMessageChunk:
		if notif.Update.Content != nil {
			if cb, ok := notif.Update.Content.(*ContentBlock); ok && cb != nil {
				c.promptText.WriteString(cb.Text)
			} else {
				// Content might be a map — try to extract text
				if raw, err := json.Marshal(notif.Update.Content); err == nil {
					var block ContentBlock
					if json.Unmarshal(raw, &block) == nil {
						c.promptText.WriteString(block.Text)
					}
				}
			}
		}

	case UpdateToolCall:
		c.promptTools = append(c.promptTools, ToolCallSummary{
			Name:   notif.Update.Title,
			Title:  notif.Update.Title,
			Status: string(notif.Update.Status),
		})

	case UpdateToolCallUpdate:
		// Update existing tool call status
		for i := range c.promptTools {
			if c.promptTools[i].Name == notif.Update.ToolCallID || c.promptTools[i].Title == notif.Update.Title {
				c.promptTools[i].Status = string(notif.Update.Status)
				break
			}
		}
	}
}

func (c *Client) handlePromptComplete(req *JSONRPCRequest) {
	var notif PromptCompleteNotification
	if err := json.Unmarshal(req.Params, &notif); err != nil {
		debug.Log("acp-client", "parse session/prompt_complete: %v", err)
		return
	}

	c.promptMu.Lock()
	defer c.promptMu.Unlock()
	if c.activePromptID == "" || notif.SessionID != c.activePromptID || c.promptDone == nil {
		return
	}
	select {
	case c.promptDone <- notif.Response:
	default:
	}
}

// ---------- FS operations ----------

func (c *Client) handleFSRead(ctx context.Context, req *JSONRPCRequest) {
	var params ReadTextFileRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		_ = c.transport.WriteError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	absPath, err := c.resolvePath(params.Path)
	if err != nil {
		_ = c.transport.WriteError(req.ID, -32000, err.Error())
		return
	}
	if err := c.authorizeFileTool(ctx, "read_file", absPath, req.Params); err != nil {
		_ = c.transport.WriteError(req.ID, -32000, err.Error())
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		_ = c.transport.WriteError(req.ID, -32000, fmt.Sprintf("read file: %v", err))
		return
	}

	_ = c.transport.WriteResponse(req.ID, ReadTextFileResponse{Content: string(data)})
}

func (c *Client) handleFSWrite(ctx context.Context, req *JSONRPCRequest) {
	var params WriteTextFileRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		_ = c.transport.WriteError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	absPath, err := c.resolvePath(params.Path)
	if err != nil {
		_ = c.transport.WriteError(req.ID, -32000, err.Error())
		return
	}
	if err := c.authorizeFileTool(ctx, "write_file", absPath, req.Params); err != nil {
		_ = c.transport.WriteError(req.ID, -32000, err.Error())
		return
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		_ = c.transport.WriteError(req.ID, -32000, fmt.Sprintf("mkdir: %v", err))
		return
	}

	if err := os.WriteFile(absPath, []byte(params.Content), 0o644); err != nil {
		_ = c.transport.WriteError(req.ID, -32000, fmt.Sprintf("write file: %v", err))
		return
	}

	_ = c.transport.WriteResponse(req.ID, WriteTextFileResponse{})
}

// ---------- Permission ----------

func (c *Client) resolvePath(path string) (string, error) {
	c.mu.Lock()
	workingDir := c.workingDir
	c.mu.Unlock()

	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	return absPath, nil
}

func (c *Client) authorizeFileTool(ctx context.Context, toolName, absPath string, rawParams json.RawMessage) error {
	if c.policy == nil {
		return nil
	}
	permissionInput, err := json.Marshal(map[string]string{"file_path": absPath})
	if err != nil {
		return fmt.Errorf("encode %s permission input: %w", toolName, err)
	}
	decision, err := c.policy.Check(toolName, permissionInput)
	if err != nil {
		return fmt.Errorf("%s permission check: %w", toolName, err)
	}
	if !c.policy.AllowedPathForTool(toolName, absPath) {
		decision = permission.Deny
	}
	switch decision {
	case permission.Allow:
		return nil
	case permission.Deny:
		return fmt.Errorf("%s denied for %s", toolName, absPath)
	case permission.Ask:
		if c.onApproval == nil {
			return fmt.Errorf("%s requires approval for %s", toolName, absPath)
		}
		if c.onApproval(ctx, toolName, string(rawParams)) != permission.Allow {
			return fmt.Errorf("%s denied for %s", toolName, absPath)
		}
		return nil
	default:
		return fmt.Errorf("%s permission returned unknown decision", toolName)
	}
}

func (c *Client) handlePermission(ctx context.Context, req *JSONRPCRequest) {
	var params RequestPermissionRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		_ = c.transport.WriteError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	resp, err := c.permissionRequestResponse(ctx, params, req.Params)
	if err != nil {
		_ = c.transport.WriteError(req.ID, -32000, err.Error())
		return
	}
	_ = c.transport.WriteResponse(req.ID, resp)
}

func (c *Client) permissionRequestResponse(ctx context.Context, params RequestPermissionRequest, rawParams json.RawMessage) (RequestPermissionResponse, error) {
	if c.onPermission != nil {
		return c.onPermission(ctx, params)
	}

	toolName, input := c.permissionApprovalContext(params, rawParams)
	if decision, handled, err := c.permissionPolicyDecision(toolName, input); err != nil {
		return RequestPermissionResponse{}, err
	} else if handled {
		return permissionDecisionResponse(params.Options, decision, true), nil
	}

	if c.onApproval != nil {
		decision := c.onApproval(ctx, toolName, string(input))
		preferPersistent := false
		if policyDecision, handled, err := c.permissionPolicyDecision(toolName, input); err == nil && handled && policyDecision == decision {
			preferPersistent = true
		}
		return permissionDecisionResponse(params.Options, decision, preferPersistent), nil
	}

	return RequestPermissionResponse{
		Outcome: RequestPermissionOutcome{
			Outcome: "rejected",
		},
	}, nil
}

func (c *Client) permissionApprovalContext(params RequestPermissionRequest, rawParams json.RawMessage) (string, json.RawMessage) {
	toolName := "delegate_" + sanitizePermissionKey(c.def.Def.Name) + "_permission"
	input := rawParams
	if params.ToolCall == nil {
		return toolName, input
	}
	kind := string(params.ToolCall.Kind)
	if kind == "" {
		kind = "other"
	}
	toolName = "delegate_" + sanitizePermissionKey(c.def.Def.Name) + "_" + sanitizePermissionKey(kind)
	if params.ToolCall.RawInput != "" {
		input = json.RawMessage(params.ToolCall.RawInput)
	}
	return toolName, input
}

func (c *Client) permissionPolicyDecision(toolName string, input json.RawMessage) (permission.Decision, bool, error) {
	if c.policy == nil {
		return permission.Ask, false, nil
	}
	decision, err := c.policy.Check(toolName, input)
	if err != nil {
		return permission.Ask, false, fmt.Errorf("%s permission check: %w", toolName, err)
	}
	if decision == permission.Ask {
		return decision, false, nil
	}
	return decision, true, nil
}

func permissionDecisionResponse(options []PermissionOption, decision permission.Decision, preferPersistent bool) RequestPermissionResponse {
	optionID, ok := selectPermissionOptionID(options, decision, preferPersistent)
	if !ok {
		return RequestPermissionResponse{
			Outcome: RequestPermissionOutcome{
				Outcome: "rejected",
			},
		}
	}
	return RequestPermissionResponse{
		Outcome: RequestPermissionOutcome{
			Outcome: "selected",
			SelectedOption: &SelectedPermissionOutcome{
				OptionID: optionID,
			},
		},
	}
}

func selectPermissionOptionID(options []PermissionOption, decision permission.Decision, preferPersistent bool) (string, bool) {
	kinds := []PermissionOptionKind{PermissionOptionRejectOnce, PermissionOptionRejectAlways}
	if preferPersistent {
		kinds = []PermissionOptionKind{PermissionOptionRejectAlways, PermissionOptionRejectOnce}
	}
	if decision == permission.Allow {
		kinds = []PermissionOptionKind{PermissionOptionAllowOnce, PermissionOptionAllowAlways}
		if preferPersistent {
			kinds = []PermissionOptionKind{PermissionOptionAllowAlways, PermissionOptionAllowOnce}
		}
	}
	for _, kind := range kinds {
		for _, option := range options {
			if option.Kind == kind {
				return option.OptionID, true
			}
		}
	}
	if decision == permission.Allow {
		return "", false
	}
	for _, option := range options {
		if option.Kind == "" {
			return option.OptionID, true
		}
	}
	return "", false
}

func sanitizePermissionKey(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "other"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "other"
	}
	return out
}

// ---------- Terminal (stub — not supported yet) ----------

func (c *Client) handleTerminalCreate(req *JSONRPCRequest) {
	_ = c.transport.WriteError(req.ID, -32001, "terminal operations not supported by ggcode ACP host")
}

func (c *Client) handleTerminalOutput(req *JSONRPCRequest) {
	_ = c.transport.WriteError(req.ID, -32001, "terminal operations not supported by ggcode ACP host")
}

func (c *Client) handleTerminalWaitForExit(req *JSONRPCRequest) {
	_ = c.transport.WriteError(req.ID, -32001, "terminal operations not supported by ggcode ACP host")
}

func (c *Client) handleTerminalKill(req *JSONRPCRequest) {
	_ = c.transport.WriteError(req.ID, -32001, "terminal operations not supported by ggcode ACP host")
}

func (c *Client) handleTerminalRelease(req *JSONRPCRequest) {
	_ = c.transport.WriteError(req.ID, -32001, "terminal operations not supported by ggcode ACP host")
}
