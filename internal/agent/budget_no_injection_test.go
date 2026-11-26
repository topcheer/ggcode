package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// contextText renders every text block of the conversation for assertions.
func contextText(a *Agent) string {
	var sb strings.Builder
	for _, m := range a.contextManager.Messages() {
		for _, b := range m.Content {
			if b.Type == "text" {
				sb.WriteString(b.Text)
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}

// TestSessionBudgetCrossingsNeverInjectedIntoContext pins the removal of the
// "[token budget] ... finalize ... prepare to conclude" nudges: an explicit
// session budget still hard-stops the run, but no budget text ever reaches
// the conversation, so the model is never steered into winding down early.
func TestSessionBudgetCrossingsNeverInjectedIntoContext(t *testing.T) {
	var executed int32
	registry := tool.NewRegistry()
	if err := registry.Register(issue5013CountingTool{name: "counter", count: &executed}); err != nil {
		t.Fatal(err)
	}
	// issue5013BatchEvents reports 6 tokens per turn (5 input + 1 output).
	mp := &mockProvider{streamEvents: [][]provider.StreamEvent{
		issue5013BatchEvents("tc", 1),
		issue5013BatchEvents("tc2", 1),
		issue5013BatchEvents("tc3", 1),
		issue5013BatchEvents("tc4", 1),
	}}
	a := NewAgent(mp, registry, "", 0)
	a.SetSessionTokenBudget(10) // 60% after turn 1, 120% after turn 2 -> stop

	var sawSystemStop bool
	err := a.RunStream(context.Background(), "work", func(ev provider.StreamEvent) {
		if ev.Type == provider.StreamEventSystem && strings.Contains(ev.Text, "fully consumed") {
			sawSystemStop = true
		}
	})
	if !errors.Is(err, ErrSessionBudgetExhausted) {
		t.Fatalf("run error = %v, want ErrSessionBudgetExhausted (explicit budget still hard-stops)", err)
	}
	if !sawSystemStop {
		t.Fatal("the stop must still be announced to the user as a system event")
	}
	if got := contextText(a); strings.Contains(got, "[token budget]") {
		t.Fatalf("budget text must not be injected into the conversation, got:\n%s", got)
	}
}

// TestTokenWasteGuidanceGone: the former "[token-waste] ... context budget"
// injection is not part of the loop anymore (state recording stays).
func TestTokenWasteGuidanceGone(t *testing.T) {
	src, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "maybeWarnTokenWaste") {
		t.Error("token-waste guidance injection was reintroduced in the run loop")
	}
}
