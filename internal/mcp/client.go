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
)

// Client connects to an MCP server via stdio transport.
type Client struct {
	name         string
	transport    string
	command      string
	args         []string
	env          map[string]string
	url          string
	headers      map[string]string
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.Reader
	reader       *bufio.Reader // reused stdout reader
	httpClient   *http.Client
	wsConn       *websocket.Conn
	sessionID    string
	mu           sync.Mutex
	stderrMu     sync.RWMutex
	stderrBuf    strings.Builder
	abortOnce    sync.Once
	nextID       atomic.Int64
	closed       bool
	oauthHandler *OAuthHandler
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
		c.httpClient = &http.Client{}
		return nil
	case "ws", "websocket":
		headers := http.Header{}
		for key, value := range c.headers {
			headers.Set(key, value)
		}
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, headers)
		if err != nil {
			return fmt.Errorf("mcp[%s]: websocket dial: %w", c.name, err)
		}
		c.wsConn = conn
		return nil
	case "", "stdio":
	default:
		return fmt.Errorf("mcp[%s]: unsupported transport %q", c.name, c.transport)
	}

	c.cmd = exec.CommandContext(ctx, c.command, c.args...)
	configureMCPCommandProcess(c.cmd)
	if len(c.env) > 0 {
		c.cmd.Env = append(os.Environ(), flattenEnvMap(c.env)...)
	}

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp[%s]: stdin pipe: %w", c.name, err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp[%s]: stdout pipe: %w", c.name, err)
	}
	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("mcp[%s]: stderr pipe: %w", c.name, err)
	}

	c.stdin = stdin
	c.stdout = stdout
	c.reader = bufio.NewReader(stdout)
	go c.captureStderr(stderr)

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("mcp[%s]: starting server: %w", c.name, err)
	}

	return nil
}

// Initialize sends the initialize request and returns server capabilities.
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	params := InitializeParams{
		ProtocolVersion: "2024-11-05",
		ClientInfo:      Implementation{Name: "ggcode", Version: "0.1.0"},
	}
	var result InitializeResult
	if err := c.sendRequest(ctx, "initialize", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: initialize: %w", c.name, err)
	}

	// Send initialized notification
	notif := Notification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	if err := c.sendNotification(notif); err != nil {
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
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	cmd := c.cmd
	transport := c.transport
	c.sessionID = ""
	c.httpClient = nil
	oauthHandler := c.oauthHandler
	c.oauthHandler = nil
	c.mu.Unlock()

	if oauthHandler != nil {
		oauthHandler.Close()
	}
	c.Abort()
	if (transport == "stdio" || transport == "") && cmd != nil {
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	return nil
}

func (c *Client) Abort() {
	c.abortOnce.Do(func() {
		if c.wsConn != nil {
			_ = c.wsConn.Close()
		}
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
}

// Name returns the MCP server name.
func (c *Client) Name() string { return c.name }

func (c *Client) nextRequestID() *ID {
	id := c.nextID.Add(1)
	i := NewIntID(id)
	return &i
}

func (c *Client) sendRequest(ctx context.Context, method string, params interface{}, result interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("mcp[%s]: connection closed", c.name)
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return err
	}

	req := Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
		ID:      c.nextRequestID(),
	}

	resp, err := c.send(req, ctx)
	if err != nil {
		return err
	}

	if resp.IsError() {
		return resp.Error
	}

	if result != nil && resp.Result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("unmarshaling result: %w", err)
		}
	}

	return nil
}

func (c *Client) sendNotification(notif Notification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	_, err := c.send(notif, context.Background())
	return err
}

func (c *Client) send(msg interface{}, ctx context.Context) (*Response, error) {
	switch c.transport {
	case "http":
		return c.sendHTTP(ctx, msg)
	case "ws", "websocket":
		return c.sendWS(ctx, msg)
	case "", "stdio":
		if err := c.writeMessage(msg); err != nil {
			return nil, err
		}
		switch msg.(type) {
		case Notification:
			return &Response{JSONRPC: "2.0"}, nil
		default:
			return c.readResponseWithCancel(ctx)
		}
	default:
		return nil, fmt.Errorf("mcp[%s]: unsupported transport %q", c.name, c.transport)
	}
}

func (c *Client) readResponseWithCancel(ctx context.Context) (*Response, error) {
	type result struct {
		resp *Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := c.readResponse(ctx)
		done <- result{resp: resp, err: err}
	}()
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
		return err
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
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
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
	if c.oauthHandler != nil {
		if token, _ := c.oauthHandler.GetAccessToken(ctx); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: read http body: %w", c.name, err)
	}
	debug.Log("mcp-http", "response server=%s status=%d content_type=%s body_len=%d", c.name, resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
	if resp.StatusCode == http.StatusUnauthorized && c.oauthHandler != nil {
		needsOAuth, _ := c.oauthHandler.Handle401(resp)
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

func (c *Client) sendWS(ctx context.Context, msg interface{}) (*Response, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.wsConn.SetWriteDeadline(deadline)
		_ = c.wsConn.SetReadDeadline(deadline)
	}
	if err := c.wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("mcp[%s]: websocket write: %w", c.name, err)
	}
	switch msg.(type) {
	case Notification:
		return &Response{JSONRPC: "2.0"}, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, payload, err := c.wsConn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: websocket read: %w", c.name, err)
		}
		parsed, err := ParseMessage(payload)
		if err != nil {
			return nil, err
		}
		if resp, ok := parsed.(*Response); ok {
			return resp, nil
		}
	}
}

func parseHTTPResponse(body []byte, contentType string) (*Response, error) {
	payload := body
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		eventData := extractSSEData(body)
		if len(eventData) == 0 {
			return nil, fmt.Errorf("parsing SSE response: no data event found")
		}
		payload = eventData
	}
	msg, err := ParseMessage(payload)
	if err != nil {
		return nil, err
	}
	resp, ok := msg.(*Response)
	if !ok {
		return nil, fmt.Errorf("expected response, got %T", msg)
	}
	return resp, nil
}

func extractSSEData(body []byte) []byte {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	// MCP responses can have very large data lines (e.g., GitHub returns 100K+ tool lists).
	// The default bufio.MaxScanTokenSize (64KB) is too small.
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case strings.TrimSpace(line) == "" && len(dataLines) > 0:
			return []byte(strings.Join(dataLines, "\n"))
		}
	}
	if len(dataLines) == 0 {
		return nil
	}
	return []byte(strings.Join(dataLines, "\n"))
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

func (c *Client) readResponse(ctx context.Context) (*Response, error) {
	for {
		msg, err := c.readMessage(ctx)
		if err != nil {
			return nil, err
		}
		switch typed := msg.(type) {
		case *Response:
			return typed, nil
		case *Notification:
			continue
		case *Request:
			if err := c.handleServerRequest(typed); err != nil {
				return nil, err
			}
		default:
			return nil, c.withStderr(fmt.Errorf("unexpected MCP message type %T", msg))
		}
	}
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
	Tools *struct {
		ListChanged bool `json:"listChanged,omitempty"`
	} `json:"tools,omitempty"`
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
