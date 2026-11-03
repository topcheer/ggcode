package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func newAutopilotTestAgent() *Agent {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "ok"}},
			},
			Usage: provider.TokenUsage{InputTokens: 1, OutputTokens: 1},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))
	return a
}

func TestAutopilotGoalCollectionInjectsOnCruise(t *testing.T) {
	a := newAutopilotTestAgent()
	before := len(a.contextManager.Messages())
	a.maybeInjectAutopilotGoalCollection()
	if !a.autopilotGoalAsked {
		t.Fatal("cruise mode must set the asked latch on first injection")
	}
	if got := len(a.contextManager.Messages()) - before; got != 1 {
		t.Fatalf("first call must inject exactly 1 message, got %d", got)
	}
	a.maybeInjectAutopilotGoalCollection()
	if got := len(a.contextManager.Messages()) - before; got != 1 {
		t.Fatalf("asked latch must prevent a second injection, got %d total", got)
	}
}

func TestSuppressAutopilotInjectionSkipsGoalCollection(t *testing.T) {
	a := newAutopilotTestAgent()
	a.SuppressAutopilotInjection()
	before := len(a.contextManager.Messages())

	a.maybeInjectAutopilotGoalCollection()
	a.maybeInjectAutopilotGoalCollection()

	if a.autopilotGoalAsked {
		t.Error("suppressed agent must not set the goal-asked latch")
	}
	if got := len(a.contextManager.Messages()) - before; got != 0 {
		t.Errorf("suppressed agent must inject 0 messages, got %d", got)
	}
}

func TestSuppressAutopilotInjectionIdempotentAndModeIntact(t *testing.T) {
	a := newAutopilotTestAgent()
	if !a.IsAutopilotMode() {
		t.Fatal("fixture must start on the cruise surface")
	}
	a.SuppressAutopilotInjection()
	a.SuppressAutopilotInjection()
	if !a.IsAutopilotMode() {
		t.Error("suppression must not downgrade the permission mode")
	}
	if a == nil {
		t.Error("nil-guard branch documentation")
	}
	var nilAgent *Agent
	nilAgent.SuppressAutopilotInjection()
}
