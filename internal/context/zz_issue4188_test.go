package context

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4188: a vision-fallback retry re-projects the same user turn (image ->
// text-only); the caller removes the original before retrying so the turn
// is not doubled. RemoveLastUserMessage must drop the trailing REAL user
// message, skip tool_result carriers, and be a no-op on empty history.
func TestRemoveLastUserMessage(t *testing.T) {
	t.Run("removes trailing real user message", func(t *testing.T) {
		m := NewManager(128000)
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2-with-image"}}})
		if !m.RemoveLastUserMessage() {
			t.Fatal("expected removal of trailing user message")
		}
		// The retry re-adds the projection; the turn must appear exactly once.
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2-textonly"}}})
		var userCount int
		for _, msg := range m.Messages() {
			if msg.Role == "user" {
				userCount++
			}
		}
		if userCount != 2 {
			t.Fatalf("user messages = %d, want 2 (q1 + projection)", userCount)
		}
	})

	t.Run("skips tool_result carriers", func(t *testing.T) {
		m := NewManager(128000)
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})
		// Carrier inserted by ReconcileToolCalls sits at the tail.
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1"},
		}})
		if !m.RemoveLastUserMessage() {
			t.Fatal("carrier must be skipped, real user q1 still removable")
		}
		var userCount int
		for _, msg := range m.Messages() {
			if msg.Role == "user" {
				userCount++
			}
		}
		if userCount != 1 {
			t.Fatalf("remaining user messages = %d, want 1 (the carrier)", userCount)
		}
	})

	t.Run("no real user message is a no-op false", func(t *testing.T) {
		m := NewManager(128000)
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})
		if m.RemoveLastUserMessage() {
			t.Fatal("no user message present, must return false")
		}
	})
}
