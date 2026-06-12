package relaycatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveUsesRelayHTTPAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/model-catalog/resolve" {
			t.Fatalf("path = %q, want /model-catalog/resolve", r.URL.Path)
		}
		if got := r.URL.Query().Get("provider_id"); got != "zai" {
			t.Fatalf("provider_id = %q, want zai", got)
		}
		if got := r.URL.Query().Get("model_id"); got != "glm-5-turbo" {
			t.Fatalf("model_id = %q, want glm-5-turbo", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"found":true,"match_kind":"provider_model_exact","matched_provider_id":"zai","matched_model_id":"glm-5-turbo","context_window":200000,"max_output_tokens":8192}`))
	}))
	defer server.Close()

	resp, err := Resolve(context.Background(), server.URL, "zai", "glm-5-turbo")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Found {
		t.Fatal("expected found response")
	}
	if resp.ContextWindow != 200000 {
		t.Fatalf("context_window = %d, want 200000", resp.ContextWindow)
	}
	if resp.MaxOutputTokens != 8192 {
		t.Fatalf("max_output_tokens = %d, want 8192", resp.MaxOutputTokens)
	}
}
