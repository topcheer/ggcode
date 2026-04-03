package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// Client connects to an MCP server via stdio transport.
type Client struct {
	name    string
	command string
	args    []string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.Reader
	reader  *bufio.Reader // reused stdout reader
	mu      sync.Mutex
	nextID  atomic.Int64
	closed  bool
}

// NewClient creates a new MCP client for the given server config.
func NewClient(name, command string, args []string) *Client {
	return &Client{
		name:    name,
		command: command,
		args:    args,
	}
}

// Start launches the MCP server process.
func (c *Client) Start(ctx context.Context) error {
	c.cmd = exec.CommandContext(ctx, c.command, c.args...)

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp[%s]: stdin pipe: %w", c.name, err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp[%s]: stdout pipe: %w", c.name, err)
	}

	c.stdin = stdin
	c.stdout = stdout
	c.reader = bufio.NewReader(stdout)

	// Capture stderr for debugging
	c.cmd.Stderr = nil // let it go to parent's stderr

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
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}
	return nil
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

	if err := c.writeMessage(req); err != nil {
		return err
	}

	// Read response
	resp, err := c.readResponse(ctx)
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
	return c.writeMessage(notif)
}

func (c *Client) writeMessage(msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := c.stdin.Write([]byte(header)); err != nil {
		return err
	}
	_, err = c.stdin.Write(data)
	return err
}

func (c *Client) readResponse(ctx context.Context) (*Response, error) {
	reader := c.reader

	// Read Content-Length header
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading header: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Content-Length:") {
			lengthStr := strings.TrimPrefix(line, "Content-Length:")
			lengthStr = strings.TrimSpace(lengthStr)
			var contentLength int
			if _, err := fmt.Sscanf(lengthStr, "%d", &contentLength); err != nil {
				return nil, fmt.Errorf("parsing Content-Length: %w", err)
			}

			// Read the empty line after headers
			for {
				sep, err := reader.ReadString('\n')
				if err != nil {
					return nil, fmt.Errorf("reading header separator: %w", err)
				}
				if strings.TrimSpace(sep) == "" {
					break
				}
			}

			// Read body
			body := make([]byte, contentLength)
			if _, err := io.ReadFull(reader, body); err != nil {
				return nil, fmt.Errorf("reading body: %w", err)
			}

			msg, err := ParseMessage(body)
			if err != nil {
				return nil, err
			}
			resp, ok := msg.(*Response)
			if !ok {
				return nil, fmt.Errorf("expected response, got %T", msg)
			}
			return resp, nil
		}
	}
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
