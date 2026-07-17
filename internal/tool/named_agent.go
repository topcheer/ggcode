package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/subagent"
)

// CreateNamedAgentTool implements the create_namedagent tool.
// It lets the LLM define reusable subagent templates that are persisted
// per-workspace and can be invoked via use_namedagent.
type CreateNamedAgentTool struct {
	Store *subagent.TemplateStore
}

func (t CreateNamedAgentTool) Name() string { return "create_namedagent" }

func (t CreateNamedAgentTool) Description() string {
	return `Define or update a named subagent template with a custom system prompt, tool restrictions, and model override.

Named subagents are persisted per-workspace and can be reused across sessions with use_namedagent.
They are ideal for recurring specialized roles like code review, test generation, or documentation.

Template fields:
- name: Unique identifier (e.g., "code-reviewer", "test-writer")
- description: One-line summary of what this subagent does
- system_prompt: Complete instructions defining the subagent's role, expertise, and constraints
- tools: Optional allowlist of built-in tool names. If omitted, all tools are available (minus blocked ones)
- blocked_tools: Optional denylist of tool names to exclude (applied after tools allowlist)
- mcp_servers: Optional list of MCP server names whose tools should be included
- model: Optional model name override (any model on the current endpoint). Empty inherits parent's model`
}

func (t CreateNamedAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {
				"type": "string",
				"description": "Unique name for this subagent template (e.g., 'code-reviewer')"
			},
			"description": {
				"type": "string",
				"description": "One-line description of what this subagent does"
			},
			"system_prompt": {
				"type": "string",
				"description": "Complete system prompt defining the subagent's role, expertise, behavior, and constraints"
			},
			"tools": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Optional allowlist of built-in tool names. Defaults to all (minus blocked). Example: [\"read_file\", \"grep\", \"git_diff\"]"
			},
			"blocked_tools": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Optional denylist of tool names to exclude, applied after the allowlist"
			},
			"mcp_servers": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Optional list of MCP server names to include tools from"
			},
			"model": {
				"type": "string",
				"description": "Optional model name override (any model on the current endpoint)"
			}
		},
		"required": ["name", "description", "system_prompt"]
	}`)
}

func (t CreateNamedAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Store == nil {
		return Result{IsError: true, Content: "create_namedagent: template store not available"}, nil
	}
	var args struct {
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		SystemPrompt string   `json:"system_prompt"`
		Tools        []string `json:"tools"`
		BlockedTools []string `json:"blocked_tools"`
		MCPServers   []string `json:"mcp_servers"`
		Model        string   `json:"model"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{IsError: true, Content: "name is required"}, nil
	}
	if strings.TrimSpace(args.SystemPrompt) == "" {
		return Result{IsError: true, Content: "system_prompt is required"}, nil
	}

	tmpl := subagent.NamedAgentTemplate{
		Name:         strings.TrimSpace(args.Name),
		Description:  strings.TrimSpace(args.Description),
		SystemPrompt: args.SystemPrompt,
		Tools:        args.Tools,
		BlockedTools: args.BlockedTools,
		MCPServers:   args.MCPServers,
		Model:        args.Model,
	}

	// Check existence BEFORE save to determine created vs updated
	_, isUpdate := t.Store.LoadExisting(tmpl.Name)
	if err := t.Store.Save(tmpl); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save template: %v", err)}, nil
	}

	verb := "created"
	if isUpdate {
		verb = "updated"
	}

	toolInfo := "all (minus blocked)"
	if len(args.Tools) > 0 {
		toolInfo = strings.Join(args.Tools, ", ")
	}
	mcpInfo := "none"
	if len(args.MCPServers) > 0 {
		mcpInfo = strings.Join(args.MCPServers, ", ")
	}
	modelInfo := "inherited from parent"
	if args.Model != "" {
		modelInfo = args.Model
	}

	return Result{Content: fmt.Sprintf("Named subagent '%s' %s successfully.\nDescription: %s\nTools: %s\nMCP servers: %s\nModel: %s\n\nUse use_namedagent to invoke it with a task.",
		tmpl.Name, verb, tmpl.Description, toolInfo, mcpInfo, modelInfo)}, nil
}

// DeleteNamedAgentTool implements the delete_namedagent tool.
type DeleteNamedAgentTool struct {
	Store *subagent.TemplateStore
}

func (t DeleteNamedAgentTool) Name() string { return "delete_namedagent" }

func (t DeleteNamedAgentTool) Description() string {
	return `Delete a named subagent template from the current workspace.

This permanently removes the template. It cannot be undone.
The template will no longer appear in the system prompt or be callable via use_namedagent.

Use list_namedagent to see available templates before deleting.`
}

func (t DeleteNamedAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {
				"type": "string",
				"description": "Name of the subagent template to delete (e.g., 'code-reviewer')"
			}
		},
		"required": ["name"]
	}`)
}

func (t DeleteNamedAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Store == nil {
		return Result{IsError: true, Content: "delete_namedagent: template store not available"}, nil
	}
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{IsError: true, Content: "name is required"}, nil
	}

	// Verify the template exists before deleting for a better error message.
	if _, err := t.Store.Load(args.Name); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("named subagent '%s' not found", args.Name)}, nil
	}

	if err := t.Store.Delete(args.Name); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to delete template: %v", err)}, nil
	}

	return Result{Content: fmt.Sprintf("Named subagent '%s' deleted successfully.", args.Name)}, nil
}

// ListNamedAgentTool implements the list_namedagent tool.
type ListNamedAgentTool struct {
	Store *subagent.TemplateStore
}

func (t ListNamedAgentTool) Name() string { return "list_namedagent" }

func (t ListNamedAgentTool) Description() string {
	return "List all named subagent templates available in the current workspace."
}

func (t ListNamedAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t ListNamedAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Store == nil {
		return Result{IsError: true, Content: "list_namedagent: template store not available"}, nil
	}
	templates, err := t.Store.List()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to list templates: %v", err)}, nil
	}
	if len(templates) == 0 {
		return Result{Content: "No named subagent templates found in this workspace. Use create_namedagent to create one."}, nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Named subagent templates (%d):\n\n", len(templates)))
	for _, tmpl := range templates {
		toolInfo := "all tools"
		if len(tmpl.Tools) > 0 {
			toolInfo = strings.Join(tmpl.Tools, ", ")
		}
		modelInfo := "inherited"
		if tmpl.Model != "" {
			modelInfo = tmpl.Model
		}
		sb.WriteString(fmt.Sprintf("- **%s**: %s (tools: %s, model: %s)\n", tmpl.Name, tmpl.Description, toolInfo, modelInfo))
	}
	sb.WriteString("\nUse use_namedagent to invoke any of these with a task.")
	return Result{Content: sb.String()}, nil
}
