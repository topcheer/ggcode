package provider

// Characteristic tests for issue #561 (provider batch):
//   A: OpenAI Chat/ChatStream never sent req.MaxTokens (adaptive cap write-only)
//   F: containsHTTPStatus missed terminator-anchored statuses ("status 401.")
//   C: Gemini streaming emitted all-zero usage when UsageMetadata missing
//   E: streaming hardcoded Choices[0], ignoring Index
//   G: replacing config.HTTPClient dropped Timeout/Jar/CheckRedirect
// Probes use real httptest round trips, mirroring the issue's ver-49 probes.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/topcheer/ggcode/internal/config"
)

// ---- Bug A: OpenAI must send max_tokens derived from cap.Get() ----

func TestIssue561A_ChatSendsMaxTokensFromAdaptiveCap(t *testing.T) {
	var gotMaxTokens any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotMaxTokens = body["max_tokens"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 12345, server.URL+"/v1")
	// Active adaptive cap at 4096; Get() must be consumed on the request path.
	p.SetAdaptiveCap(&adaptiveCap{key: "issue561-test-a", userHint: 4096})
	p.cap.cur.Store(4096)

	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if gotMaxTokens == nil {
		t.Fatalf("BUG A: request body has no max_tokens — adaptive cap is write-only")
	}
	if mt, ok := gotMaxTokens.(float64); !ok || int(mt) != 4096 {
		t.Fatalf("BUG A: expected max_tokens=4096 (cap.Get()), got %#v", gotMaxTokens)
	}
}

func TestIssue561A_ChatStreamSendsMaxTokensFromAdaptiveCap(t *testing.T) {
	var gotMaxTokens any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotMaxTokens = body["max_tokens"]
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 12345, server.URL+"/v1")
	p.SetAdaptiveCap(&adaptiveCap{key: "issue561-test-a2", userHint: 2048})
	p.cap.cur.Store(2048)

	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}
	for range ch {
	}
	if gotMaxTokens == nil {
		t.Fatalf("BUG A: stream request body has no max_tokens")
	}
	if mt, ok := gotMaxTokens.(float64); !ok || int(mt) != 2048 {
		t.Fatalf("BUG A(stream): expected max_tokens=2048, got %#v", gotMaxTokens)
	}
}

func TestIssue561A_ChatSendsMaxTokensWithoutCap(t *testing.T) {
	var gotMaxTokens any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotMaxTokens = body["max_tokens"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 999, server.URL+"/v1")
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if mt, ok := gotMaxTokens.(float64); !ok || int(mt) != 999 {
		t.Fatalf("BUG A: without cap, expected max_tokens=999 (configured), got %#v", gotMaxTokens)
	}
}

// ---- Bug F: terminator-anchored status codes must be recognized ----

func TestIssue561F_Status401WithPeriodIsNotRetryable(t *testing.T) {
	if isRetryable(fmt.Errorf("request failed with status 401.")) {
		t.Fatalf("BUG F: 'status 401.' (period-terminated) must be non-retryable")
	}
}

func TestIssue561F_StatusTerminatorAnchors(t *testing.T) {
	cases := []struct {
		msg  string
		code string
		want bool
	}{
		{"request failed with status 401.", "401", true},
		{"request failed with status 403)", "403", true},
		{"error (status 404;", "404", true},
		{"failed with status 500:", "500", true},
		{"failed with status 429, retry later", "429", true}, // pre-existing comma anchor
		{"requested 40123 tokens", "401", false},             // digit coincidence must NOT match
		{"id=4019x", "401", false},
	}
	for _, c := range cases {
		if got := containsHTTPStatus(c.msg, c.code); got != c.want {
			t.Errorf("containsHTTPStatus(%q, %q) = %v, want %v", c.msg, c.code, got, c.want)
		}
	}
}

// ---- Bug E: stream must not drop choices with index > 0 ----

func TestIssue561E_StreamRoutesChoiceByIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// vLLM/OpenRouter-style numbering: single candidate at index 1.
		fmt.Fprint(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"m\",\"choices\":[{\"index\":1,\"delta\":{\"role\":\"assistant\",\"content\":\"hello from index 1\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"m\",\"choices\":[{\"index\":1,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"m\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4,\"total_tokens\":7}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewOpenAIProviderWithBaseURL("test-key", "test-model", 128, server.URL+"/v1")
	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}
	var text strings.Builder
	var sawDone bool
	for ev := range ch {
		switch ev.Type {
		case StreamEventText:
			text.WriteString(ev.Text)
		case StreamEventError:
			t.Fatalf("stream error: %v", ev.Error)
		case StreamEventDone:
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatalf("stream never completed")
	}
	if got := text.String(); !strings.Contains(got, "hello from index 1") {
		t.Fatalf("BUG E: delta at choice index 1 was dropped, got text %q", got)
	}
}

// ---- Bug G: config.HTTPClient non-Transport fields must survive ----

func TestIssue561G_HTTPClientTimeoutPreserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	cfg := openai.DefaultConfig("test-key")
	cfg.BaseURL = server.URL + "/v1"
	// A 1ns client timeout must be honored: if preserved, every request
	// fails (Client.Timeout exceeded); pre-fix the provider replaced the
	// client wholesale, dropped the timeout, and requests succeeded.
	cfg.HTTPClient = &http.Client{
		Timeout:   1 * time.Nanosecond,
		Transport: http.DefaultTransport,
	}
	p := NewOpenAIProviderWithConfig(cfg, "test-key", "test-model", 128, "issue561-g")

	// Stub retrySleep: timeouts are retryable, so 20 fast attempts instead
	// of exponential backoff.
	origSleep := retrySleep
	defer func() { retrySleep = origSleep }()
	retrySleep = func(ctx context.Context, d time.Duration) error { return nil }

	done := make(chan error, 1)
	go func() {
		_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("BUG G: caller HTTPClient.Timeout was dropped — request succeeded despite 1ns timeout")
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("BUG G: Chat did not terminate")
	}
}

// ---- Bug C: Gemini stream usage fallback when UsageMetadata missing ----

func TestIssue561C_GeminiStreamUsageFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stream chunks WITHOUT usageMetadata — the exact condition from
		// the issue probe. Pre-fix the provider emitted all-zero usage.
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"a sufficiently long streamed reply\"}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer server.Close()

	prov, err := NewGeminiProviderWithBaseURL("dummy", "gemini-2.5-flash", 128, server.URL)
	if err != nil {
		t.Fatalf("gemini provider: %v", err)
	}
	ch, err := prov.ChatStream(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hello world from the user prompt")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}
	var usage *TokenUsage
	for ev := range ch {
		if ev.Type == StreamEventError {
			t.Fatalf("stream error: %v", ev.Error)
		}
		if ev.Type == StreamEventDone {
			usage = ev.Usage
		}
	}
	if usage == nil {
		t.Fatalf("stream never emitted Done")
	}
	if usage.InputTokens <= 0 {
		t.Fatalf("BUG C: usage.InputTokens is %d (all-zero usage without fallback)", usage.InputTokens)
	}
	if usage.OutputTokens <= 0 {
		t.Fatalf("BUG C: usage.OutputTokens is %d (char-estimate fallback missing)", usage.OutputTokens)
	}
}

// Guard against accidental name collisions with existing test helpers.
var _ = config.ResolvedEndpoint{}
