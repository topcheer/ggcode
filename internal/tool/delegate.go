package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ACPAgentRegistry is an interface for discovering and using ACP agents.
// Implemented by acp.ClientManager to avoid circular imports.
type ACPAgentRegistry interface {
	Available() []string
	AgentInfo(name string) (title, description string, ok bool)
	Get(ctx context.Context, name string) (ACPAgentClient, error)
}

// ACPAgentClient is an interface for sending prompts to an ACP agent.
// Implemented by acp.Client.
type ACPAgentClient interface {
	Prompt(ctx context.Context, prompt string) (*ACPPromptResult, error)
}

// ACPPromptResult is the result from an ACP agent prompt execution.
type ACPPromptResult struct {
	Text       string
	StopReason string
	ToolCalls  []ACPToolCallSummary
}

// ACPToolCallSummary is a summary of a tool call made by the agent.
type ACPToolCallSummary struct {
	Name   string
	Title  string
	Status string
}

// DelegateTool delegates a task to an external ACP agent.
// The tool is only registered when at least one ACP agent is discovered.
type DelegateTool struct {
	Manager ACPAgentRegistry
}

func (t DelegateTool) Name() string { return "delegate" }

func (t DelegateTool) Description() string {
	agents := t.Manager.Available()
	if len(agents) == 0 {
		return "Delegate a task to an external AI coding agent. No agents currently available."
	}

	var descs []string
	for _, name := range agents {
		title, desc, _ := t.Manager.AgentInfo(name)
		if desc != "" {
			descs = append(descs, fmt.Sprintf("- **%s** (%s): %s", name, title, desc))
		} else {
			descs = append(descs, fmt.Sprintf("- **%s** (%s)", name, title))
		}
	}

	return fmt.Sprintf(`Delegate a task to an external AI coding agent.

Available agents (auto-detected from your system):
%s

The agent will execute the task autonomously in the current working directory and return the result.
Each agent uses its own API key and billing — no additional configuration needed.

Use this when:
- The user explicitly asks a specific agent to do something (e.g. "let copilot analyze this")
- You want a second opinion from a different AI model
- You want to leverage agent-specific capabilities`, strings.Join(descs, "\n"))
}

func (t DelegateTool) Parameters() json.RawMessage {
	agents := t.Manager.Available()
	sort.Strings(agents)

	enumBytes, _ := json.Marshal(agents)

	return json.RawMessage(fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"agent": {
				"type": "string",
				"enum": %s,
				"description": "The agent to delegate to"
			},
			"prompt": {
				"type": "string",
				"description": "The task description to send to the agent. Be specific and include all necessary context — the agent has access to the current working directory."
			}
		},
		"required": ["agent", "prompt"]
	}`, string(enumBytes)))
}

func (t DelegateTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params struct {
		Agent  string `json:"agent"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{}, fmt.Errorf("parsing delegate params: %w", err)
	}

	if params.Agent == "" {
		return Result{Content: "delegate: 'agent' parameter is required", IsError: true}, nil
	}
	if params.Prompt == "" {
		return Result{Content: "delegate: 'prompt' parameter is required", IsError: true}, nil
	}

	client, err := t.Manager.Get(ctx, params.Agent)
	if err != nil {
		return Result{Content: fmt.Sprintf("Agent %q is not available: %v", params.Agent, err), IsError: true}, nil
	}

	result, err := client.Prompt(ctx, params.Prompt)
	if err != nil {
		return Result{Content: fmt.Sprintf("Agent %q error: %v", params.Agent, err), IsError: true}, nil
	}

	// Build result with agent attribution
	title, _, _ := t.Manager.AgentInfo(params.Agent)
	output := fmt.Sprintf("[Response from %s]\n\n%s", title, result.Text)

	if len(result.ToolCalls) > 0 {
		output += "\n\nTools used:"
		for _, tc := range result.ToolCalls {
			status := tc.Status
			if status == "" {
				status = "completed"
			}
			output += fmt.Sprintf("\n  - %s (%s)", tc.Title, status)
		}
	}

	return Result{Content: output}, nil
}
