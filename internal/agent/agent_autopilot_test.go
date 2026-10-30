package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// newAutopilotTestAgent builds a minimal agent parked on the cruise surface
// (bypass/autopilot) - the precondition for goal-collection injection.
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

// TestAutopilotGoalCollectionInjectsOnCruise verifies the baseline the
// suppress flag turns off: on the cruise surface the first
// maybeInjectAutopilotGoalCollection injects the meta-instruction exactly
// once (asked latch) and never twice.
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

// TestSuppressAutopilotInjectionSkipsGoalCollection verifies the linear
// frontend's opt-out: with the flag set, the cruise-mode goal-collection
// meta-instruction is never injected and the asked latch stays false, so a
// later re-check still sees a clean state.
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

// TestSuppressAutopilotInjectionIdempotentAndModeIntact verifies the flag is
// idempotent and - by design - does NOT change the permission surface: the
// agent still reports cruise mode after opting out.
func TestSuppressAutopilotInjectionIdempotentAndModeIntact(t *testing.T) {
	a := newAutopilotTestAgent()
	if !a.IsAutopilotMode() {
		t.Fatal("fixture must start on the cruise surface")
	}
	a.SuppressAutopilotInjection()
	a.SuppressAutopilotInjection() // idempotent, no panic
	if !a.IsAutopilotMode() {
		t.Error("suppression must not downgrade the permission mode")
	}
	if a == nil {
		t.Error("nil-guard branch documentation")
	}
	var nilAgent *Agent
	nilAgent.SuppressAutopilotInjection() // nil-safe, no panic
}
