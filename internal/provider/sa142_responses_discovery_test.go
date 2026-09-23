package provider

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

// ---- OpenAIResponsesProvider accessors -------------------------------------

func TestSA142_ResponsesAccessors(t *testing.T) {
	p := NewOpenAIResponsesProvider("k", "gpt-5.2", 1024, "http://127.0.0.1:9/v1")
	if p.Name() != "openai-responses" {
		t.Fatalf("Name = %q", p.Name())
	}
	p.SetReasoningEffort("high")
	if p.ReasoningEffort() != "high" {
		t.Fatalf("ReasoningEffort = %q", p.ReasoningEffort())
	}
	p.SetTextVerbosity("low")
	if p.TextVerbosity() != "low" {
		t.Fatalf("TextVerbosity = %q", p.TextVerbosity())
	}
	p.SetServiceTier("priority")
	if p.ServiceTier() != "priority" {
		t.Fatalf("ServiceTier = %q", p.ServiceTier())
	}
	p.SetToolChoice("none")
	if p.ToolChoice() != "none" {
		t.Fatalf("ToolChoice = %q", p.ToolChoice())
	}
	p.SetMaxTokens(88)
	if p.maxTokensOverride != 88 {
		t.Fatalf("maxTokensOverride = %d", p.maxTokensOverride)
	}
	p.SetBackgroundMode(true)
	if !p.background {
		t.Fatal("SetBackgroundMode ignored")
	}
	// CountTokens is a local estimate, no network.
	n, err := p.CountTokens(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hello world"}}}})
	if err != nil || n <= 0 {
		t.Fatalf("CountTokens = %d, %v", n, err)
	}
}

// ---- Model discovery --------------------------------------------------------

func TestSA142_DiscoverModelsFromURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// OpenAI protocol discovery is a single unpaginated request.
		fmt.Fprint(w, `{"data":[{"id":"m1"},{"id":"m2"}]}`)
	}))
	defer server.Close()

	resolved := &config.ResolvedEndpoint{Protocol: "openai", BaseURL: server.URL}
	models, err := discoverModelsFromURL(context.Background(), server.Client(), server.URL, resolved)
	if err != nil {
		t.Fatalf("discoverModelsFromURL: %v", err)
	}
	joined := strings.Join(models, ",")
	for _, want := range []string{"m1", "m2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("models %v missing %q", models, want)
		}
	}
	// Bad URL surfaces an error instead of an empty list.
	if _, err := discoverModelsFromURL(context.Background(), server.Client(), "http://127.0.0.1:1/x", resolved); err == nil {
		t.Fatal("unreachable discovery endpoint must error")
	}
}

// ---- Anthropic stream error event -------------------------------------------

func TestSA142_AnthropicStreamErrorEvent(t *testing.T) {
	// A 400 at stream start is non-retryable: the stream goroutine must
	// surface exactly one StreamEventError and close the channel promptly.
	p := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: field required"}}`)
	})
	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	sawErr := false
	timeout := time.After(15 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			if ev.Type == StreamEventError {
				sawErr = true
				if ev.Error == nil {
					t.Fatal("error event carried nil error")
				}
			}
		case <-timeout:
			t.Fatal("stream timed out")
		}
	}
	if !sawErr {
		t.Fatal("SSE error event must surface as StreamEventError")
	}
}

// ---- Remaining zero-coverage leaves ------------------------------------------

func TestSA142_ZeroCoverageLeaves(t *testing.T) {
	// serverToolUseRaw: empty input normalizes to {}.
	raw := serverToolUseRaw("srv_1", "web_search", nil)
	if raw == nil || !strings.Contains(string(raw), `"type":"server_tool_use"`) || !strings.Contains(string(raw), `"input":{}`) {
		t.Fatalf("serverToolUseRaw = %s", raw)
	}
	withInput := serverToolUseRaw("srv_2", "web_search", json.RawMessage(`{"query":"go"}`))
	if !strings.Contains(string(withInput), `"query":"go"`) {
		t.Fatalf("serverToolUseRaw input lost: %s", withInput)
	}
	// responsesImagePart builds a data URI.
	part := responsesImagePart("image/png", "QUJD")
	if part["type"] != "input_image" || part["image_url"] != "data:image/png;base64,QUJD" {
		t.Fatalf("responsesImagePart = %v", part)
	}
	// setCallPolicy roundtrips on each provider type.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chatcmpl_z","object":"chat.completion","created":0,"model":"m",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer server.Close()

	cp := callPolicy{requestTimeout: 5 * time.Second, maxRetries: 2}
	op := NewOpenAIProviderWithBaseURL("k", "m", 10, server.URL+"/v1")
	op.setCallPolicy(cp)
	gp, err := NewGeminiProviderWithBaseURL("k", "m", 10, server.URL)
	if err != nil {
		t.Fatalf("gemini: %v", err)
	}
	gp.setCallPolicy(cp)
	rp := NewOpenAIResponsesProvider("k", "m", 10, server.URL+"/v1/responses")
	rp.setCallPolicy(cp)
	ap := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_z","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	ap.setCallPolicy(cp)
	// The policy applies: all four complete a Chat within the deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := op.Chat(ctx, []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil); err != nil {
		t.Fatalf("openai chat with policy: %v", err)
	}
	if _, err := ap.Chat(ctx, []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil); err != nil {
		t.Fatalf("anthropic chat with policy: %v", err)
	}
	// OpenAI probeChat + logprobs flag.
	op.SetLogprobsRequest(true)
	if !op.LogprobsRequested() {
		t.Fatal("LogprobsRequested must reflect SetLogprobsRequest")
	}
	if err := op.probeChat(ctx, []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}); err != nil {
		t.Fatalf("openai probeChat happy path: %v", err)
	}
}
