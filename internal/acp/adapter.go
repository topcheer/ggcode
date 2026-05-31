package acp

import (
	"context"

	"github.com/topcheer/ggcode/internal/tool"
)

// Compile-time interface checks.
var (
	_ tool.ACPAgentRegistry = (*ClientManager)(nil)
	_ tool.ACPAgentClient   = (*Client)(nil)
)

// Get returns a running client by name, starting it lazily if needed.
// Satisfies tool.ACPAgentRegistry.
func (m *ClientManager) Get(ctx context.Context, name string) (tool.ACPAgentClient, error) {
	return m.getInternal(ctx, name)
}

// getInternal is the unexported implementation that returns *Client.
func (m *ClientManager) getInternal(ctx context.Context, name string) (*Client, error) {
	m.mu.Lock()
	client, ok := m.clients[name]
	if !ok {
		m.mu.Unlock()
		return nil, ErrAgentNotFound{name: name}
	}
	m.mu.Unlock()

	// Start the agent if not running
	if err := client.Start(ctx); err != nil {
		return nil, err
	}

	// Create a session if not yet created
	client.mu.Lock()
	hasSession := client.sessionID != ""
	client.mu.Unlock()

	if !hasSession {
		if err := client.NewSession(ctx, m.workingDir); err != nil {
			return nil, err
		}
	}

	return client, nil
}

// Prompt sends a prompt and collects the full response.
// Satisfies tool.ACPAgentClient by converting to tool types.
func (c *Client) Prompt(ctx context.Context, prompt string) (*tool.ACPPromptResult, error) {
	result, err := c.promptInternal(ctx, prompt)
	if err != nil {
		return nil, err
	}

	tc := make([]tool.ACPToolCallSummary, len(result.ToolCalls))
	for i, t := range result.ToolCalls {
		tc[i] = tool.ACPToolCallSummary{
			Name:   t.Name,
			Title:  t.Title,
			Status: t.Status,
		}
	}

	return &tool.ACPPromptResult{
		Text:       result.Text,
		StopReason: string(result.StopReason),
		ToolCalls:  tc,
	}, nil
}

// ErrAgentNotFound is returned when the requested agent is not installed.
type ErrAgentNotFound struct {
	name string
}

func (e ErrAgentNotFound) Error() string {
	return "agent " + e.name + " not found — not installed or not in $PATH"
}
