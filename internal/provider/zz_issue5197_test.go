package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Issue #5197: when request_timeout > 0, the ChatStream retry loop must rebuild
// the per-attempt timeout context after cancelAttempt(). The broken condition
// (`attempt == 0 || requestTimeout <= 0`) skipped the rebuild on retries, so
// attempt 1+ ran on an already-canceled ctx and died with "context canceled"
// instead of a real request. The regression pin: first hit returns a retryable
// 503, second hit succeeds; with a positive requestTimeout the stream must
// still reach the server twice and complete without a context-canceled error.

func TestIssue5197OpenAIStreamRetryRebuildsTimeoutContext(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":{"message":"overloaded","type":"server_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewOpenAIProviderWithBaseURL("sk-test", "test-model", 1024, srv.URL+"/v1")
	p.setCallPolicy(callPolicy{maxRetries: 2, requestTimeout: 10 * time.Second})

	ch, err := p.ChatStream(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	var done, gotErr bool
	for ev := range ch {
		switch ev.Type {
		case StreamEventText:
			text.WriteString(ev.Text)
		case StreamEventDone:
			done = true
		case StreamEventError:
			gotErr = true
			t.Errorf("unexpected error event: %v", ev.Error)
		}
	}
	if gotErr {
		t.Fatal("stream errored (see error event above)")
	}
	if !done {
		t.Fatal("no Done event")
	}
	if text.String() != "ok" {
		t.Errorf("text = %q, want ok", text.String())
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server hits = %d, want 2 (retry must run on a fresh timeout ctx, not a canceled one)", got)
	}
}

func TestIssue5197AnthropicStreamRetryRebuildsTimeoutContext(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
		io.WriteString(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n")
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	p := NewAnthropicProviderWithBaseURL("test-key", "claude-sonnet-4-5", 1024, srv.URL)
	p.setCallPolicy(callPolicy{maxRetries: 2, requestTimeout: 10 * time.Second})

	ch, err := p.ChatStream(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	var done, gotErr bool
	for ev := range ch {
		switch ev.Type {
		case StreamEventText:
			text.WriteString(ev.Text)
		case StreamEventDone:
			done = true
		case StreamEventError:
			gotErr = true
			t.Errorf("unexpected error event: %v", ev.Error)
		}
	}
	if gotErr {
		t.Fatal("stream errored (see error event above)")
	}
	if !done {
		t.Fatal("no Done event")
	}
	if text.String() != "ok" {
		t.Errorf("text = %q, want ok", text.String())
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server hits = %d, want 2 (retry must run on a fresh timeout ctx, not a canceled one)", got)
	}
}

func TestIssue5197OpenAIResponsesStreamRetryRebuildsTimeoutContext(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":{"message":"overloaded","type":"server_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("sk-test", "gpt-5-codex", 1024, srv.URL+"/v1")
	p.setCallPolicy(callPolicy{maxRetries: 2, requestTimeout: 10 * time.Second})

	ch, err := p.ChatStream(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	var done, gotErr bool
	for ev := range ch {
		switch ev.Type {
		case StreamEventText:
			text.WriteString(ev.Text)
		case StreamEventDone:
			done = true
		case StreamEventError:
			gotErr = true
			t.Errorf("unexpected error event: %v", ev.Error)
		}
	}
	if gotErr {
		t.Fatal("stream errored (see error event above)")
	}
	if !done {
		t.Fatal("no Done event")
	}
	if text.String() != "ok" {
		t.Errorf("text = %q, want ok", text.String())
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server hits = %d, want 2 (retry must run on a fresh timeout ctx, not a canceled one)", got)
	}
}
