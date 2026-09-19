package provider

// #2576 regression tests: the copilot branch of NewProvider must wire the
// resolved callPolicy (request_timeout / max_retries) like the other five
// protocol branches. Before the fix, CopilotProvider ran with a zero-value
// policy: withTimeout was a no-op (no per-call deadline) and attempts()
// fell back to providerRetryAttempts=20, silently ignoring the user's
// endpoint config (same failure family as #2573).

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// Compile-time proof the copilot provider participates in the wiring
// (via the embedded *OpenAIProvider's setCallPolicy).
var _ callPolicySetter = (*CopilotProvider)(nil)

// A copilot endpoint's request_timeout must bound a hanging upstream: with
// the policy dropped, Chat waits the full 5s server sleep instead of the
// configured 250ms deadline.
func TestIssue2576RegistryWiresPolicyCopilot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"r1","choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	}))
	defer srv.Close()

	prov, err := NewProvider(&config.ResolvedEndpoint{
		Protocol:       "copilot",
		APIKey:         "gh-test",
		Model:          "gpt-4o",
		MaxTokens:      1024,
		BaseURL:        srv.URL + "/v1",
		RequestTimeout: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	cp, ok := prov.(*CopilotProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *CopilotProvider", prov)
	}
	if cp.policy.requestTimeout != 250*time.Millisecond {
		t.Fatalf("policy.requestTimeout = %v, want 250ms (registry dropped the copilot policy?)", cp.policy.requestTimeout)
	}

	start := time.Now()
	_, err = prov.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("hi")}},
	}, nil)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("err = %v, want context deadline exceeded", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed = %v, request_timeout not honored", elapsed)
	}
}
