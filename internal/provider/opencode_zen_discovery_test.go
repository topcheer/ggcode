package provider

// Real-network discovery test against the public OpenCode Zen model listing
// (https://opencode.ai/zen/v1/models is unauthenticated). Skipped under
// -short (CI hermeticity); exercises the full candidate-resolution +
// impersonation-header path a configured zen-openai endpoint takes.

import (
	"context"
	"os"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestDiscoverModelsOpenCodeZenPublicListing(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	if os.Getenv("GGCODE_SKIP_NET") != "" {
		t.Skip("GGCODE_SKIP_NET set")
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
	for _, m := range models {
		if _, ok := wantFree[m]; ok {
			wantFree[m] = true
		}
	}
	for m, found := range wantFree {
		if !found {
			t.Errorf("free model %q missing from zen /models listing", m)
		}
	}
}
