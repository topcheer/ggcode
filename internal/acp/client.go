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
	toolpkg "github.com/topcheer/ggcode/internal/tool"
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

const (
	defaultPromptIdleTimeout    = 5 * time.Minute
	defaultPromptRequestTimeout = 30 * time.Minute
	defaultCloseSessionTimeout  = 1 * time.Second
)

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
	readErr    error
	stderrTail outputTail
	activity   activityTrail

	// Prompt execution state
	promptMu       sync.Mutex
	promptText     strings.Builder
	promptTools    []ToolCallSummary
	activePromptID string
	promptDone     chan PromptResponse
	promptActivity chan struct{}
	promptOnEvent  func(toolpkg.ACPPromptEvent)
	promptIdleTime time.Duration
	promptReqTime  time.Duration
}

// NewClient creates a new ACP client for the given discovered agent.
func NewClient(agent DiscoveredAgent, workingDir string, policy permission.PermissionPolicy, mcpServers []MCPServer) *Client {
	return &Client{
		def:            agent,
		workingDir:     workingDir,
		policy:         policy,
		mcpServers:     cloneMCPServers(mcpServers),
		done:           make(chan struct{}),
		promptIdleTime: defaultPromptIdleTimeout,
		promptReqTime:  defaultPromptRequestTimeout,
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
	c.stderrTail.Reset()

	procCtx, cancelProc := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, c.def.Path, args...)
	cmd.Dir = workingDir
	cmd.Stderr = &c.stderrTail
	configureACPCommandProcess(cmd)

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
	c.readErr = nil

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
		_ = killACPProcess(cmd)
		_ = cmd.Wait()
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
func (c *Client) promptInternal(
	ctx context.Context,
	prompt string,
	onEvent func(toolpkg.ACPPromptEvent),
) (*PromptResult, error) {
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
	c.promptActivity = make(chan struct{}, 1)
	c.promptOnEvent = onEvent
	promptDone := c.promptDone
	promptActivity := c.promptActivity
	promptIdleTime := c.promptIdleTime
	c.promptMu.Unlock()
	c.activity.Reset()
	c.recordActivity("sent session/prompt prompt_len=%d", len(prompt))

	promptReq := PromptRequest{
		SessionID: sessionID,
		Prompt:    []ContentBlock{{Type: "text", Text: prompt}},
	}
	type promptRequestResult struct {
		response PromptResponse
		err      error
	}
	promptRespCh := make(chan promptRequestResult, 1)
	go func() {
		result, err := c.sendRequest("session/prompt", promptReq, c.promptReqTime)
		if err != nil {
			promptRespCh <- promptRequestResult{err: err}
			return
		}
		var resp PromptResponse
		if len(strings.TrimSpace(string(result))) > 0 && string(result) != "{}" && string(result) != "null" {
			if err := json.Unmarshal(result, &resp); err != nil {
				promptRespCh <- promptRequestResult{err: fmt.Errorf("parsing session/prompt response: %w", err)}
				return
			}
		}
		stop := "ack"
		if resp.StopReason != "" {
			stop = summarizeStopReason(resp.StopReason)
		}
		c.recordActivity("recv session/prompt response stop=%s", stop)
		promptRespCh <- promptRequestResult{response: resp}
	}()

	idleTimer := time.NewTimer(promptIdleTime)
	defer idleTimer.Stop()

	for {
		select {
		case resp := <-promptDone:
			c.promptMu.Lock()
			pr := &PromptResult{
				Text:       c.promptText.String(),
				StopReason: resp.StopReason,
				ToolCalls:  c.promptTools,
			}
			c.clearPromptStateLocked()
			c.promptMu.Unlock()
			return pr, nil
		case rpc := <-promptRespCh:
			if rpc.err != nil {
				c.promptMu.Lock()
				c.clearPromptStateLocked()
				c.promptMu.Unlock()
				return nil, rpc.err
			}
			if rpc.response.StopReason != "" {
				c.promptMu.Lock()
				pr := &PromptResult{
					Text:       c.promptText.String(),
					StopReason: rpc.response.StopReason,
					ToolCalls:  c.promptTools,
				}
				c.clearPromptStateLocked()
				c.promptMu.Unlock()
				return pr, nil
			}
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(promptIdleTime)
		case <-promptActivity:
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(promptIdleTime)
		case <-ctx.Done():
			c.sendCancel(sessionID)
			c.promptMu.Lock()
			c.clearPromptStateLocked()
			c.promptMu.Unlock()
			return nil, ctx.Err()
		case <-idleTimer.C:
			activity := c.activity.Snapshot()
			c.sendCancel(sessionID)
			c.promptMu.Lock()
			c.clearPromptStateLocked()
			c.promptMu.Unlock()
			return nil, c.annotateTimeoutError(fmt.Errorf("timeout waiting for agent prompt completion after %s", promptIdleTime), activity)
		case <-c.done:
			activity := c.activity.Snapshot()
			c.promptMu.Lock()
			c.clearPromptStateLocked()
			c.promptMu.Unlock()
			if readErr := c.getReadErr(); readErr != nil {
				return nil, c.annotatePromptError(fmt.Errorf("agent %q transport failed: %w", c.def.Def.Name, readErr), activity)
			}
			return nil, c.annotatePromptError(fmt.Errorf("agent %q process exited unexpectedly", c.def.Def.Name), activity)
		}
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
	stdin := c.stdin
	transport := c.transport
	c.mu.Unlock()

	if running && sessionID != "" {
		c.closeSession(sessionID)
	}

	if transport != nil {
		_ = transport.CloseWriter()
	} else if stdin != nil {
		_ = stdin.Close()
	}

	if cancelRead != nil {
		cancelRead()
	}

	if cancelProc != nil {
		cancelProc()
	}

	if cmd != nil && cmd.Process != nil {
		_ = killACPProcess(cmd)
		_ = cmd.Wait()
	}

	c.mu.Lock()
	c.running = false
	c.initialized = false
	c.cancelRead = nil
	c.cancelProc = nil
	c.cmd = nil
	c.stdin = nil
	c.transport = nil
	c.readErr = nil
	c.sessionID = ""
	c.sessionCWD = ""
	c.mu.Unlock()

	return nil
}

// sendRequest sends a JSON-RPC request via the transport and waits for response.
func (c *Client) sendRequest(method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	result, err := c.transport.SendRequest(method, params, timeout)
	if err == nil {
		return result, nil
	}
	return nil, c.annotateTimeoutError(err, c.activity.Snapshot())
}

// sendCancel sends a session/cancel notification.
func (c *Client) sendCancel(sessionID string) {
	_ = c.writeNotification("session/cancel", CancelNotification{
		SessionID: sessionID,
	})
}

func (c *Client) closeSession(sessionID string) {
	if c.transport == nil || sessionID == "" {
		return
	}
	_, _ = c.sendRequest("session/close", CloseSessionRequest{SessionID: sessionID}, defaultCloseSessionTimeout)
}

func (c *Client) annotateTimeoutError(err error, activity string) error {
	if err == nil {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, "timeout waiting for client response") &&
		!strings.Contains(msg, "timeout waiting for agent prompt completion") {
		return err
	}
	if activity != "" {
		err = fmt.Errorf("%w\nRecent ACP activity:\n%s", err, activity)
	}
	stderr := c.stderrTail.Snapshot()
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w\nRecent agent stderr:\n%s", err, stderr)
}

func (c *Client) annotatePromptError(err error, activity string) error {
	if err == nil {
		return nil
	}
	if activity != "" {
		err = fmt.Errorf("%w\nRecent ACP activity:\n%s", err, activity)
	}
	stderr := c.stderrTail.Snapshot()
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w\nRecent agent stderr:\n%s", err, stderr)
}

func (c *Client) setReadErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readErr = err
}

func (c *Client) getReadErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readErr
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
			c.recordActivity("transport read error=%s", summarizeError(err))
			c.setReadErr(err)
			debug.Log("acp-client", "agent %q read error: %v", c.def.Def.Name, err)
			return
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
		c.notePromptActivity()
		c.handleSessionUpdate(req)

	case "fs/read_text_file":
		c.notePromptActivity()
		c.handleFSRead(ctx, req)

	case "fs/write_text_file":
		c.notePromptActivity()
		c.handleFSWrite(ctx, req)

	case "session/prompt_complete":
		c.handlePromptComplete(req)

	case "session/request_permission":
		c.notePromptActivity()
		c.handlePermission(ctx, req)

	case "terminal/create":
		c.notePromptActivity()
		c.handleTerminalCreate(req)

	case "terminal/output":
		c.notePromptActivity()
		c.handleTerminalOutput(req)

	case "terminal/wait_for_exit":
		c.notePromptActivity()
		c.handleTerminalWaitForExit(req)

	case "terminal/kill":
		c.notePromptActivity()
		c.handleTerminalKill(req)

	case "terminal/release":
		c.notePromptActivity()
		c.handleTerminalRelease(req)

	default:
		c.recordActivity("recv unsupported method=%s", req.Method)
		if req.ID != nil {
			_ = c.writeError(req.ID, -32601, "host does not support: "+req.Method)
		}
	}
}

func (c *Client) clearPromptStateLocked() {
	c.activePromptID = ""
	c.promptDone = nil
	c.promptActivity = nil
	c.promptOnEvent = nil
}

func (c *Client) notePromptActivity() {
	c.promptMu.Lock()
	defer c.promptMu.Unlock()
	if c.activePromptID == "" || c.promptActivity == nil {
		return
	}
	select {
	case c.promptActivity <- struct{}{}:
	default:
	}
}

func (c *Client) recordActivity(format string, args ...interface{}) {
	c.activity.Add(fmt.Sprintf(format, args...))
}

func (c *Client) transportSnapshot() *Transport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transport
}

func (c *Client) writeNotification(method string, params interface{}) error {
	transport := c.transportSnapshot()
	if transport == nil {
		return nil
	}
	return transport.WriteNotification(method, params)
}

func (c *Client) writeResponse(id interface{}, result interface{}) error {
	transport := c.transportSnapshot()
	if transport == nil {
		return nil
	}
	return transport.WriteResponse(id, result)
}

func (c *Client) writeError(id interface{}, code int, message string) error {
	transport := c.transportSnapshot()
	if transport == nil {
		return nil
	}
	return transport.WriteError(id, code, message)
}

// ---------- session/update ----------

func (c *Client) handleSessionUpdate(req *JSONRPCRequest) {
	var notif SessionNotification
	if err := json.Unmarshal(req.Params, &notif); err != nil {
		c.recordActivity("session/update parse_error=%s", err)
		debug.Log("acp-client", "parse session/update: %v", err)
		return
	}
	c.recordActivity("%s", summarizeSessionUpdateActivity(notif.Update))

	c.promptMu.Lock()
	var emitted []toolpkg.ACPPromptEvent

	switch notif.Update.Type {
	case UpdateAgentMessageChunk:
		if block, ok := contentBlockFromAny(notif.Update.Content); ok {
			switch block.Type {
			case "", "text":
				c.promptText.WriteString(block.Text)
				if block.Text != "" {
					emitted = append(emitted, toolpkg.ACPPromptEvent{
						Type: toolpkg.ACPPromptEventText,
						Text: block.Text,
					})
				}
			}
		}

	case UpdateToolCall:
		toolName := acpToolName(notif.Update.Title, notif.Update.Kind)
		c.promptTools = append(c.promptTools, ToolCallSummary{
			Name:   notif.Update.ToolCallID,
			Title:  toolName,
			Status: string(notif.Update.Status),
		})
		emitted = append(emitted, toolpkg.ACPPromptEvent{
			Type:      toolpkg.ACPPromptEventToolCall,
			ToolID:    notif.Update.ToolCallID,
			ToolName:  toolName,
			ToolTitle: notif.Update.Title,
			ToolArgs:  rawMessageString(notif.Update.RawInput),
		})

	case UpdateToolCallUpdate:
		toolName := acpToolName(notif.Update.Title, notif.Update.Kind)
		for i := range c.promptTools {
			if c.promptTools[i].Name == notif.Update.ToolCallID {
				if toolName != "" {
					c.promptTools[i].Title = toolName
				}
				c.promptTools[i].Status = string(notif.Update.Status)
				break
			}
		}
		if notif.Update.Status == ToolCallStatusCompleted || notif.Update.Status == ToolCallStatusFailed {
			emitted = append(emitted, toolpkg.ACPPromptEvent{
				Type:      toolpkg.ACPPromptEventToolResult,
				ToolID:    notif.Update.ToolCallID,
				ToolName:  toolName,
				ToolTitle: notif.Update.Title,
				Result:    toolCallUpdateResult(notif.Update),
				IsError:   notif.Update.Status == ToolCallStatusFailed,
			})
		}
	}
	onEvent := c.promptOnEvent
	c.promptMu.Unlock()
	for _, event := range emitted {
		if onEvent != nil {
			onEvent(event)
		}
	}
}

func contentBlockFromAny(content interface{}) (ContentBlock, bool) {
	if content == nil {
		return ContentBlock{}, false
	}
	if block, ok := content.(*ContentBlock); ok && block != nil {
		return *block, true
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return ContentBlock{}, false
	}
	var block ContentBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		return ContentBlock{}, false
	}
	return block, true
}

func rawMessageString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	text := strings.TrimSpace(string(raw))
	if text == "null" {
		return ""
	}
	return text
}

func summarizeSessionUpdateActivity(update SessionUpdate) string {
	switch update.Type {
	case UpdateAgentMessageChunk:
		if block, ok := contentBlockFromAny(update.Content); ok {
			switch block.Type {
			case "", "text":
				return fmt.Sprintf("session/update text_chunk len=%d", len(block.Text))
			default:
				return fmt.Sprintf("session/update content type=%s", summarizeText(block.Type))
			}
		}
		return "session/update agent_message_chunk"
	case UpdateToolCall:
		return fmt.Sprintf("session/update tool_call id=%s title=%s kind=%s", summarizeText(update.ToolCallID), summarizeText(update.Title), summarizeText(string(update.Kind)))
	case UpdateToolCallUpdate:
		result := summarizeRawJSONText(update.RawOutput)
		if result != "" {
			return fmt.Sprintf("session/update tool_call_update id=%s title=%s status=%s result=%s", summarizeText(update.ToolCallID), summarizeText(update.Title), summarizeText(string(update.Status)), summarizeText(result))
		}
		return fmt.Sprintf("session/update tool_call_update id=%s title=%s status=%s", summarizeText(update.ToolCallID), summarizeText(update.Title), summarizeText(string(update.Status)))
	default:
		return fmt.Sprintf("session/update type=%s", summarizeText(string(update.Type)))
	}
}

func summarizePermissionActivity(params RequestPermissionRequest) string {
	title := ""
	kind := ""
	if params.ToolCall != nil {
		title = params.ToolCall.Title
		kind = string(params.ToolCall.Kind)
	}
	options := summarizePermissionOptions(params.Options)
	if title != "" || kind != "" {
		return fmt.Sprintf("title=%s kind=%s options=%d %s", summarizeText(title), summarizeText(kind), len(params.Options), options)
	}
	return fmt.Sprintf("options=%d %s", len(params.Options), options)
}

func summarizePermissionResponseActivity(resp RequestPermissionResponse) string {
	if resp.Outcome.SelectedOption != nil {
		return fmt.Sprintf("outcome=%s option=%s", summarizeText(resp.Outcome.Outcome), summarizeText(resp.Outcome.SelectedOption.OptionID))
	}
	return fmt.Sprintf("outcome=%s", summarizeText(resp.Outcome.Outcome))
}

func summarizeRawJSONText(raw json.RawMessage) string {
	text := rawMessageString(raw)
	if text == "" {
		return ""
	}
	var quoted string
	if err := json.Unmarshal(raw, &quoted); err == nil {
		return quoted
	}
	return text
}

func summarizeStopReason(reason StopReason) string {
	if reason == "" {
		return "unknown"
	}
	return string(reason)
}

func summarizePath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "-"
	}
	return summarizeText(path)
}

func summarizeError(err error) string {
	if err == nil {
		return "-"
	}
	return summarizeText(err.Error())
}

func summarizeText(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return "-"
	}
	return text
}

func summarizePermissionOptions(options []PermissionOption) string {
	if len(options) == 0 {
		return "option_ids=-"
	}
	parts := make([]string, 0, len(options))
	for _, option := range options {
		part := summarizeText(option.OptionID)
		if option.Kind != "" {
			part += ":" + summarizeText(string(option.Kind))
		}
		parts = append(parts, part)
	}
	return "option_ids=" + strings.Join(parts, ",")
}

func acpToolName(title string, kind ToolKind) string {
	if strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	if strings.TrimSpace(string(kind)) != "" {
		return strings.TrimSpace(string(kind))
	}
	return "tool"
}

func toolCallUpdateResult(update SessionUpdate) string {
	if out := rawMessageString(update.RawOutput); out != "" {
		return out
	}
	if entries, ok := toolCallEntriesFromAny(update.Content); ok {
		parts := make([]string, 0, len(entries))
		for _, entry := range entries {
			switch entry.Type {
			case "content":
				if entry.Content != nil {
					if text := strings.TrimSpace(entry.Content.Text); text != "" {
						parts = append(parts, text)
					}
				}
			case "diff":
				if path := strings.TrimSpace(entry.Path); path != "" {
					parts = append(parts, "updated "+path)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	if update.Status != "" {
		return string(update.Status)
	}
	return ""
}

func toolCallEntriesFromAny(content interface{}) ([]ToolCallContentEntry, bool) {
	if content == nil {
		return nil, false
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return nil, false
	}
	var entries []ToolCallContentEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, false
	}
	return entries, true
}

func (c *Client) handlePromptComplete(req *JSONRPCRequest) {
	var notif PromptCompleteNotification
	if err := json.Unmarshal(req.Params, &notif); err != nil {
		c.recordActivity("session/prompt_complete parse_error=%s", err)
		debug.Log("acp-client", "parse session/prompt_complete: %v", err)
		return
	}
	c.recordActivity("recv session/prompt_complete stop=%s", summarizeStopReason(notif.Response.StopReason))

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
		c.recordActivity("fs/read_text_file parse_error=%s", err)
		_ = c.writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	c.recordActivity("recv fs/read_text_file path=%s", summarizePath(params.Path))

	absPath, err := c.resolvePath(params.Path)
	if err != nil {
		c.recordActivity("fs/read_text_file error=%s", summarizeError(err))
		_ = c.writeError(req.ID, -32000, err.Error())
		return
	}
	if err := c.authorizeFileTool(ctx, "read_file", absPath, req.Params); err != nil {
		c.recordActivity("fs/read_text_file denied path=%s error=%s", summarizePath(absPath), summarizeError(err))
		_ = c.writeError(req.ID, -32000, err.Error())
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		c.recordActivity("fs/read_text_file error path=%s error=%s", summarizePath(absPath), summarizeError(err))
		_ = c.writeError(req.ID, -32000, fmt.Sprintf("read file: %v", err))
		return
	}

	c.recordActivity("sent fs/read_text_file path=%s bytes=%d", summarizePath(absPath), len(data))
	_ = c.writeResponse(req.ID, ReadTextFileResponse{Content: string(data)})
}

func (c *Client) handleFSWrite(ctx context.Context, req *JSONRPCRequest) {
	var params WriteTextFileRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.recordActivity("fs/write_text_file parse_error=%s", err)
		_ = c.writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	c.recordActivity("recv fs/write_text_file path=%s content_len=%d", summarizePath(params.Path), len(params.Content))

	absPath, err := c.resolvePath(params.Path)
	if err != nil {
		c.recordActivity("fs/write_text_file error=%s", summarizeError(err))
		_ = c.writeError(req.ID, -32000, err.Error())
		return
	}
	if err := c.authorizeFileTool(ctx, "write_file", absPath, req.Params); err != nil {
		c.recordActivity("fs/write_text_file denied path=%s error=%s", summarizePath(absPath), summarizeError(err))
		_ = c.writeError(req.ID, -32000, err.Error())
		return
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.recordActivity("fs/write_text_file mkdir_error path=%s error=%s", summarizePath(dir), summarizeError(err))
		_ = c.writeError(req.ID, -32000, fmt.Sprintf("mkdir: %v", err))
		return
	}

	if err := os.WriteFile(absPath, []byte(params.Content), 0o644); err != nil {
		c.recordActivity("fs/write_text_file error path=%s error=%s", summarizePath(absPath), summarizeError(err))
		_ = c.writeError(req.ID, -32000, fmt.Sprintf("write file: %v", err))
		return
	}

	c.recordActivity("sent fs/write_text_file path=%s", summarizePath(absPath))
	_ = c.writeResponse(req.ID, WriteTextFileResponse{})
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
		c.recordActivity("session/request_permission parse_error=%s", err)
		_ = c.writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	c.recordActivity("recv session/request_permission %s", summarizePermissionActivity(params))

	resp, err := c.permissionRequestResponse(ctx, params, req.Params)
	if err != nil {
		c.recordActivity("session/request_permission error=%s", summarizeError(err))
		_ = c.writeError(req.ID, -32000, err.Error())
		return
	}
	c.recordActivity("sent session/request_permission %s", summarizePermissionResponseActivity(resp))
	_ = c.writeResponse(req.ID, resp)
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
	if len(params.ToolCall.RawInput) > 0 {
		input = params.ToolCall.RawInput
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
			if permissionOptionKindMatches(option.Kind, kind) {
				return option.OptionID, true
			}
		}
	}
	for _, optionID := range preferredPermissionOptionIDs(decision, preferPersistent) {
		for _, option := range options {
			if strings.EqualFold(strings.TrimSpace(option.OptionID), optionID) {
				return option.OptionID, true
			}
		}
	}
	if decision == permission.Allow {
		return "", false
	}
	for _, option := range options {
		if option.Kind == "" || permissionOptionKindMatches(option.Kind, PermissionOptionRejectOnce) || permissionOptionKindMatches(option.Kind, PermissionOptionRejectAlways) {
			return option.OptionID, true
		}
	}
	return "", false
}

func permissionOptionKindMatches(actual, expected PermissionOptionKind) bool {
	if actual == expected {
		return true
	}
	switch expected {
	case PermissionOptionAllowOnce:
		return actual == "allow_once"
	case PermissionOptionAllowAlways:
		return actual == "allow_always"
	case PermissionOptionRejectOnce:
		return actual == "reject_once" || actual == "deny_once"
	case PermissionOptionRejectAlways:
		return actual == "reject_always" || actual == "deny_always"
	default:
		return false
	}
}

func preferredPermissionOptionIDs(decision permission.Decision, preferPersistent bool) []string {
	if decision == permission.Allow {
		if preferPersistent {
			return []string{"approve_for_session", "allow_always", "allow_once", "allow"}
		}
		return []string{"allow_once", "allow_always", "allow", "approve_for_session"}
	}
	if preferPersistent {
		return []string{"reject_always", "deny_always", "reject_once", "deny_once", "reject", "deny", "cancel"}
	}
	return []string{"reject_once", "deny_once", "reject_always", "deny_always", "reject", "deny", "cancel"}
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
	var params CreateTerminalRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.recordActivity("terminal/create parse_error=%s", err)
		_ = c.writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	c.recordActivity("recv terminal/create command=%s cwd=%s rejected=unsupported", summarizeText(params.Command), summarizePath(params.CWD))
	_ = c.writeError(req.ID, -32001, "terminal operations not supported by ggcode ACP host")
}

func (c *Client) handleTerminalOutput(req *JSONRPCRequest) {
	var params TerminalOutputRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.recordActivity("terminal/output parse_error=%s", err)
		_ = c.writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	c.recordActivity("recv terminal/output terminal_id=%s rejected=unsupported", summarizeText(params.TerminalID))
	_ = c.writeError(req.ID, -32001, "terminal operations not supported by ggcode ACP host")
}

func (c *Client) handleTerminalWaitForExit(req *JSONRPCRequest) {
	var params WaitForTerminalExitRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.recordActivity("terminal/wait_for_exit parse_error=%s", err)
		_ = c.writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	c.recordActivity("recv terminal/wait_for_exit terminal_id=%s rejected=unsupported", summarizeText(params.TerminalID))
	_ = c.writeError(req.ID, -32001, "terminal operations not supported by ggcode ACP host")
}

func (c *Client) handleTerminalKill(req *JSONRPCRequest) {
	var params KillTerminalRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.recordActivity("terminal/kill parse_error=%s", err)
		_ = c.writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	c.recordActivity("recv terminal/kill terminal_id=%s rejected=unsupported", summarizeText(params.TerminalID))
	_ = c.writeError(req.ID, -32001, "terminal operations not supported by ggcode ACP host")
}

func (c *Client) handleTerminalRelease(req *JSONRPCRequest) {
	var params ReleaseTerminalRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.recordActivity("terminal/release parse_error=%s", err)
		_ = c.writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	c.recordActivity("recv terminal/release terminal_id=%s rejected=unsupported", summarizeText(params.TerminalID))
	_ = c.writeError(req.ID, -32001, "terminal operations not supported by ggcode ACP host")
}
