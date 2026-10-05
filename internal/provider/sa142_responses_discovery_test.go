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

// #3429: openai-responses endpoints must discover models too - the
// Responses API exposes the same GET /v1/models {"data":[...]} shape, and
// the protocol falls into the default (Bearer) header branch. The two
// discovery gates (provider model_discovery.go + TUI provider_panel.go)
// previously whitelisted only openai/anthropic/gemini/copilot, so the
// wizard let users CREATE an openai-responses endpoint and then silently
// skipped auto-discovery (TUI gate: bare return nil, zero explanation).
func TestSA142_DiscoverModelsFromURL_ResponsesProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Mirror the openai-protocol test: serve the discovery payload on
		// every path - the helper probes candidate URLs itself.
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("Authorization = %q, want Bearer k (default/openai header branch)", got)
		}
		fmt.Fprint(w, `{"data":[{"id":"gpt-x"},{"id":"gpt-y"}]}`)
	}))
	defer server.Close()

	resolved := &config.ResolvedEndpoint{Protocol: "openai-responses", BaseURL: server.URL, APIKey: "k"}
	models, err := discoverModelsFromURL(context.Background(), server.Client(), server.URL, resolved)
	if err != nil {
		t.Fatalf("discoverModelsFromURL(openai-responses): %v", err)
	}
	joined := strings.Join(models, ",")
	for _, want := range []string{"gpt-x", "gpt-y"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("models %v missing %q", models, want)
		}
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

// ---- Session header survives impersonation (#sa-142 regression) ------------

func TestSA142_SessionHeaderSurvivesImpersonation(t *testing.T) {
	// Production order: agent startup sets GGCode-SessionID, then the
	// impersonation panel replaces the whole header set with one that has
	// no session ID. All three header-mutable providers must carry the
	// session header over instead of silently dropping it.
	makeHeaders := func() http.Header {
		h := http.Header{}
		h.Set("User-Agent", "imp/1.0")
		return h
	}

	ap := newSA142AnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_s","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	ap.SetSessionID("sess-a")
	ap.UpdateRuntimeHeaders(makeHeaders())
	if snap := ap.transport.snapshotHeaders(); snap.Get("GGCode-SessionID") != "sess-a" || snap.Get("User-Agent") != "imp/1.0" {
		t.Fatalf("anthropic headers = %v", snap)
	}

	gp, err := NewGeminiProviderWithBaseURL("k", "m", 10, "http://127.0.0.1:9")
	if err != nil {
		t.Fatalf("gemini: %v", err)
	}
	gp.SetSessionID("sess-g")
	gp.UpdateRuntimeHeaders(makeHeaders())
	if snap := gp.transport.snapshotHeaders(); snap.Get("GGCode-SessionID") != "sess-g" || snap.Get("User-Agent") != "imp/1.0" {
		t.Fatalf("gemini headers = %v", snap)
	}

	op := NewOpenAIProviderWithBaseURL("k", "m", 10, "http://127.0.0.1:9/v1")
	op.SetSessionID("sess-o")
	op.UpdateRuntimeHeaders(makeHeaders())
	if snap := op.transport.snapshotHeaders(); snap.Get("GGCode-SessionID") != "sess-o" || snap.Get("User-Agent") != "imp/1.0" {
		t.Fatalf("openai headers = %v", snap)
	}

	// An explicit session ID in the new set wins over the carried-over one.
	h := makeHeaders()
	h.Set("GGCode-SessionID", "sess-explicit")
	ap.UpdateRuntimeHeaders(h)
	if snap := ap.transport.snapshotHeaders(); snap.Get("GGCode-SessionID") != "sess-explicit" {
		t.Fatalf("explicit session id = %v", snap)
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
