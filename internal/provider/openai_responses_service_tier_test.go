package provider

// sa-81: service_tier support in the OpenAI Responses adapter. The tier hint
// must ride the request's `service_tier` field and degrade gracefully when an
// OpenAI-compatible gateway predates the parameter.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesSetServiceTierValidation(t *testing.T) {
	p := NewOpenAIResponsesProvider("k", "gpt-5-codex", 0, "http://localhost")
	if p.ServiceTier() != "" {
		t.Fatalf("initial tier = %q, want empty", p.ServiceTier())
	}
	p.SetServiceTier(" Priority ")
	if p.ServiceTier() != "priority" {
		t.Errorf("tier = %q, want priority (trimmed+lowercased)", p.ServiceTier())
	}
	p.SetServiceTier("dedicated")
	if p.ServiceTier() != "priority" {
		t.Errorf("invalid value changed tier to %q, want priority preserved", p.ServiceTier())
	}
	p.SetServiceTier("")
	if p.ServiceTier() != "" {
		t.Errorf("empty value did not clear tier, got %q", p.ServiceTier())
	}
}

func TestOpenAIResponsesServiceTierInRequest(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &reqBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1},"status":"completed"}`)
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "gpt-5", 128, srv.URL)
	p.SetServiceTier("flex")
	if _, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("q")}},
	}, nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reqBody["service_tier"] != "flex" {
		t.Errorf("service_tier = %v, want flex", reqBody["service_tier"])
	}
}

func TestOpenAIResponsesNoServiceTierOmitsField(t *testing.T) {
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
	if strings.Contains(string(raw), `"service_tier"`) {
		t.Errorf("request body contains service_tier without config: %s", raw)
	}
}

func TestOpenAIResponsesServiceTierDegradeRetry(t *testing.T) {
	var firstBody, secondBody string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls++
		switch calls {
		case 1:
			firstBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"Unknown parameter: 'service_tier'.","type":"invalid_request_error","param":"service_tier"}}`)
		default:
			secondBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1},"status":"completed"}`)
		}
	}))
	defer srv.Close()

	p := NewOpenAIResponsesProvider("k", "gpt-5", 0, srv.URL)
	p.SetServiceTier("flex")
	resp, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("q")}},
	}, nil)
	if err != nil {
		t.Fatalf("Chat after degrade retry: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected non-nil response after retry")
	}
	if calls != 2 {
		t.Fatalf("expected exactly 2 requests, got %d", calls)
	}
	if !strings.Contains(firstBody, `"service_tier":"flex"`) {
		t.Errorf("first request missing tier hint: %s", firstBody)
	}
	if strings.Contains(secondBody, "service_tier") {
		t.Errorf("retry still carries service_tier: %s", secondBody)
	}
}
