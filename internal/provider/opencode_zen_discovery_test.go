package provider

// Real-network discovery test against the public OpenCode Zen model listing
// (https://opencode.ai/zen/v1/models is unauthenticated). Skipped under
// -short (CI hermeticity); exercises the full candidate-resolution +
// impersonation-header path a configured zen-openai endpoint takes.

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestDiscoverModelsOpenCodeZenPublicListing(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	if os.Getenv("GGCODE_SKIP_NET") != "" {
		t.Skip("GGCODE_SKIP_NET set")
	}
	// Reachability guard: CI runners intermittently fail DNS for this host
	// ("lookup opencode.ai: no such host" - 3 consecutive CI reds across two
	// unrelated PRs while local runs pass). A live-network smoke test must
	// not block unrelated changes on transient DNS; skip WITH A REASON when
	// the host is unreachable, keep the full assertion path when it is not.
	if err := probeHostReachable("opencode.ai"); err != nil {
		t.Skipf("opencode.ai unreachable (transient DNS or egress block): %v", err)
	}
	resetModelDiscoveryCacheForTests(t)

	models, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		EndpointID:   "zen-openai",
		EndpointName: "Zen (OpenAI)",
		Protocol:     "openai",
		BaseURL:      "https://opencode.ai/zen/v1",
		// The listing is public; any non-placeholder key satisfies the
		// discovery gate (hasUsableAPIKey rejects only empty/${VAR} values).
		APIKey: "public-listing",
	})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) < 20 {
		t.Fatalf("expected a substantial model listing (74 at implementation time), got %d", len(models))
	}
	wantFree := map[string]bool{
		"mimo-v2.5-free":          false,
		"deepseek-v4-flash-free":  false,
		"jev-1.13-free":           false,
		"ling-3.0-flash-fin-free": false,
		"nemotron-3-ultra-free":   false,
	}
	// The free-model ROSTER is upstream marketing content, not a contract:
	// entries get delisted without notice (deepseek-v4-flash-free vanished
	// between runs). Structural assertions (listing size, discovery path)
	// stay hard; roster membership is advisory so upstream churn cannot
	// reddten the suite.
	for _, m := range models {
		if _, ok := wantFree[m]; ok {
			wantFree[m] = true
		}
	}
	for m, found := range wantFree {
		if !found {
			t.Logf("note: free model %q missing from zen /models listing (upstream roster drift, non-blocking)", m)
		}
	}
}

// probeHostReachable reports whether host resolves within a short window.
// Used by live-network tests to distinguish "environment cannot reach the
// internet" (skip) from "the code under test is broken" (fail).
func probeHostReachable(host string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := net.Resolver{}
	_, err := r.LookupHost(ctx, host)
	return err
}
