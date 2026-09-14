package session

// #2325: the checkpoint context path was uncapped - a session with a
// valid last checkpoint followed by 7.5k messages loaded a 2.82M-token
// context. capContextTail windows any converged context to
// MaxContextMessages (orphan-pair guard, shift-not-collapse user anchor).

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestCapContextTailWindowsOversizedSlice(t *testing.T) {
	var msgs []provider.Message
	for i := 0; i < MaxContextMessages+500; i++ {
		role := "assistant"
		if i%10 == 0 {
			role = "user"
		}
		msgs = append(msgs, provider.Message{Role: role,
			Content: []provider.ContentBlock{{Type: "text", Text: "turn"}}})
	}
	msgs = append(msgs, provider.Message{Role: "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "final question"}}})
	got := capContextTail(msgs)
	if len(got) > MaxContextMessages+1 {
		t.Fatalf("capped slice must be window+note, got %d", len(got))
	}
	if !strings.Contains(got[0].Content[0].Text, "truncated") {
		t.Fatal("truncation note must lead the capped context")
	}
	last := got[len(got)-1]
	if last.Role != "user" || last.Content[0].Text != "final question" {
		t.Fatalf("window must retain the newest user turn, last=%v", last.Role)
	}
}

func TestCapContextTailNoOpWhenSmall(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
	}
	if got := capContextTail(msgs); len(got) != 2 {
		t.Fatalf("small slice untouched, got %d", len(got))
	}
}
