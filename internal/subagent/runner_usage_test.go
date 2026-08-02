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

func TestFormatProgressSummary(t *testing.T) {
	tests := []struct {
		name     string
		snap     Snapshot
		contains []string
	}{
		{
			name:     "empty snapshot",
			snap:     Snapshot{Status: StatusRunning},
			contains: []string{"[running]"},
		},
		{
			name: "with tool info",
			snap: Snapshot{
				Status:        StatusRunning,
				ToolCallCount: 5,
				CurrentTool:   "read_file",
				CurrentPhase:  "tool",
			},
			contains: []string{"[running]", "5 tools", "read_file", "tool"},
		},
		{
			name: "with progress summary",
			snap: Snapshot{
				Status:          StatusRunning,
				ToolCallCount:   3,
				ProgressSummary: "Searching for patterns...",
			},
			contains: []string{"[running]", "3 tools", "Searching for patterns..."},
		},
		{
			name: "truncates long progress summary",
			snap: Snapshot{
				Status:          StatusRunning,
				ProgressSummary: strings.Repeat("x", 100),
			},
			contains: []string{"..."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatProgressSummary(tt.snap)
			for _, s := range tt.contains {
				if !strings.Contains(got, s) {
					t.Errorf("formatProgressSummary() = %q, expected to contain %q", got, s)
				}
			}
		})
	}
}

func TestWaitForSnapshotWithProgress_CallsProgressFn(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: 2 * time.Second})
	id := mgr.Spawn("worker", "task", "display task", nil, context.Background())

	// Spawn starts in StatusRunning. WaitForSnapshotWithProgress will poll
	// and call progressFn before timing out.
	var callCount int
	var lastSummary string
	progressFn := func(summary string) {
		callCount++
		lastSummary = summary
	}

	// Use a short wait so the test completes quickly.
	_, err := WaitForSnapshotWithProgress(context.Background(), mgr, id, 300*time.Millisecond, progressFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount == 0 {
		t.Error("expected progressFn to be called at least once")
	}
	if !strings.Contains(lastSummary, "[") {
		t.Errorf("expected summary to contain status marker, got %q", lastSummary)
	}
}

func TestWaitForSnapshotWithProgress_NilFn(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: 2 * time.Second})
	id := mgr.Spawn("worker", "task", "task", nil, context.Background())

	// Should not panic with nil progressFn.
	_, err := WaitForSnapshotWithProgress(context.Background(), mgr, id, 200*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
