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

	"github.com/topcheer/ggcode/internal/debug"
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

// Client manages a single ACP agent process.
// It handles lifecycle (start/stop), session management, and prompt execution.
type Client struct {
	def        DiscoveredAgent
	workingDir string

	// Process management
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	transport *Transport

	// State
	mu          sync.Mutex
	initialized bool
	caps        AgentCapabilities
	authMethods []AuthMethod
	agentInfo   ImplementationInfo
	sessionID   string
	running     bool

	// Permission handling
	onPermission PermissionHandler

	// Read loop management
	cancelRead context.CancelFunc
	done       chan struct{}

	// Prompt execution state
	promptMu    sync.Mutex
	promptText  strings.Builder
	promptTools []ToolCallSummary
}

// NewClient creates a new ACP client for the given discovered agent.
func NewClient(agent DiscoveredAgent, workingDir string) *Client {
	return &Client{
		def:        agent,
		workingDir: workingDir,
		done:       make(chan struct{}),
	}
}

// SetPermissionHandler sets the handler for agent permission requests.
func (c *Client) SetPermissionHandler(h PermissionHandler) {
	c.onPermission = h
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
	c.mu.Unlock()

	debug.Log("acp-client", "starting agent %q: %s %s", c.def.Def.Name, c.def.Path, strings.Join(c.def.Def.ACPCommand, " "))

	args := make([]string, len(c.def.Def.ACPCommand))
	copy(args, c.def.Def.ACPCommand)

	cmd := exec.CommandContext(ctx, c.def.Path, args...)
	cmd.Dir = c.workingDir
	cmd.Stderr = os.Stderr // let agent stderr pass through for debug

	// Wire stdin/stdout
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("creating stdin pipe for %s: %w", c.def.Def.Name, err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		return fmt.Errorf("creating stdout pipe for %s: %w", c.def.Def.Name, err)
	}

	if err := cmd.Start(); err != nil {
		stdinPipe.Close()
		return fmt.Errorf("starting process %s: %w", c.def.Def.Name, err)
	}

	c.cmd = cmd
	c.stdin = stdinPipe
	c.transport = NewTransport(stdoutPipe, stdinPipe)

	// Start read loop
	readCtx, cancelRead := context.WithCancel(context.Background())
	c.cancelRead = cancelRead
	c.done = make(chan struct{})
	go c.readLoop(readCtx)

	// Perform initialize handshake
	if err := c.initialize(ctx); err != nil {
		cancelRead()
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("initialize handshake with %s: %w", c.def.Def.Name, err)
	}

	c.mu.Lock()
	c.running = true
	c.mu.Unlock()

	debug.Log("acp-client", "agent %q started successfully (protocol=%d, loadSession=%v)",
		c.def.Def.Name, ProtocolVersion, c.caps.LoadSession)

	return nil
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
	c.mu.Unlock()

	params := NewSessionRequest{
		CWD: cwd,
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
	c.mu.Unlock()

	debug.Log("acp-client", "created session %s on agent %q", resp.SessionID, c.def.Def.Name)
	return nil
}

// promptInternal sends a prompt and collects the full response.
// Blocks until the agent completes (end_turn, error, etc.).
func (c *Client) promptInternal(ctx context.Context, prompt string) (*PromptResult, error) {
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
	c.promptMu.Unlock()

	// Send session/prompt — SendRequest handles request/response pairing.
	// The read loop goroutine collects session/update notifications into
	// promptText/promptTools as side effects while we wait for the response.
	promptReq := PromptRequest{
		SessionID: sessionID,
		Prompt:    []ContentBlock{{Type: "text", Text: prompt}},
	}

	resultCh := make(chan promptResult, 1)
	go func() {
		respRaw, err := c.sendRequest("session/prompt", promptReq, 10*time.Minute)
		if err != nil {
			resultCh <- promptResult{err: err}
			return
		}
		var resp PromptResponse
		if err := json.Unmarshal(respRaw, &resp); err != nil {
			resultCh <- promptResult{err: err}
			return
		}
		resultCh <- promptResult{stopReason: resp.StopReason}
	}()

	// Wait for completion or context cancellation
	select {
	case r := <-resultCh:
		if r.err != nil {
			return nil, r.err
		}
		c.promptMu.Lock()
		pr := &PromptResult{
			Text:       c.promptText.String(),
			StopReason: r.stopReason,
			ToolCalls:  c.promptTools,
		}
		c.promptMu.Unlock()
		return pr, nil

	case <-ctx.Done():
		c.sendCancel(sessionID)
		return nil, ctx.Err()

	case <-c.done:
		return nil, fmt.Errorf("agent %q process exited unexpectedly", c.def.Def.Name)
	}
}

// promptResult is an internal transport for the goroutine result.
type promptResult struct {
	stopReason StopReason
	err        error
}

// Close sends session/close (if session exists) and kills the process.
func (c *Client) Close() error {
	c.mu.Lock()
	sessionID := c.sessionID
	running := c.running
	c.mu.Unlock()

	if running && sessionID != "" {
		// Best-effort close session
		_, _ = c.sendRequest("session/close", CloseSessionRequest{SessionID: sessionID}, 5*time.Second)
	}

	if c.cancelRead != nil {
		c.cancelRead()
	}

	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}

	c.mu.Lock()
	c.running = false
	c.initialized = false
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
		c.handleFSRead(req)

	case "fs/write_text_file":
		c.handleFSWrite(req)

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

// ---------- FS operations ----------

func (c *Client) handleFSRead(req *JSONRPCRequest) {
	var params ReadTextFileRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		_ = c.transport.WriteError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	absPath := params.Path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(c.workingDir, params.Path)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		_ = c.transport.WriteError(req.ID, -32000, fmt.Sprintf("read file: %v", err))
		return
	}

	_ = c.transport.WriteResponse(req.ID, ReadTextFileResponse{Content: string(data)})
}

func (c *Client) handleFSWrite(req *JSONRPCRequest) {
	var params WriteTextFileRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		_ = c.transport.WriteError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	absPath := params.Path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(c.workingDir, params.Path)
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

func (c *Client) handlePermission(ctx context.Context, req *JSONRPCRequest) {
	var params RequestPermissionRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		_ = c.transport.WriteError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	if c.onPermission != nil {
		resp, err := c.onPermission(ctx, params)
		if err != nil {
			_ = c.transport.WriteError(req.ID, -32000, err.Error())
			return
		}
		_ = c.transport.WriteResponse(req.ID, resp)
		return
	}

	// Default: auto-approve
	_ = c.transport.WriteResponse(req.ID, RequestPermissionResponse{
		Outcome: RequestPermissionOutcome{
			Outcome: "selected",
			SelectedOption: &SelectedPermissionOutcome{
				OptionID: "allow",
			},
		},
	})
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
