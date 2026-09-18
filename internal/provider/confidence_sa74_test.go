package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestMeanLogprob (sa-74): confidence aggregation helper.
func TestMeanLogprob(t *testing.T) {
	if got := meanLogprob(nil); got != nil {
		t.Fatalf("expected nil for empty input, got %v", *got)
	}
	got := meanLogprob([]float64{-0.5, -1.5})
	if got == nil || math.Abs(*got+1.0) > 1e-9 {
		t.Fatalf("expected -1.0, got %v", got)
	}
}

// TestOpenAIStreamConfidence (sa-74): when logprobs are requested, the
// request carries logprobs/top_logprobs and the Done event carries the
// turn's mean token logprob as Confidence.
func TestOpenAIStreamConfidence(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		chunks := []string{
			`{"id":"1","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"content":"hel"},"logprobs":{"content":[{"token":"hel","logprob":-0.5}]}}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"content":"lo"},"logprobs":{"content":[{"token":"lo","logprob":-1.5}]}}]}`,
			`{"id":"1","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 1024, server.URL+"/v1")
	p.SetLogprobsRequest(true)

	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	var done *StreamEvent
	timeout := time.After(5 * time.Second)
	for ev := range ch {
		if ev.Type == StreamEventDone {
			e := ev
			done = &e
		}
	}
	<-timeout
	if done == nil {
		t.Fatalf("no Done event received")
	}
	if done.Confidence == nil {
		t.Fatalf("expected Confidence on Done event")
	}
	if math.Abs(*done.Confidence+1.0) > 1e-9 {
		t.Fatalf("expected Confidence -1.0, got %v", *done.Confidence)
	}
	if gotBody["logprobs"] != true {
		t.Fatalf("expected request logprobs=true, got %#v", gotBody["logprobs"])
	}
	if tp, _ := gotBody["top_logprobs"].(float64); tp != 1 {
		t.Fatalf("expected request top_logprobs=1, got %#v", gotBody["top_logprobs"])
	}
}

// TestOpenAIStreamConfidenceDisabled (sa-74): without SetLogprobsRequest the
// request must not ask for logprobs and Confidence stays nil.
func TestOpenAIStreamConfidenceDisabled(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"logprobs\":{\"content\":[{\"token\":\"ok\",\"logprob\":-3.9}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer server.Close()

	p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 1024, server.URL+"/v1")

	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	var done *StreamEvent
	for ev := range ch {
		if ev.Type == StreamEventDone {
			e := ev
			done = &e
		}
	}
	if done == nil {
		t.Fatalf("no Done event received")
	}
	if done.Confidence != nil {
		t.Fatalf("expected nil Confidence when logprobs disabled, got %v", *done.Confidence)
	}
	if _, ok := gotBody["logprobs"]; ok {
		t.Fatalf("expected request to omit logprobs when disabled, got %#v", gotBody["logprobs"])
	}
}

// TestOpenAIChatConfidenceNonStreaming (sa-74): non-streaming Chat relays the
// mean content-token logprob as ChatResponse.Confidence.
func TestOpenAIChatConfidenceNonStreaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop","logprobs":{"content":[{"token":"ok","logprob":-0.25},{"token":"!","logprob":-0.75}]}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 1024, server.URL+"/v1")
	p.SetLogprobsRequest(true)
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Confidence == nil {
		t.Fatalf("expected Confidence on ChatResponse")
	}
	if math.Abs(*resp.Confidence+0.5) > 1e-9 {
		t.Fatalf("expected Confidence -0.5, got %v", *resp.Confidence)
	}
}
