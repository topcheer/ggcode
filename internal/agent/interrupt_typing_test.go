package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// Table-driven classification (InterruptBench arXiv:2604.00892 typing).
func TestClassifyInterruption(t *testing.T) {
	cases := []struct {
		name string
		text string
		want InterruptionKind
	}{
		// Retraction: whole-message stop phrases only.
		{"bare stop", "stop", InterruptionRetractStop},
		{"stop with punctuation", "Stop! ", InterruptionRetractStop},
		{"please cancel", "please cancel", InterruptionRetractStop},
		{"cancel the task", "cancel the task", InterruptionRetractStop},
		{"cn bare 取消", "取消", InterruptionRetractStop},
		{"cn 算了吧", "算了吧。", InterruptionRetractStop},
		{"cn 请停止", "请停止", InterruptionRetractStop},
		{"cn 别做了", "别做了", InterruptionRetractStop},
		// NOT retraction: carries task content (constraint, not withdrawal).
		{"stop adding tests", "stop adding tests", InterruptionAddition},
		{"cancel timer refactor", "cancel the timer refactor task", InterruptionAddition},
		{"dont commit", "don't commit to main", InterruptionAddition},
		// Revision: explicit redirection markers.
		{"instead", "use the retry library instead", InterruptionRevision},
		{"switch to", "switch to plan B", InterruptionRevision},
		{"cn 改成", "改成方案B", InterruptionRevision},
		{"cn 换成", "换成 redis 方案", InterruptionRevision},
		// Addition: default.
		{"plain guidance", "also add a README", InterruptionAddition},
		{"change of plans (legacy test text)", "change of plans", InterruptionAddition},
		{"empty", "", InterruptionAddition},
	}
	for _, c := range cases {
		if got := classifyInterruption(c.text); got != c.want {
			t.Errorf("%s: classifyInterruption(%q) = %s, want %s", c.name, c.text, got, c.want)
		}
	}
}

// The addition preamble must stay byte-identical to the pre-typing guidance
// text so existing steering behavior is unchanged.
func TestInterruptionPreambleAdditionUnchanged(t *testing.T) {
	const legacy = "New user guidance arrived while you were working. Treat it as higher-priority context, adjust your plan immediately if needed, and then continue."
	if got := interruptionPreamble(InterruptionAddition); got != legacy {
		t.Fatalf("addition preamble drifted:\n got: %q\nwant: %q", got, legacy)
	}
	if !strings.Contains(interruptionPreamble(InterruptionRevision), "REVISES the goal") {
		t.Fatalf("revision preamble missing goal-supersession directive")
	}
	if !strings.Contains(interruptionPreamble(InterruptionRetractStop), "RETRACTED") {
		t.Fatalf("retraction preamble missing withdrawal directive")
	}
}

// End-to-end retraction-stop: a whole-message stop phrase must (1) inject the
// retraction notice, (2) abort the run with context.Canceled (riding the r445
// snapshot path), (3) never reach another LLM call.
func TestRunStreamRetractStopAbortsRun(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("working...")}}},
			// Second response exists ONLY to detect the bug: if the loop kept
			// running after retraction it would consume this.
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("should not be reached")}}},
		},
	}
	registry := tool.NewRegistry()
	a := NewAgent(mp, registry, "", 3)
	calls := 0
	a.SetInterruptionHandler(func() []provider.ContentBlock {
		calls++
		if calls == 2 {
			return []provider.ContentBlock{{Type: "text", Text: "算了"}}
		}
		return nil
	})
	err := a.RunStream(context.Background(), "do something", func(event provider.StreamEvent) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retraction must abort with context.Canceled, got %v", err)
	}
	// Exactly one LLM call happened before the withdrawal landed; the second
	// staged response must never be consumed.
	if len(mp.capturedMsgs) != 1 {
		t.Fatalf("expected exactly 1 LLM call before retraction abort, got %d", len(mp.capturedMsgs))
	}
	// The injected notice must be in the conversation context so a future
	// resume sees the withdrawal directive.
	found := false
	for _, m := range a.contextManager.Messages() {
		if m.Role != "user" {
			continue
		}
		// The preamble and the user text ride as separate blocks of the
		// SAME injected message - match at message level.
		hasDirective, hasText := false, false
		for _, b := range m.Content {
			if b.Type != "text" {
				continue
			}
			if strings.Contains(b.Text, "RETRACTED") {
				hasDirective = true
			}
			if strings.Contains(b.Text, "算了") {
				hasText = true
			}
		}
		if hasDirective && hasText {
			found = true
		}
	}
	if !found {
		t.Fatalf("retraction notice not present in conversation context")
	}
}

// End-to-end revision: redirection marker keeps the run alive but injects the
// goal-supersession preamble alongside the user text.
func TestRunStreamRevisionInjectsSupersession(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("starting plan A")}}},
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("switched to plan B, done")}}},
		},
	}
	registry := tool.NewRegistry()
	a := NewAgent(mp, registry, "", 3)
	calls := 0
	a.SetInterruptionHandler(func() []provider.ContentBlock {
		calls++
		if calls == 1 {
			return []provider.ContentBlock{{Type: "text", Text: "改成方案B"}}
		}
		return nil
	})
	if err := a.RunStream(context.Background(), "implement plan A", func(event provider.StreamEvent) {}); err != nil {
		t.Fatalf("revision must not abort the run: %v", err)
	}
	last := mp.capturedMsgs[len(mp.capturedMsgs)-1]
	found := false
	for _, m := range last {
		if m.Role != "user" {
			continue
		}
		hasDirective, hasText := false, false
		for _, b := range m.Content {
			if b.Type != "text" {
				continue
			}
			if strings.Contains(b.Text, "REVISES the goal") {
				hasDirective = true
			}
			if strings.Contains(b.Text, "改成方案B") {
				hasText = true
			}
		}
		if hasDirective && hasText {
			found = true
		}
	}
	if !found {
		t.Fatalf("revision supersession preamble not present in captured payload")
	}
}
