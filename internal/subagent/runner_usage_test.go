package subagent

import (
	"context"
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
