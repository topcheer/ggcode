package context

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestIssue5807IdenticalHeadRewriteIsNoop pins the cache-awareness substrate:
// a byte-identical UpdateFirstSystemMessage call must not bump version or
// nonTailMutSeq, while a real head change must bump both exactly once (a
// head rewrite invalidates the whole prefix after it).
func TestIssue5807IdenticalHeadRewriteIsNoop(t *testing.T) {
	m := NewManager(100000)
	m.UpdateFirstSystemMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "stable head", Cache: true}},
	})
	v1, s1 := m.version, m.nonTailMutSeq

	m.UpdateFirstSystemMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "stable head"}},
	})
	if m.version != v1 || m.nonTailMutSeq != s1 {
		t.Fatalf("byte-identical head rewrite must not bump version/seq: %d/%d -> %d/%d", v1, s1, m.version, m.nonTailMutSeq)
	}

	m.UpdateFirstSystemMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "changed head", Cache: true}},
	})
	if m.version != v1+1 || m.nonTailMutSeq != s1+1 {
		t.Fatalf("changed head must bump version+nonTailMutSeq exactly once: %d/%d -> %d/%d", v1, s1, m.version, m.nonTailMutSeq)
	}
}

// TestIssue5807VolatileTailRotationKeepsSingleBreakpoint pins the breakpoint
// rotation: exactly one live volatile cache flag (the newest turn's), older
// tails keep their bytes but lose the flag, and whitespace-only volatile
// text degrades to a plain Add.
func TestIssue5807VolatileTailRotationKeepsSingleBreakpoint(t *testing.T) {
	m := NewManager(100000)
	m.UpdateFirstSystemMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "head", Cache: true}},
	})

	turn := func(text, volatile string) {
		m.AppendUserTurnWithVolatileTail(provider.Message{Role: "user"},
			[]provider.ContentBlock{{Type: "text", Text: text}}, volatile)
	}
	turn("first", "ctx for first")
	turn("second", "ctx for second")

	msgs := m.Messages()
	if len(msgs) != 3 {
		t.Fatalf("want head + 2 turns, got %d messages", len(msgs))
	}
	flagged := 0
	for _, msg := range msgs {
		if msg.Role != "user" {
			continue
		}
		for i, b := range msg.Content {
			if b.Type == "text" && i > 0 && b.Cache {
				flagged++
			}
		}
	}
	if flagged != 1 {
		t.Fatalf("exactly one live volatile breakpoint expected, got %d", flagged)
	}
	last := msgs[2]
	if len(last.Content) != 2 || last.Content[1].Text != "ctx for second" || !last.Content[1].Cache {
		t.Fatalf("newest turn must carry its volatile tail with the live flag: %+v", last.Content)
	}
	if msgs[1].Content[1].Cache || msgs[1].Content[1].Text != "ctx for first" {
		t.Fatalf("older volatile tail must keep bytes but drop the flag: %+v", msgs[1].Content)
	}

	turn("third", "   ")
	msgs = m.Messages()
	if last := msgs[len(msgs)-1]; len(last.Content) != 1 {
		t.Fatalf("whitespace-only volatile text must not append a trailing block: %+v", last.Content)
	}
}

// TestIssue5807RemoveLastUserMessageDropsVolatileTail pins tail-safety: the
// retry-path removal takes the turn and its volatile tail together, leaving
// earlier turns frozen.
func TestIssue5807RemoveLastUserMessageDropsVolatileTail(t *testing.T) {
	m := NewManager(100000)
	m.UpdateFirstSystemMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "head", Cache: true}},
	})
	for _, text := range []string{"first", "second"} {
		m.AppendUserTurnWithVolatileTail(provider.Message{Role: "user"},
			[]provider.ContentBlock{{Type: "text", Text: text}}, "ctx for "+text)
	}

	if !m.RemoveLastUserMessage() {
		t.Fatal("expected the last user turn to be removed")
	}
	msgs := m.Messages()
	if len(msgs) != 2 {
		t.Fatalf("want head + 1 remaining turn, got %d messages", len(msgs))
	}
	if msgs[1].Content[1].Text != "ctx for first" {
		t.Fatalf("turn-1 volatile tail must be intact after tail removal: %+v", msgs[1].Content)
	}
}
