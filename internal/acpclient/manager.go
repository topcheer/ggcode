package acpclient

import (
	"context"
	"encoding/json"

	acpgo "github.com/topcheer/ggcode-acp-go"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

type ClientManager struct {
	inner *acpgo.ClientManager
}

type Client struct {
	inner *acpgo.Client
}

type permissionPolicyAdapter struct {
	inner permission.PermissionPolicy
}

type debugLogger struct{}

func (debugLogger) Debugf(format string, args ...any) {
	debug.Log("acp-client", format, args...)
}

func NewClientManager(workingDir string, policy permission.PermissionPolicy) *ClientManager {
	acpgo.SetLogger(debugLogger{})
	return &ClientManager{inner: acpgo.NewClientManager(workingDir, permissionPolicyAdapter{inner: policy})}
}

func (m *ClientManager) Available() []string {
	if m == nil || m.inner == nil {
		return nil
	}
	return m.inner.Available()
}

func (m *ClientManager) AgentInfo(name string) (title, description string, ok bool) {
	if m == nil || m.inner == nil {
		return "", "", false
	}
	return m.inner.AgentInfo(name)
}

func (m *ClientManager) SetWorkingDir(dir string) {
	if m == nil || m.inner == nil {
		return
	}
	m.inner.SetWorkingDir(dir)
}

func (m *ClientManager) SetApprovalHandler(h func(context.Context, string, string) permission.Decision) {
	if m == nil || m.inner == nil {
		return
	}
	if h == nil {
		m.inner.SetApprovalHandler(nil)
		return
	}
	m.inner.SetApprovalHandler(func(ctx context.Context, toolName string, input string) acpgo.Decision {
		return toExternalDecision(h(ctx, toolName, input))
	})
}

func (m *ClientManager) CloseAll() {
	if m == nil || m.inner == nil {
		return
	}
	m.inner.CloseAll()
}

func (m *ClientManager) Get(ctx context.Context, name string) (tool.ACPAgentClient, error) {
	if m == nil || m.inner == nil {
		return nil, nil
	}
	client, err := m.inner.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return &Client{inner: client}, nil
}

func (c *Client) Prompt(ctx context.Context, prompt string) (*tool.ACPPromptResult, error) {
	result, err := c.inner.Prompt(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return toToolPromptResult(result), nil
}

func (c *Client) PromptStream(ctx context.Context, prompt string, onEvent func(tool.ACPPromptEvent)) (*tool.ACPPromptResult, error) {
	var adapted func(acpgo.PromptEvent)
	if onEvent != nil {
		adapted = func(event acpgo.PromptEvent) {
			onEvent(tool.ACPPromptEvent{
				Type:      tool.ACPPromptEventType(event.Type),
				Text:      event.Text,
				ToolID:    event.ToolID,
				ToolName:  event.ToolName,
				ToolTitle: event.ToolTitle,
				ToolArgs:  event.ToolArgs,
				Result:    event.Result,
				IsError:   event.IsError,
			})
		}
	}
	result, err := c.inner.PromptStream(ctx, prompt, adapted)
	if err != nil {
		return nil, err
	}
	return toToolPromptResult(result), nil
}

func (c *Client) Close() error {
	if c == nil || c.inner == nil {
		return nil
	}
	return c.inner.Close()
}

func toToolPromptResult(result *acpgo.PromptResult) *tool.ACPPromptResult {
	if result == nil {
		return nil
	}
	toolCalls := make([]tool.ACPToolCallSummary, len(result.ToolCalls))
	for i, call := range result.ToolCalls {
		toolCalls[i] = tool.ACPToolCallSummary{
			Name:   call.Name,
			Title:  call.Title,
			Status: call.Status,
		}
	}
	return &tool.ACPPromptResult{
		Text:       result.Text,
		StopReason: string(result.StopReason),
		ToolCalls:  toolCalls,
	}
}

func toExternalDecision(decision permission.Decision) acpgo.Decision {
	switch decision {
	case permission.Allow:
		return acpgo.Allow
	case permission.Deny:
		return acpgo.Deny
	default:
		return acpgo.Ask
	}
}

func (a permissionPolicyAdapter) Check(toolName string, input json.RawMessage) (acpgo.Decision, error) {
	if a.inner == nil {
		return acpgo.Ask, nil
	}
	decision, err := a.inner.Check(toolName, input)
	if err != nil {
		return acpgo.Ask, err
	}
	return toExternalDecision(decision), nil
}

func (a permissionPolicyAdapter) AllowedPathForTool(toolName, path string) bool {
	if a.inner == nil {
		return true
	}
	return a.inner.AllowedPathForTool(toolName, path)
}
