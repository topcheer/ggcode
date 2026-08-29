package provider

// Regression test for GitHub issue #1288: Gemini model discovery ignored
// pagination - the API defaults to pageSize=50 while the registry holds
// far more entries, so discovery silently truncated and the 6h cache
// solidified the truncated list.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue1288_GeminiPaginationFollowed(t *testing.T) {
	resetModelDiscoveryCacheForTests(t)
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.URL.Query().Get("pageSize"); got != "1000" {
			t.Errorf("pageSize must be requested as 1000, got %q", got)
		}
		switch r.URL.Query().Get("pageToken") {
		case "":
			w.Write([]byte(`{"models":[{"name":"models/gemini-a"},{"name":"models/gemini-b"}],"nextPageToken":"tok-2"}`))
		case "tok-2":
			w.Write([]byte(`{"models":[{"name":"models/gemini-c"}],"nextPageToken":"tok-3"}`))
		case "tok-3":
			w.Write([]byte(`{"models":[{"name":"models/imagen-x"}],"nextPageToken":""}`))
		default:
			t.Errorf("unexpected pageToken %q", r.URL.Query().Get("pageToken"))
		}
	}))
	defer server.Close()

	models, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		EndpointID:   "gem",
		EndpointName: "Gemini",
		Protocol:     "gemini",
		BaseURL:      server.URL,
		APIKey:       "g-key",
	})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 4 {
		t.Fatalf("#1288: pagination not followed - want 4 models across 3 pages, got %d: %v", len(models), models)
	}
	if calls != 3 {
		t.Fatalf("expected 3 page requests, got %d", calls)
	}
	seen := map[string]bool{}
	for _, m := range models {
		seen[m] = true
	}
	for _, want := range []string{"gemini-a", "gemini-b", "gemini-c", "imagen-x"} {
		if !seen[want] {
			t.Fatalf("missing %s in %v", want, models)
		}
	}
}

func TestIssue1288_GeminiRepeatedTokenTerminates(t *testing.T) {
	// A broken relay echoing the same nextPageToken forever must not spin
	// the pagination loop.
	resetModelDiscoveryCacheForTests(t)
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"models":[{"name":"models/gemini-a"}],"nextPageToken":"same-forever"}`))
	}))
	defer server.Close()

	models, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		EndpointID:   "gem2",
		EndpointName: "Gemini2",
		Protocol:     "gemini",
		BaseURL:      server.URL,
		APIKey:       "g-key",
	})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("want deduplicated single model, got %v", models)
	}
	if calls != 2 { // first request + one repeat detection
		t.Fatalf("repeated-token circuit breaker failed: %d requests", calls)
	}
}
