package e2e_test

import (
	"os"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// skipE2E skips the test if no API key is configured.
func skipE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("ZAI_API_KEY") == "" && os.Getenv("GGCODE_ZAI_API_KEY") == "" {
		t.Skip("skipping E2E test: no API key set")
	}
}

// newProviderFromEnv creates a real LLM provider from environment variables.
func newProviderFromEnv(t *testing.T) provider.Provider {
	t.Helper()
	key := os.Getenv("ZAI_API_KEY")
	if key == "" {
		key = os.Getenv("GGCODE_ZAI_API_KEY")
	}
	if key == "" {
		t.Fatal("no API key for provider")
	}
	model := os.Getenv("ZAI_MODEL")
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	prov, err := provider.NewProvider(&config.ResolvedEndpoint{
		VendorID:   "anthropic",
		VendorName: "anthropic",
		Protocol:   "anthropic",
		BaseURL:    "https://api.anthropic.com",
		APIKey:     key,
		Model:      model,
		MaxTokens:  1024,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return prov
}
