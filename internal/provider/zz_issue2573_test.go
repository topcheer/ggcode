package provider

// #2573 regression tests: the sa-78 callPolicy (request_timeout /
// max_retries) must actually reach the openai-responses provider instead of
// being silently dropped by the registry. Before the fix:
//   - registry resolved the policy but never called setCallPolicy for
//     openai-responses (both the explicit protocol branch and the URL-sniff
//     /responses fallback),
//   - OpenAIResponsesProvider had no policy field, no callPolicySetter
//     implementation, no retry loop and no per-call deadline in
//     Chat/ChatStream.

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

// Compile-time proof the provider now participates in the sa-78 wiring.
var _ callPolicySetter = (*OpenAIResponsesProvider)(nil)

// Chat must retry transient 5xx failures per the configured maxRetries
// instead of a single throwaway post.
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

// Chat must honor the configured per-call deadline instead of hanging for
// the server's full response.
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

// The explicit openai-responses protocol branch must wire the resolved
// policy (pre-fix: policy parsed then discarded).
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

// The URL-sniff fallback branch (protocol "openai" pointed at a /responses
// path) must wire the resolved policy too.
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

// ChatStream must honor the configured deadline for the full stream
// lifetime: a delta followed by a hang ends in a deadline error event, not
// an eternal stream.
func TestIssue2573StreamDeadlineCoversFullStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hel\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done() // hang until the client-side deadline kills the conn
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("sk-test", "gpt-5-codex", 1024, srv.URL+"/v1")
	p.setCallPolicy(callPolicy{requestTimeout: 300 * time.Millisecond})

	ch, err := p.ChatStream(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	start := time.Now()
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
	elapsed := time.Since(start)
	if text.String() != "hel" {
		t.Errorf("text = %q, want hel (pre-deadline delta must be delivered)", text.String())
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "context deadline exceeded") {
		t.Errorf("err = %v, want context deadline exceeded", gotErr)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed = %v, stream deadline not honored", elapsed)
	}
}

// A mid-stream transport failure BEFORE any content event was emitted must
// be retried: the retry replays the same stateless input losslessly.
func TestIssue2573StreamRetriesTransportFailureBeforeEmission(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			// Truncated body: declared Content-Length never delivered →
			// client scanner dies with unexpected EOF, nothing emitted.
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

// A mid-stream transport failure AFTER content was emitted must NOT be
// retried: replaying would duplicate already-delivered content.
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
			return // truncate: client sees unexpected EOF after the delta
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
