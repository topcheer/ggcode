package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/subagent"
)

// ACPAgentRegistry is an interface for discovering and using ACP agents.
// Implemented by acp.ClientManager to avoid circular imports.
type ACPAgentRegistry interface {
	Available() []string
	AgentInfo(name string) (title, description string, ok bool)
	SetWorkingDir(dir string)
	Get(ctx context.Context, name string) (ACPAgentClient, error)
}

type ACPPromptEventType string

const (
	ACPPromptEventText       ACPPromptEventType = "text"
	ACPPromptEventToolCall   ACPPromptEventType = "tool_call"
	ACPPromptEventToolResult ACPPromptEventType = "tool_result"
)

type ACPPromptEvent struct {
	Type      ACPPromptEventType
	Text      string
	ToolID    string
	ToolName  string
	ToolTitle string
	ToolArgs  string
	Result    string
	IsError   bool
}

type pendingToolEventMeta struct {
	DisplayName string
	Detail      string
	RawArgs     string
}

type pendingToolEventStore struct {
	byID   map[string]pendingToolEventMeta
	byName map[string][]pendingToolEventMeta
}

func newPendingToolEventStore() *pendingToolEventStore {
	return &pendingToolEventStore{
		byID:   make(map[string]pendingToolEventMeta),
		byName: make(map[string][]pendingToolEventMeta),
	}
}

func (s *pendingToolEventStore) put(toolID, toolName string, meta pendingToolEventMeta) {
	if toolID != "" {
		s.byID[toolID] = meta
	}
	toolName = strings.TrimSpace(toolName)
	if toolName != "" {
		s.byName[toolName] = append(s.byName[toolName], meta)
	}
}

func (s *pendingToolEventStore) take(toolID, toolName string) (pendingToolEventMeta, bool) {
	if toolID != "" {
		meta, ok := s.byID[toolID]
		if !ok {
			return pendingToolEventMeta{}, false
		}
		delete(s.byID, toolID)
		toolName = strings.TrimSpace(toolName)
		if toolName != "" {
			queue := s.byName[toolName]
			for i, candidate := range queue {
				if candidate == meta {
					s.byName[toolName] = append(queue[:i], queue[i+1:]...)
					if len(s.byName[toolName]) == 0 {
						delete(s.byName, toolName)
					}
					break
				}
			}
		}
		return meta, true
	}
	toolName = strings.TrimSpace(toolName)
	queue := s.byName[toolName]
	if len(queue) != 1 {
		return pendingToolEventMeta{}, false
	}
	delete(s.byName, toolName)
	return queue[0], true
}

// ACPAgentClient is an interface for sending prompts to an ACP agent.
// Implemented by acp.Client.
type ACPAgentClient interface {
	Prompt(ctx context.Context, prompt string) (*ACPPromptResult, error)
	PromptStream(ctx context.Context, prompt string, onEvent func(ACPPromptEvent)) (*ACPPromptResult, error)
	Close() error
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
	Manager           ACPAgentRegistry
	SubAgentManager   *subagent.Manager
	SubAgentManagerFn func() *subagent.Manager
	WorkingDir        string
	WorkingDirFn      func() string
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

The agent executes autonomously in the current working directory. It may run asynchronously as a delegated sub-agent; use wait_agent or list_agents to follow an async run when the result says one was started.
Each agent uses its own API key and billing — no additional configuration needed.

Use this when:
- The user explicitly asks a specific agent to do something (e.g. "let copilot analyze this")
- You want a second opinion from a different AI model
- You want to leverage agent-specific capabilities

Avoid this for quick shell commands, direct file edits, or simple repository inspection that the current agent can do with local tools. Include all context the delegate needs in the prompt.`, strings.Join(descs, "\n"))
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
				"description": "The task description to send to the agent. Be specific and include all necessary context; the agent may not accept follow-up instructions reliably. The agent has access to the current working directory."
			},
			"description": {
				"type": "string",
				"description": "Optional short label for the live delegate panel"
			}
		},
		"required": ["agent", "prompt"]
	}`, string(enumBytes)))
}

func (t DelegateTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params struct {
		Agent       string `json:"agent"`
		Prompt      string `json:"prompt"`
		Description string `json:"description,omitempty"`
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

	workingDir := t.WorkingDir
	if t.WorkingDirFn != nil {
		if dir := t.WorkingDirFn(); dir != "" {
			workingDir = dir
		}
	}
	if workingDir != "" {
		t.Manager.SetWorkingDir(workingDir)
	}

	client, err := t.Manager.Get(ctx, params.Agent)
	if err != nil {
		return Result{Content: fmt.Sprintf("Agent %q is not available: %v", params.Agent, err), IsError: true}, nil
	}

	title, _, _ := t.Manager.AgentInfo(params.Agent)
	if title == "" {
		title = params.Agent
	}

	if mgr := t.subAgentManager(); mgr != nil {
		return t.executeAsync(ctx, mgr, client, title, params), nil
	}
	defer client.Close()

	result, err := client.Prompt(ctx, params.Prompt)
	if err != nil {
		return Result{Content: fmt.Sprintf("Agent %q error: %v", params.Agent, err), IsError: true}, nil
	}

	return Result{Content: formatDelegateOutput(title, result)}, nil
}

func (t DelegateTool) subAgentManager() *subagent.Manager {
	if t.SubAgentManagerFn != nil {
		return t.SubAgentManagerFn()
	}
	return t.SubAgentManager
}

func (t DelegateTool) executeAsync(
	ctx context.Context,
	mgr *subagent.Manager,
	client ACPAgentClient,
	title string,
	params struct {
		Agent       string `json:"agent"`
		Prompt      string `json:"prompt"`
		Description string `json:"description,omitempty"`
	},
) Result {
	task := strings.TrimSpace(params.Description)
	if task == "" {
		task = strings.TrimSpace(params.Prompt)
	}
	id := mgr.Spawn(title, task, params.Prompt, nil, ctx)
	mgr.Notify(id)

	safego.Go("delegate-tool", func() {
		defer client.Close()

		runCtx, cancel := context.WithCancel(mgr.RootContext())
		if !mgr.SetCancel(id, cancel) {
			cancel()
			return
		}
		mgr.UpdateActivity(id, "delegating", "", "")

		var latestResult *ACPPromptResult
		var streamedText strings.Builder
		pendingTools := newPendingToolEventStore()
		var textBuf strings.Builder
		flushText := func() {
			if textBuf.Len() == 0 {
				return
			}
			if sa, ok := mgr.Get(id); ok {
				sa.AppendEvent(subagent.AgentEvent{
					Type: subagent.AgentEventText,
					Text: textBuf.String(),
				})
			}
			textBuf.Reset()
		}

		res, err := client.PromptStream(runCtx, params.Prompt, func(ev ACPPromptEvent) {
			switch ev.Type {
			case ACPPromptEventText:
				streamedText.WriteString(ev.Text)
				textBuf.WriteString(ev.Text)
				mgr.NotifyStreamText(id, ev.Text)
				if textBuf.Len() >= 800 {
					flushText()
					mgr.Notify(id)
				}
			case ACPPromptEventToolCall:
				flushText()
				present := DescribeExternalToolCall(ev.ToolName, ev.ToolTitle, ev.ToolArgs)
				pendingTools.put(ev.ToolID, ev.ToolName, pendingToolEventMeta{
					DisplayName: present.DisplayName,
					Detail:      present.Detail,
					RawArgs:     ev.ToolArgs,
				})
				if sa, ok := mgr.Get(id); ok {
					sa.IncrementToolCalls()
					sa.AppendEvent(subagent.AgentEvent{
						Type:            subagent.AgentEventToolCall,
						ToolID:          ev.ToolID,
						ToolName:        ev.ToolName,
						ToolArgs:        ev.ToolArgs,
						ToolDisplayName: present.DisplayName,
						ToolDetail:      present.Detail,
					})
				}
				mgr.UpdateActivity(id, "tool", present.DisplayName, present.Detail)
				mgr.Notify(id)
				mgr.NotifyToolCall(id, ev.ToolID, ev.ToolName, present.DisplayName, ev.ToolArgs, present.Detail)
			case ACPPromptEventToolResult:
				flushText()
				meta, _ := pendingTools.take(ev.ToolID, ev.ToolName)
				if meta.DisplayName == "" && meta.Detail == "" && meta.RawArgs == "" {
					present := DescribeExternalToolCall(ev.ToolName, ev.ToolTitle, ev.ToolArgs)
					meta = pendingToolEventMeta{
						DisplayName: present.DisplayName,
						Detail:      present.Detail,
						RawArgs:     ev.ToolArgs,
					}
				}
				if sa, ok := mgr.Get(id); ok {
					sa.AppendEvent(subagent.AgentEvent{
						Type:            subagent.AgentEventToolResult,
						ToolID:          ev.ToolID,
						ToolName:        ev.ToolName,
						ToolArgs:        meta.RawArgs,
						ToolDisplayName: meta.DisplayName,
						ToolDetail:      meta.Detail,
						Result:          ev.Result,
						IsError:         ev.IsError,
					})
				}
				mgr.Notify(id)
				mgr.NotifyToolResult(id, ev.ToolID, ev.ToolName, meta.DisplayName, meta.Detail, ev.Result, ev.IsError)
			}
		})
		latestResult = res
		flushText()
		if err != nil {
			if sa, ok := mgr.Get(id); ok {
				sa.AppendEvent(subagent.AgentEvent{
					Type:    subagent.AgentEventError,
					Text:    err.Error(),
					IsError: true,
				})
			}
			mgr.Notify(id)
			mgr.Complete(id, "", err)
			return
		}
		if latestResult == nil {
			latestResult = &ACPPromptResult{Text: streamedText.String()}
		} else if strings.TrimSpace(latestResult.Text) == "" {
			latestResult.Text = streamedText.String()
		}
		mgr.Complete(id, formatDelegateOutput(title, latestResult), nil)
	})

	return Result{Content: fmt.Sprintf(
		"Started delegated agent %q as %s. Follow the live panel and use wait_agent with agent_id %q when you need the final result.",
		title,
		id,
		id,
	)}
}

func formatDelegateOutput(title string, result *ACPPromptResult) string {
	if result == nil {
		return fmt.Sprintf("[Response from %s]", title)
	}
	output := fmt.Sprintf("[Response from %s]\n\n%s", title, result.Text)
	if len(result.ToolCalls) > 0 {
		output += "\n\nTools used:"
		for _, tc := range result.ToolCalls {
			name := strings.TrimSpace(tc.Title)
			if name == "" {
				name = strings.TrimSpace(tc.Name)
			}
			if name == "" {
				name = "tool"
			}
			status := strings.TrimSpace(tc.Status)
			if status == "" {
				status = "completed"
			}
			output += fmt.Sprintf("\n  - %s (%s)", name, status)
		}
	}
	return output
}
