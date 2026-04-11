package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNewProvider_CopilotInjectsHeaders(t *testing.T) {
	var authHeader string
	var intentHeader string
	var initiatorHeader string
	var requestPath string
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		intentHeader = r.Header.Get("Openai-Intent")
		initiatorHeader = r.Header.Get("x-initiator")
		requestPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	prov, err := NewProvider(&config.ResolvedEndpoint{
		Protocol:  "copilot",
		BaseURL:   server.URL + "/v1",
		APIKey:    "copilot-token",
		Model:     "gpt-4o",
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	resp, err := prov.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("Reply with pong")}},
	}, nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if requestPath != "/v1/chat/completions" {
		t.Fatalf("expected chat completions path, got %q", requestPath)
	}
	if authHeader != "Bearer copilot-token" {
		t.Fatalf("expected bearer auth header, got %q", authHeader)
	}
	if intentHeader != "conversation-edits" {
		t.Fatalf("expected Openai-Intent header, got %q", intentHeader)
	}
	if initiatorHeader != "user" {
		t.Fatalf("expected x-initiator=user, got %q", initiatorHeader)
	}
	if len(resp.Message.Content) != 1 || strings.TrimSpace(resp.Message.Content[0].Text) != "pong" {
		t.Fatalf("unexpected response: %#v", resp.Message.Content)
	}
}
