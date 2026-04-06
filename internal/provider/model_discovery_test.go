package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestDiscoverModelsOpenAICompatibleEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/v1/models" {
			t.Fatalf("expected /api/v1/models, got %s", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("expected bearer auth, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
	}))
	defer server.Close()

	models, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		EndpointID:   "api",
		EndpointName: "API",
		Protocol:     "openai",
		BaseURL:      server.URL + "/api/v1",
		APIKey:       "test-key",
	})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 2 || models[0] != "model-a" || models[1] != "model-b" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestDiscoverModelsAnthropicFallbacksToV1Models(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/anthropic/models":
			http.Error(w, "not found", http.StatusNotFound)
		case "/anthropic/v1/models":
			if got := r.Header.Get("x-api-key"); got != "anthropic-key" {
				t.Fatalf("expected x-api-key auth, got %q", got)
			}
			if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
				t.Fatalf("expected anthropic-version header, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-latest"}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	models, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		EndpointID:   "anthropic",
		EndpointName: "Anthropic",
		Protocol:     "anthropic",
		BaseURL:      server.URL + "/anthropic",
		APIKey:       "anthropic-key",
	})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 1 || models[0] != "claude-latest" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestDiscoverModelsGeminiModelsEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1beta/models" {
			t.Fatalf("expected /v1beta/models, got %s", got)
		}
		if got := r.URL.Query().Get("key"); got != "gemini-key" {
			t.Fatalf("expected API key query, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-pro"},{"name":"models/gemini-2.5-flash"}]}`))
	}))
	defer server.Close()

	models, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		EndpointID:   "api",
		EndpointName: "Gemini API",
		Protocol:     "gemini",
		BaseURL:      server.URL,
		APIKey:       "gemini-key",
	})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 2 || models[0] != "gemini-2.5-pro" || models[1] != "gemini-2.5-flash" {
		t.Fatalf("unexpected models: %#v", models)
	}
}
