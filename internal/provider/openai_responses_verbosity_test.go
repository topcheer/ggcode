package provider

// sa-72: GPT-5 text.verbosity support in the OpenAI Responses adapter.
// The verbosity hint must ride the request's `text` object, survive config
// resolution, and degrade gracefully when an OpenAI-compatible gateway
// predates the parameter.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestSetTextVerbosityValidation(t *testing.T) {
	p := NewOpenAIResponsesProvider("k", "gpt-5-codex", 0, "http://localhost")
	if p.TextVerbosity() != "" {
		t.Fatalf("initial verbosity = %q, want empty", p.TextVerbosity())
	}
	p.SetTextVerbosity(" High ")
	if p.TextVerbosity() != "high" {
		t.Errorf("verbosity = %q, want high (trimmed+lowercased)", p.TextVerbosity())
	}
	p.SetTextVerbosity("verbose")
	if p.TextVerbosity() != "high" {
		t.Errorf("invalid value changed verbosity to %q, want high preserved", p.TextVerbosity())
	}
	p.SetTextVerbosity("")
	if p.TextVerbosity() != "" {
		t.Errorf("empty value did not clear verbosity, got %q", p.TextVerbosity())
	}
}

func TestOpenAIResponsesVerbosityInRequest(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &reqBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1},"status":"completed"}`)
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "gpt-5", 128, srv.URL)
	p.SetReasoningEffort("medium")
	p.SetTextVerbosity("low")
	if _, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("q")}},
	}, nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	textObj, ok := reqBody["text"].(map[string]any)
	if !ok {
		t.Fatalf("text object missing from request: %v", reqBody["text"])
	}
	if textObj["verbosity"] != "low" {
		t.Errorf("text.verbosity = %v, want low", textObj["verbosity"])
	}
	// Verbosity must not clobber the reasoning object.
	reasoning, _ := reqBody["reasoning"].(map[string]any)
	if reasoning == nil || reasoning["effort"] != "medium" {
		t.Errorf("reasoning = %v, want effort medium", reqBody["reasoning"])
	}
}

func TestOpenAIResponsesNoVerbosityOmitsTextField(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1},"status":"completed"}`)
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "gpt-5", 0, srv.URL)
	if _, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("q")}},
	}, nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if strings.Contains(string(raw), `"text"`) {
		t.Errorf("request body contains text field without verbosity config: %s", raw)
	}
}

func TestOpenAIResponsesVerbosityRejectedRetry(t *testing.T) {
	var bodies []string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"Unrecognized request argument supplied: text.verbosity","type":"invalid_request_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1},"status":"completed"}`)
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "gpt-5", 64, srv.URL)
	p.SetTextVerbosity("high")
	resp, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("q")}},
	}, nil)
	if err != nil {
		t.Fatalf("Chat should succeed after verbosity fallback: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (reject + retry)", calls)
	}
	if !strings.Contains(bodies[0], `"verbosity":"high"`) {
		t.Errorf("first request missing verbosity: %s", bodies[0])
	}
	if strings.Contains(bodies[1], `"text"`) {
		t.Errorf("retry still carries text object: %s", bodies[1])
	}
	if resp.Message.Role != "assistant" {
		t.Errorf("role = %q", resp.Message.Role)
	}
}

func TestOpenAIResponsesVerbosityOtherErrorsNoRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"model not found","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "gpt-5", 0, srv.URL)
	p.SetTextVerbosity("high")
	if _, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("q")}},
	}, nil); err == nil {
		t.Fatal("expected error for unrelated bad request")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry for unrelated errors)", calls)
	}
}

func TestResponsesRejectsVerbosityMatcher(t *testing.T) {
	cases := []struct {
		snippet string
		want    bool
	}{
		{`Unrecognized request argument supplied: text.verbosity`, true},
		{`Unknown parameter: 'text.verbosity'`, true},
		{`Unsupported parameter: verbosity`, true},
		{`verbosity must be one of low, medium, high`, false}, // validation errors mean it IS supported
		{`model not found`, false},
	}
	for _, tc := range cases {
		if got := responsesRejectsVerbosity([]byte(tc.snippet)); got != tc.want {
			t.Errorf("responsesRejectsVerbosity(%q) = %v, want %v", tc.snippet, got, tc.want)
		}
	}
}

func TestRegistryAppliesTextVerbosity(t *testing.T) {
	resolved := &config.ResolvedEndpoint{
		Protocol:  "openai-responses",
		APIKey:    "k",
		Model:     "gpt-5",
		MaxTokens: 128,
		BaseURL:   "http://localhost/v1",
	}
	p, err := NewProvider(resolved)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if tp, ok := p.(TextVerbosityProvider); ok {
		if tp.TextVerbosity() != "" {
			t.Errorf("verbosity = %q without config, want empty", tp.TextVerbosity())
		}
	} else {
		t.Fatal("responses provider does not implement TextVerbosityProvider")
	}

	resolved.TextVerbosity = "high"
	p2, err := NewProvider(resolved)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if tp, ok := p2.(TextVerbosityProvider); !ok || tp.TextVerbosity() != "high" {
		t.Errorf("registry did not apply text_verbosity, got %v", p2)
	}
}
