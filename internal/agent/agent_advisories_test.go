package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// newAdvisoriesTestAgent builds a minimal agent for exercising
// injectPreSendAdvisories without a live provider round-trip.
func newAdvisoriesTestAgent(t *testing.T, maxIter int) *Agent {
	t.Helper()
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "ok"}},
			},
			Usage: provider.TokenUsage{InputTokens: 10, OutputTokens: 2},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", maxIter)
	if a == nil || a.contextManager == nil {
		t.Fatal("NewAgent returned incomplete agent")
	}
	return a
}

// countCheckpointMessages counts progress-checkpoint messages in a snapshot.
func countCheckpointMessages(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "Progress checkpoint: iteration") {
				n++
			}
		}
	}
	return n
}

// TestInjectPreSendAdvisoriesProgressCheckpoint pins the mid-point progress
// checkpoint semantics after the r125 extraction of the advisory fleet out of
// RunStreamWithContent: fires once at 60% of maxIter (only when maxIter >= 20),
// injects the assessment prompt, and never fires twice.
func TestInjectPreSendAdvisoriesProgressCheckpoint(t *testing.T) {
	a := newAdvisoriesTestAgent(t, 20)
	injected := false

	// Below the 60% threshold (i+1=11 < 20*3/5=12): no checkpoint.
	msgs := a.injectPreSendAdvisories(10, &injected)
	if injected {
		t.Fatal("checkpoint fired below 60% threshold")
	}
	if got := countCheckpointMessages(msgs); got != 0 {
		t.Fatalf("expected 0 checkpoint messages below threshold, got %d", got)
	}

	// At the threshold (i+1=12): fires exactly once and lands in the snapshot.
	msgs = a.injectPreSendAdvisories(11, &injected)
	if !injected {
		t.Fatal("checkpoint did not fire at 60% threshold")
	}
	if got := countCheckpointMessages(msgs); got != 1 {
		t.Fatalf("expected exactly 1 checkpoint message, got %d", got)
	}
	if len(msgs) == 0 || len(msgs) != len(a.contextManager.Messages()) {
		t.Fatalf("returned snapshot %d does not match context manager %d",
			len(msgs), len(a.contextManager.Messages()))
	}

	// One-shot: with the flag set, later iterations never re-fire.
	msgs = a.injectPreSendAdvisories(12, &injected)
	if got := countCheckpointMessages(msgs); got != 1 {
		t.Fatalf("checkpoint re-fired; expected 1 message, got %d", got)
	}
}

// TestInjectPreSendAdvisoriesShortRunSkipsCheckpoint pins the maxIter >= 20
// gate: short runs never get the mid-point checkpoint.
func TestInjectPreSendAdvisoriesShortRunSkipsCheckpoint(t *testing.T) {
	a := newAdvisoriesTestAgent(t, 5)
	injected := false
	// maxIter=5 < 20: even past the relative threshold nothing fires.
	msgs := a.injectPreSendAdvisories(4, &injected)
	if injected {
		t.Fatal("checkpoint fired for short run (maxIter < 20)")
	}
	if got := countCheckpointMessages(msgs); got != 0 {
		t.Fatalf("expected 0 checkpoint messages for short run, got %d", got)
	}
}
