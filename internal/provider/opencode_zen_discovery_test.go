package provider

import (
	"context"
	"net"
	"os"
	"strings"
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

	if err := probeHostReachable("opencode.ai"); err != nil {
		t.Skipf("opencode.ai unreachable (transient DNS or egress block): %v", err)
	}
	resetModelDiscoveryCacheForTests(t)

	models, err := DiscoverModels(context.Background(), &config.ResolvedEndpoint{
		EndpointID:   "zen-openai",
		EndpointName: "Zen (OpenAI)",
		Protocol:     "openai",
		BaseURL:      "https://opencode.ai/zen/v1",

		APIKey: "public-listing",
	})
	if err != nil {

		if strings.Contains(err.Error(), "401") {
			t.Skipf("opencode.ai now rejects the public listing key (upstream auth change): %v", err)
		}

		if strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "Client.Timeout exceeded") {
			t.Skipf("opencode.ai listing timed out awaiting headers (transient upstream latency): %v", err)
		}
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

func probeHostReachable(host string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := net.Resolver{}
	_, err := r.LookupHost(ctx, host)
	return err
}
