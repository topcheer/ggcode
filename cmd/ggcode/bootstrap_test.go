package main

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestResolveProviderRejectsEmptyConfig(t *testing.T) {
	cfg := &config.Config{}
	_, _, err := ResolveProvider(cfg)
	if err == nil {
		t.Fatal("expected error for empty config (no vendor)")
	}
}

func TestResolveProviderRejectsMissingAPIKey(t *testing.T) {
	cfg := &config.Config{
		Vendor: "openai",
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Endpoints: map[string]config.EndpointConfig{
					"default": {
						BaseURL: "https://api.openai.com/v1",
					},
				},
			},
		},
	}
	_, _, err := ResolveProvider(cfg)
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}
