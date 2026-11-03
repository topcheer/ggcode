package agent

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestStreamStallNoStallWarnsNothing(t *testing.T) {
	src := make(chan provider.StreamEvent)
	out := streamWithStallDetection(src, 50*time.Millisecond)

	go func() {
		defer close(src)
		for i := 0; i < 3; i++ {
			src <- provider.StreamEvent{Type: provider.StreamEventText, Text: "hi"}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	warnings := 0
	for ev := range out {
		if ev.Type == provider.StreamEventSystem && containsStall(ev.Text) {
			warnings++
		}
	}
	if warnings != 0 {
		t.Fatalf("expected 0 stall warnings, got %d", warnings)
	}
}

func TestStreamStallEmitsWarningAfterTimeout(t *testing.T) {
	src := make(chan provider.StreamEvent)
	out := streamWithStallDetection(src, 30*time.Millisecond)

	go func() {

		src <- provider.StreamEvent{Type: provider.StreamEventText, Text: "first"}

		time.Sleep(80 * time.Millisecond)
		src <- provider.StreamEvent{Type: provider.StreamEventText, Text: "second"}
		close(src)
	}()

	var events []provider.StreamEvent
	for ev := range out {
		events = append(events, ev)
	}

	hasWarning := false
	for _, ev := range events {
		if ev.Type == provider.StreamEventSystem && containsStall(ev.Text) {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Fatalf("expected at least one stall warning, got events: %d", len(events))
	}
}

func TestStreamStallWarnsOnlyOncePerGap(t *testing.T) {
	src := make(chan provider.StreamEvent)
	out := streamWithStallDetection(src, 20*time.Millisecond)

	go func() {

		time.Sleep(100 * time.Millisecond)
		close(src)
	}()

	warnings := 0
	for ev := range out {
		if ev.Type == provider.StreamEventSystem && containsStall(ev.Text) {
			warnings++
		}
	}
	if warnings != 1 {
		t.Fatalf("expected exactly 1 stall warning, got %d", warnings)
	}
}

func TestStreamStallResetsAfterEvent(t *testing.T) {
	src := make(chan provider.StreamEvent)
	out := streamWithStallDetection(src, 20*time.Millisecond)

	go func() {

		time.Sleep(50 * time.Millisecond)

		src <- provider.StreamEvent{Type: provider.StreamEventText, Text: "recover"}

		time.Sleep(50 * time.Millisecond)
		close(src)
	}()

	warnings := 0
	for ev := range out {
		if ev.Type == provider.StreamEventSystem && containsStall(ev.Text) {
			warnings++
		}
	}
	if warnings != 2 {
		t.Fatalf("expected 2 stall warnings (one per gap), got %d", warnings)
	}
}

func TestStreamStallForwardsAllEvents(t *testing.T) {
	src := make(chan provider.StreamEvent)
	out := streamWithStallDetection(src, 5*time.Second)

	input := []provider.StreamEvent{
		{Type: provider.StreamEventText, Text: "a"},
		{Type: provider.StreamEventText, Text: "b"},
		{Type: provider.StreamEventDone},
	}
	go func() {
		defer close(src)
		for _, ev := range input {
			src <- ev
		}
	}()

	var forwarded []provider.StreamEvent
	for ev := range out {
		forwarded = append(forwarded, ev)
	}
	if len(forwarded) != len(input) {
		t.Fatalf("expected %d events forwarded, got %d", len(input), len(forwarded))
	}
	for i, ev := range forwarded {
		if ev.Type != input[i].Type || ev.Text != input[i].Text {
			t.Fatalf("event %d mismatch: got %+v, want %+v", i, ev, input[i])
		}
	}
}

func containsStall(s string) bool {
	return len(s) > 6 && s[:6] == "stream"
}

func TestStreamThinkingMultiBlockSignature(t *testing.T) {

	blocks := []provider.ContentBlock{
		{Type: "thinking", ThinkingSignature: "sig-A", ReasoningContent: "thought A"},
		{Type: "thinking", ThinkingSignature: "sig-B", ReasoningContent: "thought B"},
	}
	seen := map[string]string{}
	for _, b := range blocks {
		if b.ThinkingSignature != "" {
			seen[b.ThinkingSignature] = b.ReasoningContent
		}
	}
	if len(seen) != 2 || seen["sig-A"] != "thought A" || seen["sig-B"] != "thought B" {
		t.Fatalf("multi-block thinking pairs must stay distinct, got %v", seen)
	}
}
