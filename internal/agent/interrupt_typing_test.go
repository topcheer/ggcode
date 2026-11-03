package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestClassifyInterruption(t *testing.T) {
	cases := []struct {
		name string
		text string
		want InterruptionKind
	}{

		{"bare stop", "stop", InterruptionRetractStop},
		{"stop with punctuation", "Stop! ", InterruptionRetractStop},
		{"please cancel", "please cancel", InterruptionRetractStop},
		{"cancel the task", "cancel the task", InterruptionRetractStop},
		{"cn bare 取消", "取消", InterruptionRetractStop},
		{"cn 算了吧", "算了吧。", InterruptionRetractStop},
		{"cn 请停止", "请停止", InterruptionRetractStop},
		{"cn 别做了", "别做了", InterruptionRetractStop},

		{"stop adding tests", "stop adding tests", InterruptionAddition},
		{"cancel timer refactor", "cancel the timer refactor task", InterruptionAddition},
		{"dont commit", "don't commit to main", InterruptionAddition},

		{"instead of that", "instead of that, parse the CSV directly", InterruptionRevision},
		{"switch to plan", "switch to plan B", InterruptionRevision},
		{"cn 改成方案", "改成方案B", InterruptionRevision},
		{"cn 换成方案", "换成方案B再跑", InterruptionRevision},
		{"cn 换个思路", "换个思路，直接用批处理", InterruptionRevision},

		{"bare instead refinement", "use buffered I/O instead of the streaming reader, it's simpler", InterruptionAddition},
		{"bare instead library", "use the retry library instead", InterruptionAddition},
		{"cn 改为 rename", "把变量名改为 camelCase 再继续", InterruptionAddition},
		{"cn 改成 reorder", "改成先跑测试再构建", InterruptionAddition},
		{"rather than comparison", "rather than a mutex, use an atomic counter here", InterruptionAddition},

		{"plain guidance", "also add a README", InterruptionAddition},
		{"change of plans (legacy test text)", "change of plans", InterruptionAddition},
		{"empty", "", InterruptionAddition},

		{"long multi-sentence brief => insert",
			"Please also review the billing module for double-charge bugs. The finance team reported duplicated invoices on renewals. Reproduce with the fixture in testdata/billing, then propose a minimal fix with tests.",
			InterruptionInsert},
		{"short follow-up stays addition", "also handle the retry backoff in the same client", InterruptionAddition},
		{"revision marker beats insert signal",
			"Please switch to plan B entirely and drop the old approach. It must also cover the retry path with tests and the migration script.",
			InterruptionRevision},
	}
	for _, c := range cases {
		if got := classifyInterruption(c.text); got != c.want {
			t.Errorf("%s: classifyInterruption(%q) = %s, want %s", c.name, c.text, got, c.want)
		}
	}
}

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

func TestRunStreamRetractStopAbortsRun(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("working...")}}},

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

	if len(mp.capturedMsgs) != 1 {
		t.Fatalf("expected exactly 1 LLM call before retraction abort, got %d", len(mp.capturedMsgs))
	}

	found := false
	for _, m := range a.contextManager.Messages() {
		if m.Role != "user" {
			continue
		}

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
