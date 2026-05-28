package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestDiscoverModelsOpenAICompatibleEndpoint(t *testing.T) {
	resetModelDiscoveryCacheForTests(t)
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
	resetModelDiscoveryCacheForTests(t)
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
	resetModelDiscoveryCacheForTests(t)
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

func TestDiscoverModelsCopilotEndpoint(t *testing.T) {
	resetModelDiscoveryCacheForTests(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/models" {
			t.Fatalf("expected /v1/models, got %s", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer copilot-token" {
			t.Fatalf("expected bearer auth, got %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "ggcode" {
			t.Fatalf("expected ggcode user agent, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"claude-3.5-sonnet"}]}`))
	}))
	defer server.Close()

	models, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		EndpointID:   "github.com",
		EndpointName: "GitHub.com",
		Protocol:     "copilot",
		BaseURL:      server.URL + "/v1",
		APIKey:       "copilot-token",
	})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 2 || models[0] != "gpt-4o" || models[1] != "claude-3.5-sonnet" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestDiscoverModelsReusesSameHostCacheForNonGatewayVendor(t *testing.T) {
	resetModelDiscoveryCacheForTests(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
	}))
	defer server.Close()

	first, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		VendorID:   "custom",
		EndpointID: "openai",
		Protocol:   "openai",
		BaseURL:    server.URL + "/v1",
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("first DiscoverModels: %v", err)
	}
	second, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		VendorID:   "custom",
		EndpointID: "anthropic",
		Protocol:   "anthropic",
		BaseURL:    server.URL + "/anthropic",
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("second DiscoverModels: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 network hit, got %d", hits.Load())
	}
	if len(first) != len(second) || first[0] != second[0] || first[1] != second[1] {
		t.Fatalf("expected shared cached models, got %v and %v", first, second)
	}
}

func TestDiscoverModelsCachesPerGatewayEndpoint(t *testing.T) {
	resetModelDiscoveryCacheForTests(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/openrouter/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"router-a"}]}`))
		case "/vercel/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"vercel-a"}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	first, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		VendorID:   "ai-gateway",
		EndpointID: "openrouter",
		Protocol:   "openai",
		BaseURL:    server.URL + "/openrouter",
		APIKey:     "test-key",
		Tags:       []string{"gateway"},
	})
	if err != nil {
		t.Fatalf("first DiscoverModels: %v", err)
	}
	second, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		VendorID:   "ai-gateway",
		EndpointID: "vercel",
		Protocol:   "openai",
		BaseURL:    server.URL + "/vercel",
		APIKey:     "test-key",
		Tags:       []string{"gateway"},
	})
	if err != nil {
		t.Fatalf("second DiscoverModels: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected 2 network hits for gateway endpoints, got %d", hits.Load())
	}
	if len(first) != 1 || first[0] != "router-a" || len(second) != 1 || second[0] != "vercel-a" {
		t.Fatalf("unexpected models: %v / %v", first, second)
	}
}

func TestDiscoverModelsUsesPersistentCacheWithinTTL(t *testing.T) {
	resetModelDiscoveryCacheForTests(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"cached-model"}]}`))
	}))
	defer server.Close()

	resolved := &config.ResolvedEndpoint{
		VendorID:   "custom",
		EndpointID: "primary",
		Protocol:   "openai",
		BaseURL:    server.URL + "/v1",
		APIKey:     "test-key",
	}
	models, err := DiscoverModels(context.Background(), resolved)
	if err != nil {
		t.Fatalf("initial DiscoverModels: %v", err)
	}
	if len(models) != 1 || models[0] != "cached-model" {
		t.Fatalf("unexpected models: %v", models)
	}
	resetInMemoryModelDiscoveryCache()
	models, err = DiscoverModels(context.Background(), resolved)
	if err != nil {
		t.Fatalf("cached DiscoverModels: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected persistent cache reuse with 1 network hit, got %d", hits.Load())
	}
	if len(models) != 1 || models[0] != "cached-model" {
		t.Fatalf("unexpected cached models: %v", models)
	}
}

func TestDiscoverModelsRefetchesExpiredCache(t *testing.T) {
	resetModelDiscoveryCacheForTests(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_, _ = w.Write([]byte(`{"data":[{"id":"old-model"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"new-model"}]}`))
	}))
	defer server.Close()

	resolved := &config.ResolvedEndpoint{
		VendorID:   "custom",
		EndpointID: "primary",
		Protocol:   "openai",
		BaseURL:    server.URL + "/v1",
		APIKey:     "test-key",
	}
	if _, err := DiscoverModels(context.Background(), resolved); err != nil {
		t.Fatalf("initial DiscoverModels: %v", err)
	}
	cacheKey := modelDiscoveryCacheKey(resolved)
	modelDiscoveryCacheMu.Lock()
	entry := modelDiscoveryCache[cacheKey]
	entry.FetchedAt = time.Now().Add(-modelDiscoveryCacheTTL - time.Minute)
	modelDiscoveryCache[cacheKey] = entry
	if err := persistModelDiscoveryCacheLocked(); err != nil {
		modelDiscoveryCacheMu.Unlock()
		t.Fatalf("persist expired cache: %v", err)
	}
	modelDiscoveryCacheMu.Unlock()
	resetInMemoryModelDiscoveryCache()

	models, err := DiscoverModels(context.Background(), resolved)
	if err != nil {
		t.Fatalf("refetch DiscoverModels: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected stale cache to refetch, got %d network hits", hits.Load())
	}
	if len(models) != 1 || models[0] != "new-model" {
		t.Fatalf("unexpected refetched models: %v", models)
	}
}

func resetModelDiscoveryCacheForTests(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	resetInMemoryModelDiscoveryCache()
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
}

func resetInMemoryModelDiscoveryCache() {
	modelDiscoveryCacheMu.Lock()
	defer modelDiscoveryCacheMu.Unlock()
	modelDiscoveryCacheLoaded = false
	modelDiscoveryCache = map[string]modelDiscoveryCacheEntry{}
}
