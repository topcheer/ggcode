package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// autopilotPingPongProvider: the main model keeps ending its turn with text
// only ("done"), and the strategist keeps telling it to continue for the
// first N consultations before declaring the goal achieved.
type autopilotPingPongProvider struct {
	mockProvider
	turns           int32
	strategistCalls int32
	continueRounds  int32
}

func (p *autopilotPingPongProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	atomic.AddInt32(&p.turns, 1)
	events := []provider.StreamEvent{
		{Type: provider.StreamEventText, Text: "I think the work is done."},
		{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 5, OutputTokens: 1}},
	}
	ch := make(chan provider.StreamEvent, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (p *autopilotPingPongProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	isStrategist := false
	for _, m := range messages {
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "autonomous task strategist") {
				isStrategist = true
			}
		}
	}
	text := "ok"
	if isStrategist {
		n := atomic.AddInt32(&p.strategistCalls, 1)
		if n <= p.continueRounds {
			text = "Continue: the tests for step " + string(rune('0'+n)) + " have not been run yet. Run them now."
		} else {
			text = strategistCompleteMarker + "\nAll steps are verified."
		}
	}
	return &provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: text}}},
		Usage:   provider.TokenUsage{InputTokens: 5, OutputTokens: 1},
	}, nil
}

// TestAutopilotNoProgressNeverForceTerminates: in cruise mode with a goal,
// many consecutive text-only (no-progress) rounds must NOT terminate the run.
// The former gate stopped after 3 such rounds (consulting the strategist only
// twice); now the run continues until the strategist itself declares the goal
// achieved - the AI decides when to stop.
func TestAutopilotNoProgressNeverForceTerminates(t *testing.T) {
	const rounds = 6
	pp := &autopilotPingPongProvider{continueRounds: rounds}
	a := NewAgent(pp, tool.NewRegistry(), "", 0)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
	a.SetAutopilotGoal("ship the feature with passing tests")

	var systemEvents []string
	err := a.RunStream(context.Background(), "go", func(ev provider.StreamEvent) {
		if ev.Type == provider.StreamEventSystem {
			systemEvents = append(systemEvents, ev.Text)
		}
	})
	if err != nil {
		t.Fatalf("run error = %v, want a clean strategist-declared completion", err)
	}
	joined := strings.Join(systemEvents, "\n")
	if strings.Contains(joined, "terminating to avoid deadlock") {
		t.Fatalf("the no-progress force-terminate is back:\n%s", joined)
	}
	if got := atomic.LoadInt32(&pp.strategistCalls); got != rounds+1 {
		t.Fatalf("strategist consultations = %d, want %d (one per no-progress round + the final goal-achieved one)", got, rounds+1)
	}
	if got := atomic.LoadInt32(&pp.turns); got != rounds+1 {
		t.Fatalf("model turns = %d, want %d (every round must be allowed to continue)", got, rounds+1)
	}
	if !strings.Contains(joined, "goal achieved - autopilot complete") {
		t.Fatalf("run must end on the strategist's own completion decision, events:\n%s", joined)
	}
	if a.hasAutopilotGoal() {
		t.Fatal("goal must be cleared once the strategist declares it achieved")
	}
}
