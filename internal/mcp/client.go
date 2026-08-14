package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

// Client connects to an MCP server via stdio transport.
type Client struct {
	name              string
	transport         string
	command           string
	args              []string
	env               map[string]string
	url               string
	headers           map[string]string
	cmd               *exec.Cmd
	procCancel        context.CancelFunc
	stdin             io.WriteCloser
	stdout            io.Reader
	reader            *bufio.Reader // reused stdout reader
	httpClient        *http.Client
	wsMu              sync.Mutex                // serializes ReadMessage on wsConn (fix #138)
	readMu            sync.Mutex                // serializes reads on the shared stdio bufio.Reader and response matching (fix #156)
	waiters           map[string]chan *Response // stdio response waiters keyed by request ID JSON (guarded by mu)
	wsConn            *websocket.Conn
	sessionID         string
	negotiatedVersion string // protocol version agreed upon during initialize
	mu                sync.Mutex
	stderrMu          sync.RWMutex
	stderrBuf         strings.Builder
	abortOnce         sync.Once
	nextID            atomic.Int64
	closed            atomic.Bool
	oauthHandler      *OAuthHandler

	// processExit is closed when the stdio server process exits unexpectedly
	// (i.e., not via Close/Abort). Consumers can use this for auto-reconnect.
	// It is closed at most once; nil for non-stdio transports.
	processExit chan struct{}

	// notificationHandler is called for every server-initiated notification
	// (e.g., notifications/tools/list_changed, notifications/message).
	// If nil, notifications are silently dropped (legacy behavior).
	notificationHandler func(method string, params json.RawMessage)

	// samplingHandler processes sampling/createMessage requests from the
	// server. If nil, sampling requests are rejected with an error.
	samplingHandler SamplingHandler

	// elicitationHandler processes elicitation/create requests from the
	// server (MCP 2025-06-18+). If nil, elicitation requests are rejected
	// with an error.
	elicitationHandler ElicitationHandler

	// serverCaps holds the capabilities advertised by the server during
	// initialize. Used to gate feature-specific requests (e.g., logging).
	serverCaps ServerCaps
}

// NewClient creates a new MCP client for the given server config.
func NewClient(name, command string, args []string) *Client {
	return &Client{
		name:      name,
		transport: "stdio",
		command:   command,
		args:      args,
	}
}

func NewClientFromConfig(cfg config.MCPServerConfig) *Client {
	transport := strings.ToLower(strings.TrimSpace(cfg.Type))
	if transport == "" {
		transport = "stdio"
	}
	client := &Client{
		name:      cfg.Name,
		transport: transport,
		command:   cfg.Command,
		args:      append([]string(nil), cfg.Args...),
		env:       cloneStringMap(cfg.Env),
		url:       cfg.URL,
		headers:   cloneStringMap(cfg.Headers),
	}
	if transport == "http" {
		client.oauthHandler = NewOAuthHandler(cfg.Name, cfg.URL, auth.DefaultStore())
		if cfg.OAuthClientID != "" {
			client.oauthHandler.SetClientCredentials(cfg.OAuthClientID, cfg.OAuthClientSecret)
		}
	}
	return client
}

// Start launches the MCP server process.
func (c *Client) Start(ctx context.Context) error {
	switch c.transport {
	case "http":
		c.httpClient = newMCPHTTPClient(0) // no client-level timeout; per-request context used
		return nil
	case "ws", "websocket":
		headers := http.Header{}
		for key, value := range c.headers {
			headers.Set(key, value)
		}
		dialer := *websocket.DefaultDialer
		dialer.Proxy = http.ProxyFromEnvironment
		conn, _, err := dialer.DialContext(ctx, c.url, headers)
		if err != nil {
			return fmt.Errorf("mcp[%s]: websocket dial: %w", c.name, err)
		}
		c.wsConn = conn
		return nil
	case "", "stdio":
	default:
		return fmt.Errorf("mcp[%s]: unsupported transport %q", c.name, c.transport)
	}

	procCtx, cancelProc := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, c.command, c.args...)
	configureMCPCommandProcess(cmd)
	if len(c.env) > 0 {
		cmd.Env = append(os.Environ(), flattenEnvMap(c.env)...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancelProc()
		return fmt.Errorf("mcp[%s]: stdin pipe: %w", c.name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelProc()
		return fmt.Errorf("mcp[%s]: stdout pipe: %w", c.name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancelProc()
		return fmt.Errorf("mcp[%s]: stderr pipe: %w", c.name, err)
	}
	safego.Go("mcp.captureStderr", func() { c.captureStderr(stderr) })

	if err := cmd.Start(); err != nil {
		cancelProc()
		return fmt.Errorf("mcp[%s]: starting server: %w", c.name, err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.procCancel = cancelProc
	c.stdin = stdin
	c.stdout = stdout
	c.reader = bufio.NewReader(stdout)
	c.processExit = make(chan struct{})
	c.mu.Unlock()

	// Monitor process exit. When the process dies on its own (not via
	// Close/Abort), close the processExit channel so watchers can react.
	safego.Go("mcp.procWatch", func() {
		_ = cmd.Wait()
		// Only signal unexpected exit if we haven't been closed by the user.
		if !c.closed.Load() {
			c.closed.Store(true)
			close(c.processExit)
			debug.Log("mcp-procwatch", "server=%s process exited unexpectedly", c.name)
		}
	})

	return nil
}

// latestMCPProtocolVersion is the latest MCP protocol version this client
// supports. The client sends this during initialize; the server may negotiate
// down to an older version.
const latestMCPProtocolVersion = "2025-11-25"

// knownMCPProtocolVersions lists all protocol versions this client accepts.
// If the server negotiates to a version not in this set, initialization fails.
var knownMCPProtocolVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
}

// NegotiatedVersion returns the MCP protocol version agreed upon during
// initialize, or empty string if not yet initialized.
func (c *Client) NegotiatedVersion() string {
	return c.negotiatedVersion
}

// Initialize sends the initialize request and returns server capabilities.
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	caps := ClientCaps{
		Roots: struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{ListChanged: true},
	}
	if c.samplingHandler != nil {
		caps.Sampling = &struct{}{}
	}
	if c.elicitationHandler != nil {
		caps.Elicitation = &struct{}{}
	}
	params := InitializeParams{
		ProtocolVersion: latestMCPProtocolVersion,
		Capabilities:    caps,
		ClientInfo:      Implementation{Name: "ggcode", Version: "0.1.0"},
	}
	var result InitializeResult
	if err := c.sendRequest(ctx, "initialize", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: initialize: %w", c.name, err)
	}

	// Cache server capabilities for feature gating.
	c.serverCaps = result.Capabilities

	// Version negotiation: the server may respond with the same version
	// (if it supports ours) or a different one (its latest supported).
	// We accept any known version; reject unknown versions.
	serverVersion := result.ProtocolVersion
	if serverVersion == "" {
		return nil, fmt.Errorf("mcp[%s]: server returned empty protocolVersion", c.name)
	}
	if !knownMCPProtocolVersions[serverVersion] {
		return nil, fmt.Errorf("mcp[%s]: unsupported protocol version %q (client supports %s)",
			c.name, serverVersion, latestMCPProtocolVersion)
	}
	c.negotiatedVersion = serverVersion

	// Send initialized notification
	notif := Notification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	if err := c.sendNotification(ctx, notif); err != nil {
		return nil, fmt.Errorf("mcp[%s]: initialized notification: %w", c.name, err)
	}

	return &result, nil
}

// ListTools returns the tools provided by the MCP server.
func (c *Client) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	params := ListToolsParams{}
	var result ListToolsResult
	if err := c.sendRequest(ctx, "tools/list", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: tools/list: %w", c.name, err)
	}
	return result.Tools, nil
}

func (c *Client) ListPrompts(ctx context.Context) ([]PromptDefinition, error) {
	params := struct {
		Cursor string `json:"cursor,omitempty"`
	}{}
	var result ListPromptsResult
	if err := c.sendRequest(ctx, "prompts/list", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: prompts/list: %w", c.name, err)
	}
	return result.Prompts, nil
}

func (c *Client) ListResources(ctx context.Context) ([]ResourceDefinition, error) {
	params := struct {
		Cursor string `json:"cursor,omitempty"`
	}{}
	var result ListResourcesResult
	if err := c.sendRequest(ctx, "resources/list", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: resources/list: %w", c.name, err)
	}
	return result.Resources, nil
}

func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*GetPromptResult, error) {
	params := GetPromptParams{
		Name:      name,
		Arguments: args,
	}
	var result GetPromptResult
	if err := c.sendRequest(ctx, "prompts/get", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: prompts/get: %w", c.name, err)
	}
	return &result, nil
}

func (c *Client) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	params := ReadResourceParams{URI: uri}
	var result ReadResourceResult
	if err := c.sendRequest(ctx, "resources/read", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: resources/read: %w", c.name, err)
	}
	return &result, nil
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	params := CallToolParams{
		Name:      name,
		Arguments: args,
	}
	var result CallToolResult
	if err := c.sendRequest(ctx, "tools/call", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: tools/call %s: %w", c.name, name, err)
	}
	return &result, nil
}

// Close terminates the server process.
func (c *Client) Close() error {
	// Capture cleanup targets BEFORE calling Abort(), because Abort()
	// unconditionally sets c.closed=true via abortOnce.Do — if we check
	// c.closed after Abort(), the cleanup block would always be skipped
	// (the original bug from issue #39).
	c.mu.Lock()
	if c.closed.Load() {
		c.mu.Unlock()
		return nil
	}
	c.closed.Store(true)
	cmd := c.cmd
	transport := c.transport
	c.sessionID = ""
	c.httpClient = nil
	c.procCancel = nil
	oauthHandler := c.oauthHandler
	c.oauthHandler = nil
	c.mu.Unlock()

	// Abort transports (without holding c.mu) so any in-flight
	// sendRequest/sendNotification holding the lock can unwind quickly.
	c.Abort()

	if oauthHandler != nil {
		oauthHandler.Close()
	}
	if (transport == "stdio" || transport == "") && cmd != nil {
		done := make(chan struct{})
		safego.Go("mcp.client.waitProcess", func() {
			_ = cmd.Wait()
			close(done)
		})
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	return nil
}

// ForceReauth deletes the server-name-specific OAuth credential (if any),
// leaving the canonical (shared) credential untouched. The next request will
// get a 401 and trigger a fresh OAuth flow bound to this server name.
func (c *Client) ForceReauth() error {
	c.mu.Lock()
	handler := c.oauthHandler
	c.mu.Unlock()
	if handler == nil {
		return nil
	}
	return handler.DeleteServerToken()
}

func (c *Client) Abort() {
	c.abortOnce.Do(func() {
		// Mark as closed atomically — Abort may be called from
		// sendRequest's cancel path which already holds c.mu.
		// atomic.Bool avoids both the data race and the deadlock.
		c.closed.Store(true)

		wsConn := c.wsConn
		stdin := c.stdin
		cmd := c.cmd
		procCancel := c.procCancel

		if wsConn != nil {
			_ = wsConn.Close()
		}
		if stdin != nil {
			_ = stdin.Close()
		}
		if procCancel != nil {
			procCancel()
		}
		if cmd != nil && cmd.Process != nil {
			// Kill the entire process group to prevent orphaned child processes.
			killProcessGroup(cmd)
		}
	})
}

// Name returns the MCP server name.
func (c *Client) Name() string { return c.name }

// ProcessExit returns a channel that is closed when the stdio server process
// exits unexpectedly. Returns nil for non-stdio transports or if the process
// hasn't started yet. Used by the auto-reconnect watcher.
func (c *Client) ProcessExit() <-chan struct{} {
	return c.processExit
}

// IsClosed returns whether the client has been closed (either explicitly or
// because the underlying process exited).
func (c *Client) IsClosed() bool {
	return c.closed.Load()
}

func (c *Client) nextRequestID() *ID {
	id := c.nextID.Add(1)
	i := NewIntID(id)
	return &i
}

// mcpRequestTimeout is the per-request deadline for all MCP requests
// (HTTP, WebSocket, and stdio). Prevents indefinite hangs when a server
// accepts the connection but never responds.
const mcpRequestTimeout = 120 * time.Second

func (c *Client) sendRequest(ctx context.Context, method string, params interface{}, result interface{}) error {
	if c.closed.Load() {
		return fmt.Errorf("mcp[%s]: connection closed", c.name)
	}

	// Apply per-request timeout so a slow/hung MCP server can't block forever.
	// If the caller's ctx already has a shorter deadline, that takes priority.
	ctx, cancel := context.WithTimeout(ctx, mcpRequestTimeout)
	defer cancel()

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("mcp[%s]: marshal params for %s: %w", c.name, method, err)
	}

	req := Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
		ID:      c.nextRequestID(),
	}

	// For WS/stdio transports, the send path includes a read loop that may
	// dispatch notifications. Those notification handlers can call back into
	// sendRequest (e.g. ListTools after tools/list_changed). If we hold c.mu
	// during the read loop, this reentrant call deadlocks. So we split:
	// 1) write under c.mu, 2) read without c.mu, using wsMu to serialize reads.
	resp, err := c.sendRequestUnlocked(req, ctx)
	if err != nil {
		return fmt.Errorf("mcp[%s]: send %s: %w", c.name, method, err)
	}

	if resp.IsError() {
		return resp.Error
	}

	if result != nil && resp.Result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("mcp[%s]: unmarshal result: %w", c.name, err)
		}
	}

	return nil
}

// sendRequestUnlocked sends a request and reads the response. c.mu is held
// only during the write phase, not the read phase (fix #138). The stdio read
// path is serialized by readMu in readResponseWithCancel (fix #156) so that
// concurrent sendRequest calls cannot interleave reads on the shared
// bufio.Reader.
func (c *Client) sendRequestUnlocked(req Request, ctx context.Context) (*Response, error) {
	switch c.transport {
	case "ws", "websocket":
		return c.sendWSUnlocked(ctx, req)
	case "http":
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.sendHTTP(ctx, req)
	default: // stdio
		c.mu.Lock()
		if err := c.writeMessage(req); err != nil {
			c.mu.Unlock()
			return nil, fmt.Errorf("mcp[%s]: write message: %w", c.name, err)
		}
		c.mu.Unlock()
		return c.readResponseWithCancel(ctx, req.ID)
	}
}

func (c *Client) sendNotification(ctx context.Context, notif Notification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	_, err := c.send(notif, ctx)
	return err
}

func (c *Client) send(msg interface{}, ctx context.Context) (*Response, error) {
	switch c.transport {
	case "http":
		return c.sendHTTP(ctx, msg)
	case "ws", "websocket":
		// Notifications only need a write, not a read loop.
		if _, ok := msg.(Notification); ok {
			return c.sendWSNotification(ctx, msg)
		}
		// Requests are handled by sendRequestUnlocked via sendWSUnlocked.
		// This path handles server-initiated requests (rare).
		req, _ := msg.(Request)
		return c.sendWSUnlocked(ctx, req)
	case "", "stdio":
		if err := c.writeMessage(msg); err != nil {
			return nil, fmt.Errorf("mcp[%s]: write message: %w", c.name, err)
		}
		switch msg.(type) {
		case Notification:
			return &Response{JSONRPC: "2.0"}, nil
		default:
			var reqID *ID
			if r, ok := msg.(Request); ok {
				reqID = r.ID
			}
			return c.readResponseWithCancel(ctx, reqID)
		}
	default:
		return nil, fmt.Errorf("mcp[%s]: unsupported transport %q", c.name, c.transport)
	}
}

func (c *Client) readResponseWithCancel(ctx context.Context, reqID *ID) (*Response, error) {
	type result struct {
		resp *Response
		err  error
	}
	// Register the waiter SYNCHRONOUSLY, before the read goroutine starts
	// (fix #156): another caller holding readMu may read our response as
	// soon as our request hits the wire, so the waiter must already be
	// registered by the time this function is entered post-write.
	var waiter chan *Response
	if reqID != nil {
		waiter = make(chan *Response, 1)
		c.registerWaiter(reqID, waiter)
	}
	done := make(chan result, 1)
	safego.Go("mcp.client.readResponse", func() {
		if waiter != nil {
			defer c.unregisterWaiter(reqID, waiter)
		}
		// Serialize the whole read loop (fix #156): concurrent sendRequest
		// calls on the same stdio client must not interleave Peek/ReadBytes/
		// ReadString calls on the shared bufio.Reader (not concurrency-safe).
		c.readMu.Lock()
		defer c.readMu.Unlock()
		resp, err := c.readResponse(ctx, reqID, waiter)
		done <- result{resp: resp, err: err}
	})
	select {
	case res := <-done:
		if err := ctx.Err(); err != nil {
			return nil, c.withStderr(err)
		}
		return res.resp, res.err
	case <-ctx.Done():
		c.Abort()
		res := <-done
		if err := ctx.Err(); err != nil {
			return nil, c.withStderr(err)
		}
		return res.resp, res.err
	}
}

func (c *Client) writeMessage(msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp[%s]: marshal message: %w", c.name, err)
	}
	if c.transport == "" || c.transport == "stdio" {
		data = append(data, '\n')
		_, err = c.stdin.Write(data)
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := c.stdin.Write([]byte(header)); err != nil {
		return err
	}
	_, err = c.stdin.Write(data)
	return err
}

func (c *Client) sendHTTP(ctx context.Context, msg interface{}) (*Response, error) {
	return c.sendHTTPWithRetry(ctx, msg, true)
}

func (c *Client) sendHTTPWithRetry(ctx context.Context, msg interface{}, allowRetry bool) (*Response, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: marshal http message: %w", c.name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: create request: %w", c.name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	authHeader := ""
	if c.oauthHandler != nil {
		if token, _ := c.oauthHandler.GetAccessToken(ctx); token != "" {
			authHeader = "Bearer " + token
			req.Header.Set("Authorization", authHeader)
			debug.Log("mcp-http", "send_with_token server=%s has_token=true", c.name)
		} else {
			debug.Log("mcp-http", "send_no_token server=%s", c.name)
		}
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: http request: %w", c.name, err)
	}
	defer resp.Body.Close()
	if sessionID := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); sessionID != "" {
		c.sessionID = sessionID
	}
	body, err := util.ReadAll(resp.Body, util.ReadLimitMCP)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: read http body: %w", c.name, err)
	}
	debug.Log("mcp-http", "response server=%s status=%d content_type=%s body_len=%d", c.name, resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && c.oauthHandler != nil {
		// 401 = no auth; 403 = auth present but insufficient permissions.
		// Both should trigger OAuth DCR discovery as a fallback — the user's
		// configured API key may have limited scope while OAuth grants broader access.
		needsOAuth, _ := c.oauthHandler.Handle401(resp)
		if allowRetry {
			if token, _ := c.oauthHandler.GetAccessToken(ctx); token != "" && "Bearer "+token != authHeader {
				// OAuth succeeded — permanently switch auth mode by removing the
				// user-configured API key header so future requests use OAuth token only.
				if _, hasUserAuth := c.headers["Authorization"]; hasUserAuth {
					delete(c.headers, "Authorization")
					debug.Log("mcp-http", "auth_switched server=%s from_apikey=true to_oauth=true", c.name)
				}
				debug.Log("mcp-http", "retry_after_discovery server=%s has_token=true", c.name)
				return c.sendHTTPWithRetry(ctx, msg, false)
			}
		}
		if needsOAuth {
			return nil, &OAuthRequiredError{Handler: c.oauthHandler}
		}
	}
	if resp.StatusCode >= 400 {
		bodyPreview := strings.TrimSpace(string(body))
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200]
		}
		return nil, fmt.Errorf("mcp[%s]: http status %d: %s", c.name, resp.StatusCode, bodyPreview)
	}
	switch msg.(type) {
	case Notification:
		return &Response{JSONRPC: "2.0"}, nil
	}
	return parseHTTPResponse(body, resp.Header.Get("Content-Type"))
}

// sendWSUnlocked writes a request under c.mu, then reads the response
// without holding c.mu (fix #138). This prevents reentrant deadlock when
// a notification handler calls back into sendRequest. wsMu serializes
// ReadMessage calls to avoid concurrent reads on the non-thread-safe
// websocket connection.
func (c *Client) sendWSUnlocked(ctx context.Context, req Request) (*Response, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: marshal ws message: %w", c.name, err)
	}
	// Write under c.mu to serialize WS writes.
	c.mu.Lock()
	if c.closed.Load() {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.wsConn.SetWriteDeadline(deadline)
		_ = c.wsConn.SetReadDeadline(deadline)
	}
	if err := c.wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp[%s]: websocket write: %w", c.name, err)
	}
	c.mu.Unlock()
	// Read loop — NOT holding c.mu so notification handlers can re-enter
	// sendRequest without deadlock. wsMu ensures only one goroutine reads.
	reqID := req.ID
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("mcp[%s]: context cancelled: %w", c.name, err)
		}
		c.wsMu.Lock()
		_, payload, err := c.wsConn.ReadMessage()
		c.wsMu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: websocket read: %w", c.name, err)
		}
		parsed, err := ParseMessage(payload)
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: parse ws message: %w", c.name, err)
		}
		switch typed := parsed.(type) {
		case *Response:
			if reqID != nil {
				reqIDJSON, _ := json.Marshal(reqID)
				if len(typed.ID) > 0 && string(typed.ID) != string(reqIDJSON) {
					debug.Log("mcp-ws", "server=%s dropping mismatched response ID", c.name)
					continue
				}
			}
			return typed, nil
		case *Notification:
			c.processNotification(typed)
			continue
		case *Request:
			_ = c.respondToServerRequestWS(typed)
			continue
		}
	}
}

// sendWSNotification writes a notification over WebSocket without entering
// a read loop (notifications are fire-and-forget).
func (c *Client) sendWSNotification(ctx context.Context, msg interface{}) (*Response, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: marshal ws notification: %w", c.name, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.wsConn.SetWriteDeadline(deadline)
	}
	if err := c.wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("mcp[%s]: websocket write: %w", c.name, err)
	}
	return &Response{JSONRPC: "2.0"}, nil
}

func parseHTTPResponse(body []byte, contentType string) (*Response, error) {
	payload := body
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		// Some MCP servers send Notification messages (e.g., logging/progress)
		// before the actual Response in the SSE stream. Parse all events and
		// return the first one that is a valid JSON-RPC Response.
		resp, err := extractSSEResponse(body)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
	// For non-SSE responses, also try the SSE extraction path as a fallback.
	// Some servers return content-type: application/json but actually send
	// newline-delimited JSON messages (Notification then Response) in a single
	// response body. If the first ParseMessage returns a Notification, try
	// extracting all SSE-style events to find the Response.
	msg, err := ParseMessage(payload)
	if err != nil {
		return nil, fmt.Errorf("parse message: %w", err)
	}
	resp, ok := msg.(*Response)
	if ok {
		return resp, nil
	}
	// First message was a Notification — try SSE extraction as fallback.
	debug.Log("mcp-http", "parseHTTPResponse: first message was %T, trying SSE fallback", msg)
	if r, err := extractSSEResponse(body); err == nil {
		return r, nil
	}
	return nil, fmt.Errorf("expected response, got %T", msg)
}

// extractSSEResponse parses ALL SSE events from the body and returns the first
// one that parses as a JSON-RPC Response. This handles servers that send
// Notification messages (logging, progress) before the actual Response.
func extractSSEResponse(body []byte) (*Response, error) {
	events := extractAllSSEData(body)
	if len(events) == 0 {
		return nil, fmt.Errorf("parsing SSE response: no data event found")
	}
	var lastParseErr error
	for _, payload := range events {
		msg, err := ParseMessage(payload)
		if err != nil {
			lastParseErr = err
			continue
		}
		if resp, ok := msg.(*Response); ok {
			return resp, nil
		}
		// Skip notifications — keep looking for the Response
		debug.Log("mcp-http", "parseHTTPResponse: skipping non-response SSE event %T", msg)
	}
	if lastParseErr != nil {
		return nil, fmt.Errorf("parsing SSE response: no valid JSON-RPC message found: %w", lastParseErr)
	}
	return nil, fmt.Errorf("parsing SSE response: no Response found in %d event(s)", len(events))
}

func extractAllSSEData(body []byte) [][]byte {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	var events [][]byte
	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case strings.TrimSpace(line) == "" && len(dataLines) > 0:
			events = append(events, []byte(strings.Join(dataLines, "\n")))
			dataLines = nil
		}
	}
	if len(dataLines) > 0 {
		events = append(events, []byte(strings.Join(dataLines, "\n")))
	}
	return events
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func flattenEnvMap(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	flat := make([]string, 0, len(values))
	for key, value := range values {
		flat = append(flat, key+"="+value)
	}
	return flat
}

// respondToServerRequestWS handles server-initiated requests received over
// WebSocket transport. Sends an error response since the agent doesn't support
// sampling/elicitation over WS (keeps the server from blocking indefinitely).
func (c *Client) respondToServerRequestWS(req *Request) error {
	if req == nil || req.ID == nil {
		return nil
	}
	errResp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"error": map[string]interface{}{
			"code":    -32601,
			"message": fmt.Sprintf("method not supported over WebSocket: %s", req.Method),
		},
	}
	data, err := json.Marshal(errResp)
	if err != nil {
		return err
	}
	return c.wsConn.WriteMessage(websocket.TextMessage, data)
}

func (c *Client) readResponse(ctx context.Context, reqID *ID, waiter chan *Response) (*Response, error) {
	for {
		if waiter != nil {
			select {
			case resp := <-waiter:
				return resp, nil
			default:
			}
		}
		msg, err := c.readMessage(ctx)
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: read message: %w", c.name, err)
		}
		switch typed := msg.(type) {
		case *Response:
			// Under concurrent requests a response may belong to a different
			// caller. Forward foreign responses to their waiter instead of
			// misattributing them (fix #156).
			if reqID != nil && len(typed.ID) > 0 && !responseIDMatches(typed.ID, reqID) {
				c.deliverResponse(typed)
				continue
			}
			return typed, nil
		case *Notification:
			c.processNotification(typed)
			continue
		case *Request:
			if err := c.handleServerRequest(typed); err != nil {
				return nil, fmt.Errorf("mcp[%s]: handle server request: %w", c.name, err)
			}
		default:
			return nil, c.withStderr(fmt.Errorf("unexpected MCP message type %T", msg))
		}
	}
}

// responseIDMatches reports whether a raw JSON-RPC response ID equals the
// given request ID (fix #156).
func responseIDMatches(raw json.RawMessage, reqID *ID) bool {
	if reqID == nil || len(raw) == 0 {
		return true
	}
	reqJSON, err := json.Marshal(reqID)
	if err != nil {
		return false
	}
	return bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(reqJSON))
}

func (c *Client) registerWaiter(reqID *ID, ch chan *Response) {
	key, err := json.Marshal(reqID)
	if err != nil {
		return
	}
	c.mu.Lock()
	if c.waiters == nil {
		c.waiters = make(map[string]chan *Response)
	}
	c.waiters[string(key)] = ch
	c.mu.Unlock()
}

func (c *Client) unregisterWaiter(reqID *ID, ch chan *Response) {
	key, err := json.Marshal(reqID)
	if err != nil {
		return
	}
	c.mu.Lock()
	if cur, ok := c.waiters[string(key)]; ok && cur == ch {
		delete(c.waiters, string(key))
	}
	c.mu.Unlock()
}

// deliverResponse hands a response belonging to another concurrent caller to
// that caller's waiter channel (fix #156).
func (c *Client) deliverResponse(resp *Response) {
	if len(resp.ID) == 0 {
		return
	}
	key := string(bytes.TrimSpace(resp.ID))
	c.mu.Lock()
	ch, ok := c.waiters[key]
	c.mu.Unlock()
	if ok {
		select {
		case ch <- resp:
		default:
		}
		return
	}
	debug.Log("mcp-stdio", "server=%s dropping response with unknown ID %s", c.name, key)
}

func (c *Client) readMessage(ctx context.Context) (interface{}, error) {
	reader := c.reader
	for {
		select {
		case <-ctx.Done():
			return nil, c.withStderr(ctx.Err())
		default:
		}

		peek, err := reader.Peek(1)
		if err != nil {
			return nil, c.withStderr(fmt.Errorf("reading message: %w", err))
		}
		switch peek[0] {
		case '\r', '\n', ' ', '\t':
			if _, err := reader.ReadByte(); err != nil {
				return nil, c.withStderr(fmt.Errorf("discarding whitespace: %w", err))
			}
			continue
		case '{':
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return nil, c.withStderr(fmt.Errorf("reading message line: %w", err))
			}
			msg, err := ParseMessage(bytes.TrimSpace(line))
			if err != nil {
				return nil, c.withStderr(err)
			}
			return msg, nil
		default:
			return c.readHeaderFramedMessage(ctx)
		}
	}
}

func (c *Client) readHeaderFramedMessage(ctx context.Context) (interface{}, error) {
	reader := c.reader
	contentLength := -1

	for {
		select {
		case <-ctx.Done():
			return nil, c.withStderr(ctx.Err())
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, c.withStderr(fmt.Errorf("reading header: %w", err))
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if contentLength >= 0 {
				break
			}
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			continue
		}
		if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &contentLength); err != nil {
			return nil, c.withStderr(fmt.Errorf("parsing Content-Length: %w", err))
		}
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, c.withStderr(fmt.Errorf("reading body: %w", err))
	}

	msg, err := ParseMessage(body)
	if err != nil {
		return nil, c.withStderr(err)
	}
	return msg, nil
}

func (c *Client) handleServerRequest(req *Request) error {
	if req == nil || req.ID == nil {
		return nil
	}
	switch req.Method {
	case "sampling/createMessage":
		return c.handleSampling(req)
	case "elicitation/create":
		return c.handleElicitation(req)
	case "roots/list":
		rootURI, err := currentRootURI()
		if err != nil {
			return c.writeErrorResponse(req.ID, -32603, err.Error())
		}
		return c.writeResultResponse(req.ID, map[string]any{
			"roots": []map[string]string{{"uri": rootURI}},
		})
	case "ping":
		return c.writeResultResponse(req.ID, map[string]any{})
	default:
		return c.writeErrorResponse(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// handleSampling processes a sampling/createMessage request from the MCP server.
// Servers use this to ask the client to generate an LLM completion on their behalf.
func (c *Client) handleSampling(req *Request) error {
	if c.samplingHandler == nil {
		return c.writeErrorResponse(req.ID, -32601, "sampling not supported")
	}

	params, err := ParseSamplingParams(req.Params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32602, fmt.Sprintf("invalid sampling params: %v", err))
	}

	// Use a bounded timeout to prevent runaway sampling from blocking the
	// MCP read loop. The handler itself may use a shorter context.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := c.samplingHandler(ctx, params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32603, fmt.Sprintf("sampling failed: %v", err))
	}
	return c.writeResultResponse(req.ID, result)
}

// handleElicitation processes an elicitation/create request from the MCP server.
// Servers use this to ask the client to collect structured input from the user.
func (c *Client) handleElicitation(req *Request) error {
	if c.elicitationHandler == nil {
		return c.writeErrorResponse(req.ID, -32601, "elicitation not supported")
	}

	params, err := ParseElicitationParams(req.Params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32602, fmt.Sprintf("invalid elicitation params: %v", err))
	}

	// Validate the server-provided schema before presenting it to the user.
	if err := ValidateElicitationSchema(params.Schema); err != nil {
		return c.writeErrorResponse(req.ID, -32602, fmt.Sprintf("invalid elicitation schema: %v", err))
	}

	// Bounded timeout to prevent blocking the MCP read loop indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := c.elicitationHandler(ctx, params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32603, fmt.Sprintf("elicitation failed: %v", err))
	}
	return c.writeResultResponse(req.ID, result)
}

// SetElicitationHandler registers a handler for elicitation/create requests.
// When set, the client advertises elicitation capability during initialize.
// Pass nil to disable elicitation support.
func (c *Client) SetElicitationHandler(h ElicitationHandler) {
	c.elicitationHandler = h
}

// SetSamplingHandler registers a handler for sampling/createMessage requests.
// When set, the client advertises sampling capability during initialize.
// Pass nil to disable sampling support.
func (c *Client) SetSamplingHandler(h SamplingHandler) {
	c.samplingHandler = h
}

func (c *Client) writeResultResponse(id *ID, result interface{}) error {
	data, err := json.Marshal(result)
	if err != nil {
		return c.withStderr(err)
	}
	return c.writeMessage(Response{
		JSONRPC: "2.0",
		ID:      marshalRequestID(id),
		Result:  data,
	})
}

func (c *Client) writeErrorResponse(id *ID, code int, message string) error {
	return c.writeMessage(Response{
		JSONRPC: "2.0",
		ID:      marshalRequestID(id),
		Error: &Error{
			Code:    code,
			Message: message,
		},
	})
}

func marshalRequestID(id *ID) json.RawMessage {
	if id == nil {
		return nil
	}
	data, err := json.Marshal(id)
	if err != nil {
		return nil
	}
	return data
}

func currentRootURI() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String(), nil
}

func (c *Client) captureStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c.appendStderr(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) appendStderr(data []byte) {
	if len(data) == 0 {
		return
	}
	const maxStderrBytes = 64 * 1024
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	remaining := maxStderrBytes - c.stderrBuf.Len()
	if remaining <= 0 {
		return
	}
	if len(data) > remaining {
		data = data[:remaining]
	}
	c.stderrBuf.Write(data)
}

func (c *Client) stderrSummary() string {
	c.stderrMu.RLock()
	defer c.stderrMu.RUnlock()
	text := strings.TrimSpace(c.stderrBuf.String())
	if text == "" {
		return ""
	}
	const maxSummary = 512
	if len(text) > maxSummary {
		text = text[len(text)-maxSummary:]
	}
	return strings.TrimSpace(text)
}

func (c *Client) withStderr(err error) error {
	if err == nil {
		return nil
	}
	if stderr := c.stderrSummary(); stderr != "" && !strings.Contains(err.Error(), stderr) {
		return fmt.Errorf("%w: server stderr: %s", err, stderr)
	}
	return err
}

// SetNotificationHandler registers a callback for server-initiated notifications.
// The handler receives the notification method (e.g., "notifications/tools/list_changed")
// and raw params. It is called from the read loop; handlers must not block.
// Pass nil to disable notification processing (notifications are silently dropped).
func (c *Client) SetNotificationHandler(h func(method string, params json.RawMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notificationHandler = h
}

// processNotification dispatches a server notification to the registered handler.
func (c *Client) processNotification(notif *Notification) {
	if notif == nil {
		return
	}
	// Read the handler pointer without acquiring c.mu. sendRequest holds c.mu
	// during the entire request/response cycle, and processNotification is
	// called from within that read loop. If we try to acquire c.mu here we
	// deadlock. A function-pointer read is atomic on 64-bit platforms; the
	// worst case is reading a stale pointer during handler replacement, which
	// is harmless (one notification delivered to the old handler).
	h := c.notificationHandler
	if h == nil {
		return
	}
	debug.Log("mcp-notif", "server=%s method=%s", c.name, notif.Method)
	// Call handler outside the lock to avoid deadlock if the handler
	// calls back into the client (e.g. sendRequest for resources).
	h(notif.Method, notif.Params)
}

// SetLevel requests the server to set its minimum logging level.
// The server must advertise the logging capability during initialize.
// Valid levels: "debug", "info", "notice", "warning", "error", "critical",
// "alert", "emergency".
func (c *Client) SetLevel(ctx context.Context, level string) error {
	if c.serverCaps.Logging == nil {
		return fmt.Errorf("mcp[%s]: server does not support logging", c.name)
	}
	params := struct {
		Level string `json:"level"`
	}{Level: level}
	if err := c.sendRequest(ctx, "logging/setLevel", params, nil); err != nil {
		return fmt.Errorf("mcp[%s]: logging/setLevel: %w", c.name, err)
	}
	return nil
}

// HasToolsListChanged returns true if the server advertised tools with listChanged.
func (c *Client) HasToolsListChanged() bool {
	return c.serverCaps.Tools != nil && c.serverCaps.Tools.ListChanged
}

// HasLogging returns true if the server supports the logging capability.
func (c *Client) HasLogging() bool {
	return c.serverCaps.Logging != nil
}

// HasResourceSubscribe returns true if the server supports resource subscriptions.
func (c *Client) HasResourceSubscribe() bool {
	return c.serverCaps.Resources != nil && c.serverCaps.Resources.Subscribe
}

// --- MCP Protocol Types ---

type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    ClientCaps     `json:"capabilities"`
	ClientInfo      Implementation `json:"clientInfo"`
}

type ClientCaps struct {
	Roots struct {
		ListChanged bool `json:"listChanged,omitempty"`
	} `json:"roots,omitempty"`
	Sampling    *struct{} `json:"sampling,omitempty"`
	Elicitation *struct{} `json:"elicitation,omitempty"`
}

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    ServerCaps     `json:"capabilities"`
	ServerInfo      Implementation `json:"serverInfo"`
}

type ServerCaps struct {
	Tools      *ToolsCapability     `json:"tools,omitempty"`
	Resources  *ResourcesCapability `json:"resources,omitempty"`
	Prompts    *PromptsCapability   `json:"prompts,omitempty"`
	Logging    *struct{}            `json:"logging,omitempty"`
	Completion *struct{}            `json:"completion,omitempty"`
}

// ToolsCapability describes the server's tool capabilities.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability describes the server's resource capabilities.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability describes the server's prompt capabilities.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ListToolsParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type ListToolsResult struct {
	Tools []ToolDefinition `json:"tools"`
}

type ListPromptsResult struct {
	Prompts []PromptDefinition `json:"prompts"`
}

type PromptDefinition struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type GetPromptParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

type PromptMessage struct {
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content"`
}

type ListResourcesResult struct {
	Resources []ResourceDefinition `json:"resources"`
}

type ResourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type ReadResourceParams struct {
	URI string `json:"uri"`
}

type ReadResourceResult struct {
	Contents []ResourceContent `json:"contents"`
}

type ResourceContent struct {
	URI      string `json:"uri,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
