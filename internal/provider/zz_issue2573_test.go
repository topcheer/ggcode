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

	"github.com/topcheer/ggcode/internal/config"
)

var _ callPolicySetter = (*OpenAIResponsesProvider)(nil)

func TestIssue2573ChatRetriesTransientFailures(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, "upstream overloaded")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"r1","status":"completed","output":[]}`)
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("sk-test", "gpt-5-codex", 1024, srv.URL+"/v1")
	p.setCallPolicy(callPolicy{maxRetries: 3})

	resp, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp == nil {
		t.Fatal("resp is nil")
	}
	if got := hits.Load(); got != 3 {
		t.Errorf("server hits = %d, want 3 (two 503s retried, third succeeds)", got)
	}
}

func TestIssue2573ChatDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"r1","status":"completed","output":[]}`)
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("sk-test", "gpt-5-codex", 1024, srv.URL+"/v1")
	p.setCallPolicy(callPolicy{requestTimeout: 250 * time.Millisecond})

	start := time.Now()
	_, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected deadline error, got nil")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("err = %v, want context deadline exceeded", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed = %v, deadline clearly not honored", elapsed)
	}
}

func TestIssue2573RegistryWiresPolicyExplicitProtocol(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"r1","status":"completed","output":[]}`)
	}))
	defer srv.Close()

	prov, err := NewProvider(&config.ResolvedEndpoint{
		Protocol:       "openai-responses",
		APIKey:         "sk-test",
		Model:          "gpt-5-codex",
		MaxTokens:      1024,
		BaseURL:        srv.URL + "/v1",
		RequestTimeout: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, ok := prov.(*OpenAIResponsesProvider); !ok {
		t.Fatalf("provider type = %T, want *OpenAIResponsesProvider", prov)
	}

	start := time.Now()
	_, err = prov.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("err = %v, want context deadline exceeded (policy was dropped by registry?)", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed = %v, request_timeout not honored", elapsed)
	}
}

func TestIssue2573RegistryWiresPolicyURLSniffFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"r1","status":"completed","output":[]}`)
	}))
	defer srv.Close()

	prov, err := NewProvider(&config.ResolvedEndpoint{
		Protocol:       "openai",
		APIKey:         "sk-test",
		Model:          "gpt-5-codex",
		MaxTokens:      1024,
		BaseURL:        srv.URL + "/v1/responses",
		RequestTimeout: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	rp, ok := prov.(*OpenAIResponsesProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *OpenAIResponsesProvider (URL sniff failed?)", prov)
	}
	_ = rp

	start := time.Now()
	_, err = prov.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("err = %v, want context deadline exceeded (policy was dropped by URL-sniff branch?)", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed = %v, request_timeout not honored", elapsed)
	}
}

func TestIssue2573StreamDeadlineCoversFullStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = recover() }()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hel\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("sk-test", "gpt-5-codex", 1024, srv.URL+"/v1")

	p.setCallPolicy(callPolicy{requestTimeout: 5 * time.Second})

	ch, err := p.ChatStream(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	start := time.Now()
	var text strings.Builder
	var gotErr error
	first := true
	for ev := range ch {
		if first {
			start = time.Now()
			first = false
		}
		switch ev.Type {
		case StreamEventText:
			text.WriteString(ev.Text)
		case StreamEventError:
			gotErr = ev.Error
		}
	}
	elapsed := time.Since(start)
	if text.String() != "hel" {
		t.Errorf("text = %q, want hel (pre-deadline delta must be delivered)", text.String())
	}
	if gotErr == nil || !(strings.Contains(gotErr.Error(), "context deadline exceeded") || strings.Contains(gotErr.Error(), "broken pipe") || strings.Contains(gotErr.Error(), "unexpected EOF")) {
		t.Errorf("err = %v, want deadline-class terminal error", gotErr)
	}
	if elapsed > 10*time.Second {
		t.Errorf("elapsed = %v, stream deadline not honored", elapsed)
	}
}

func TestIssue2573StreamRetriesTransportFailureBeforeEmission(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Content-Length", "2048")
			io.WriteString(w, ": keepalive\n\n")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("sk-test", "gpt-5-codex", 1024, srv.URL+"/v1")
	p.setCallPolicy(callPolicy{maxRetries: 2})

	ch, err := p.ChatStream(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	var done bool
	var gotErr error
	for ev := range ch {
		switch ev.Type {
		case StreamEventText:
			text.WriteString(ev.Text)
		case StreamEventDone:
			done = true
		case StreamEventError:
			gotErr = ev.Error
		}
	}
	if gotErr != nil {
		t.Fatalf("unexpected error event: %v", gotErr)
	}
	if !done {
		t.Fatal("no Done event")
	}
	if text.String() != "ok" {
		t.Errorf("text = %q, want ok (only retry-attempt content)", text.String())
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("server hits = %d, want 2 (first transport failure retried)", got)
	}
}

func TestIssue2573StreamDoesNotRetryAfterEmission(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Content-Length", "4096")
			io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"par\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("sk-test", "gpt-5-codex", 1024, srv.URL+"/v1")
	p.setCallPolicy(callPolicy{maxRetries: 3})

	ch, err := p.ChatStream(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	var gotErr error
	for ev := range ch {
		switch ev.Type {
		case StreamEventText:
			text.WriteString(ev.Text)
		case StreamEventError:
			gotErr = ev.Error
		}
	}
	if text.String() != "par" {
		t.Errorf("text = %q, want par (no duplicated replay)", text.String())
	}
	if gotErr == nil {
		t.Fatal("expected stream error after mid-stream failure, got none")
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("server hits = %d, want 1 (post-emission failure must be fatal, not retried)", got)
	}
}
