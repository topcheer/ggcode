package subagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

type usageAwareTestRunner struct {
	onUsage func(provider.TokenUsage)
}

func (r *usageAwareTestRunner) SetUsageHandler(fn func(provider.TokenUsage)) {
	r.onUsage = fn
}

func (r *usageAwareTestRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	if r.onUsage != nil {
		r.onUsage(provider.TokenUsage{InputTokens: 17, OutputTokens: 6})
	}
	onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "done"})
	onEvent(provider.StreamEvent{Type: provider.StreamEventDone})
	return nil
}

func TestRun_ForwardsUsageHandlerToSubAgent(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: time.Second})
	id := mgr.Spawn("worker", "task", "task", nil, context.Background())
	runner := &usageAwareTestRunner{}

	var got provider.TokenUsage
	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return runner
		},
		OnUsage: func(usage provider.TokenUsage) {
			got = usage
		},
	})

	if got != (provider.TokenUsage{InputTokens: 17, OutputTokens: 6}) {
		t.Fatalf("expected forwarded usage, got %+v", got)
	}
	if runner.onUsage == nil {
		t.Fatal("expected sub-agent usage handler to be installed")
	}
}

type toolOrderTestRunner struct{}

func (r *toolOrderTestRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	onEvent(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{
			ID:        "tool-1",
			Name:      "read_file",
			Arguments: []byte(`{"path":"/tmp/a.txt"}`),
		},
	})
	onEvent(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{
			ID:        "tool-2",
			Name:      "bash",
			Arguments: []byte(`{"command":"pwd"}`),
		},
	})
	onEvent(provider.StreamEvent{
		Type:    provider.StreamEventToolResult,
		Tool:    provider.ToolCallDelta{ID: "tool-2"},
		Result:  "/repo\n",
		IsError: false,
	})
	onEvent(provider.StreamEvent{
		Type:    provider.StreamEventToolResult,
		Tool:    provider.ToolCallDelta{ID: "tool-1"},
		Result:  "hello\n",
		IsError: false,
	})
	onEvent(provider.StreamEvent{Type: provider.StreamEventDone})
	return nil
}

func TestRun_MatchesToolResultsByID(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: time.Second})
	id := mgr.Spawn("worker", "task", "task", nil, context.Background())

	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return &toolOrderTestRunner{}
		},
	})

	sa, ok := mgr.Get(id)
	if !ok {
		t.Fatal("expected sub-agent to exist")
	}
	events := sa.Events()
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	if events[2].ToolID != "tool-2" || events[2].ToolName != "bash" || !strings.Contains(events[2].ToolArgs, `"command":"pwd"`) {
		t.Fatalf("unexpected second result event: %+v", events[2])
	}
	if events[3].ToolID != "tool-1" || events[3].ToolName != "read_file" || !strings.Contains(events[3].ToolArgs, `"path":"/tmp/a.txt"`) {
		t.Fatalf("unexpected third result event: %+v", events[3])
	}
}
