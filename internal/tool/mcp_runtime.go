package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type MCPServerSnapshot struct {
	Name          string
	Connected     bool
	Pending       bool
	Error         string
	ToolNames     []string
	PromptNames   []string
	ResourceNames []string
}

type MCPPromptMessage struct {
	Role string
	Text string
	Raw  string
}

type MCPPromptResult struct {
	Description string
	Messages    []MCPPromptMessage
}

type MCPResourceContent struct {
	URI      string
	MIMEType string
	Text     string
	Blob     string
}

type MCPResourceResult struct {
	Contents []MCPResourceContent
}

type MCPRuntime interface {
	SnapshotMCP() []MCPServerSnapshot
	GetPrompt(ctx context.Context, server, name string, args map[string]interface{}) (*MCPPromptResult, error)
	ReadResource(ctx context.Context, server, uri string) (*MCPResourceResult, error)
}

type ListMCPCapabilitiesTool struct {
	Runtime MCPRuntime
}

func (t ListMCPCapabilitiesTool) Name() string { return "list_mcp_capabilities" }
func (t ListMCPCapabilitiesTool) Description() string {
	return "List connected MCP servers and their available tools, prompts, and resources. Use this before reading an MCP resource or fetching an MCP prompt."
}
func (t ListMCPCapabilitiesTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"server": {
				"type": "string",
				"description": "Optional MCP server name to filter to a single server"
			}
		}
	}`)
}
func (t ListMCPCapabilitiesTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Server string `json:"server"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Runtime == nil {
		return Result{IsError: true, Content: "MCP runtime is unavailable"}, nil
	}
	snapshots := t.Runtime.SnapshotMCP()
	var sb strings.Builder
	count := 0
	for _, snap := range snapshots {
		if args.Server != "" && snap.Name != args.Server {
			continue
		}
		count++
		status := "failed"
		switch {
		case snap.Connected:
			status = "connected"
		case snap.Pending:
			status = "pending"
		}
		sb.WriteString(fmt.Sprintf("- %s (%s)\n", snap.Name, status))
		if snap.Error != "" {
			sb.WriteString(fmt.Sprintf("  error: %s\n", snap.Error))
		}
		sb.WriteString(fmt.Sprintf("  tools: %s\n", joinOrNone(snap.ToolNames)))
		sb.WriteString(fmt.Sprintf("  prompts: %s\n", joinOrNone(snap.PromptNames)))
		sb.WriteString(fmt.Sprintf("  resources: %s\n", joinOrNone(snap.ResourceNames)))
	}
	if count == 0 {
		if args.Server != "" {
			return Result{IsError: true, Content: fmt.Sprintf("MCP server %q not found", args.Server)}, nil
		}
		return Result{Content: "No MCP servers available."}, nil
	}
	return Result{Content: strings.TrimSpace(sb.String())}, nil
}

type GetMCPPromptTool struct {
	Runtime MCPRuntime
}

func (t GetMCPPromptTool) Name() string { return "get_mcp_prompt" }
func (t GetMCPPromptTool) Description() string {
	return "Fetch a prompt template from a connected MCP server."
}
func (t GetMCPPromptTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"server": {"type": "string", "description": "MCP server name"},
			"name": {"type": "string", "description": "Prompt name"},
			"arguments": {"type": "object", "description": "Optional prompt arguments"}
		},
		"required": ["server", "name"]
	}`)
}
func (t GetMCPPromptTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Server    string                 `json:"server"`
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Runtime == nil {
		return Result{IsError: true, Content: "MCP runtime is unavailable"}, nil
	}
	if strings.TrimSpace(args.Server) == "" || strings.TrimSpace(args.Name) == "" {
		return Result{IsError: true, Content: "server and name are required"}, nil
	}
	result, err := t.Runtime.GetPrompt(ctx, args.Server, args.Name, args.Arguments)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	var sb strings.Builder
	if result.Description != "" {
		sb.WriteString(result.Description)
		sb.WriteString("\n\n")
	}
	for i, msg := range result.Messages {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(fmt.Sprintf("[%s]\n", firstNonEmptyString(msg.Role, "message")))
		if msg.Text != "" {
			sb.WriteString(msg.Text)
		} else {
			sb.WriteString(msg.Raw)
		}
	}
	return Result{Content: strings.TrimSpace(sb.String())}, nil
}

type ReadMCPResourceTool struct {
	Runtime MCPRuntime
}

func (t ReadMCPResourceTool) Name() string { return "read_mcp_resource" }
func (t ReadMCPResourceTool) Description() string {
	return "Read a resource exposed by a connected MCP server."
}
func (t ReadMCPResourceTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"server": {"type": "string", "description": "MCP server name"},
			"uri": {"type": "string", "description": "Resource URI"}
		},
		"required": ["server", "uri"]
	}`)
}
func (t ReadMCPResourceTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Server string `json:"server"`
		URI    string `json:"uri"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Runtime == nil {
		return Result{IsError: true, Content: "MCP runtime is unavailable"}, nil
	}
	if strings.TrimSpace(args.Server) == "" || strings.TrimSpace(args.URI) == "" {
		return Result{IsError: true, Content: "server and uri are required"}, nil
	}
	result, err := t.Runtime.ReadResource(ctx, args.Server, args.URI)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	var sb strings.Builder
	for i, content := range result.Contents {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(fmt.Sprintf("URI: %s\n", content.URI))
		if content.MIMEType != "" {
			sb.WriteString(fmt.Sprintf("MIME: %s\n", content.MIMEType))
		}
		if content.Text != "" {
			sb.WriteString("\n")
			sb.WriteString(content.Text)
			continue
		}
		if content.Blob != "" {
			sb.WriteString(fmt.Sprintf("\n[binary content, %d base64 chars]", len(content.Blob)))
		}
	}
	return Result{Content: strings.TrimSpace(sb.String())}, nil
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, ", ")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
