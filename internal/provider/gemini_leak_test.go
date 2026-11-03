package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// TestGeminiChatStream_ProducerExitsAfterConsumerCancel is the #3068
// regression test: the streamRead producer goroutine used 11 bare
// `ch <- StreamEvent{...}` sends. A consumer that cancels and stops reading
// filled the 64-event buffer and parked the producer forever (defer
// close(ch) never ran). With the sendEvent guard, cancellation unblocks the
// next send and the channel closes promptly.
func TestGeminiChatStream_ProducerExitsAfterConsumerCancel(t *testing.T) {
	const totalEvents = 200 // well over the 64-event channel buffer

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < totalEvents; i++ {
			fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"chunk%d\"}]}},{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":1}}\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer server.Close()

	prov, err := NewProvider(&config.ResolvedEndpoint{
		Protocol:  "gemini",
		BaseURL:   server.URL,
		APIKey:    "dummy",
		Model:     "gemini-2.5-flash",
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatalf("expected Gemini provider, got error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := prov.ChatStream(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hello")}},
	}, nil)
	if err != nil {
		cancel()
		t.Fatalf("ChatStream error: %v", err)
	}

	for i := 0; i < 2; i++ {
		<-ch
	}
	cancel()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}

		case <-deadline:
			t.Fatal("producer goroutine leaked: channel not closed within 3s after consumer cancel")
		}
	}
}
