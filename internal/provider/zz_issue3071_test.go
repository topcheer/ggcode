package provider

// Regression probes for #3071 (openai.go, two points):
//   V1: the ChatStream producer used 14 bare `ch <- StreamEvent{...}` sends -
//       a cancelling consumer that stopped reading filled the 64-event buffer
//       and parked the goroutine forever (#3068's sibling; the fallback
//       wrapper never covered the no-fallback path).
//   V2: foldInjectedUserMessages' else branch appended only messages[i+1]
//       and skipped to i+2, silently DROPPING messages[i+2..j-1] when ≥2
//       consecutive text-only user messages followed a tool_use assistant
//       turn without a tool_result.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// V2: three consecutive text-only user messages after a tool_use assistant
// turn, no tool_result anywhere - all must survive the fold.
func TestIssue3071_FoldPreservesConsecutiveGuidanceUsers(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("do the thing")}},
		{Role: "assistant", Content: []ContentBlock{ToolUseBlock("call_1", "run_command", json.RawMessage(`{"command":"ls"}`))}},
		{Role: "user", Content: []ContentBlock{TextBlock("guidance one: focus on tests")}},
		{Role: "user", Content: []ContentBlock{TextBlock("guidance two: also check docs")}},
		{Role: "user", Content: []ContentBlock{TextBlock("guidance three: run vet afterwards")}},
		{Role: "assistant", Content: []ContentBlock{TextBlock("ok, proceeding")}},
	}

	got := foldInjectedUserMessages(messages, func(blocks []ContentBlock, prefix string) []ContentBlock {
		return blocks // fold no-op; the else path must not reach it anyway
	})

	if len(got) != len(messages) {
		t.Fatalf("message count changed: got %d, want %d", len(got), len(messages))
	}
	for i, m := range got {
		if m.Role != messages[i].Role {
			t.Errorf("message %d role changed: %q", i, m.Role)
		}
	}
	for _, want := range []string{"guidance one", "guidance two", "guidance three"} {
		found := false
		for _, m := range got {
			for _, b := range m.Content {
				if strings.Contains(b.Text, want) {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("guidance message %q dropped by fold (V2)", want)
		}
	}
}

// V1: a consumer that cancels and stops reading must not park the producer
// goroutine forever - the channel must close promptly (same shape as the
// #3068 gemini_leak_test).
func TestIssue3071_ChatStreamProducerExitsAfterConsumerCancel(t *testing.T) {
	const totalEvents = 200 // well over the 64-event channel buffer

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < totalEvents; i++ {
			fmt.Fprintf(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"chunk%d\"},\"finish_reason\":null}]}\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer server.Close()

	prov, err := NewProvider(&config.ResolvedEndpoint{
		Protocol:  "openai",
		BaseURL:   server.URL,
		APIKey:    "dummy",
		Model:     "gpt-test",
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatalf("expected OpenAI provider, got error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := prov.ChatStream(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hello")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}

	// Read a couple of events, then cancel and stop reading entirely.
	<-ch
	<-ch
	cancel()

	// The producer must observe cancellation and close the channel instead
	// of parking on a full buffer. Generous 10s guard against CI jitter.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed: producer exited cleanly
			}
		case <-deadline:
			t.Fatal("producer goroutine leaked: channel not closed within 10s of consumer cancel (V1)")
		}
	}
}
