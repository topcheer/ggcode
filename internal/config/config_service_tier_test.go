package config

import "testing"

func TestResolveEndpointServiceTier(t *testing.T) {
	c := &Config{
		Language: "en",
		Vendors: map[string]VendorConfig{
			"openai": {
				Endpoints: map[string]EndpointConfig{
					"default": {
						Protocol:     "openai-responses",
						BaseURL:      "https://api.openai.com/v1",
						APIKey:       "sk-test",
						MaxTokens:    4096,
						DefaultModel: "gpt-5-codex",

						ServiceTier: " flex ",
					},
				},
			},
		},
	}
	ep, err := c.ResolveEndpoint("openai", "default")
	if err != nil {
		t.Fatalf("ResolveEndpoint: %v", err)
	}
	if ep.ServiceTier != "flex" {
		t.Errorf("ServiceTier = %q, want flex (trimmed)", ep.ServiceTier)
	}
}

func TestResolveEndpointServiceTierEmpty(t *testing.T) {
	c := &Config{
		Language: "en",
		Vendors: map[string]VendorConfig{
			"openai": {
				Endpoints: map[string]EndpointConfig{
					"default": {
						Protocol:     "openai-responses",
						BaseURL:      "https://api.openai.com/v1",
						APIKey:       "sk-test",
						MaxTokens:    4096,
						DefaultModel: "gpt-5-codex",
					},
				},
			},
		},
	}
	ep, err := c.ResolveEndpoint("openai", "default")
	if err != nil {
		t.Fatalf("ResolveEndpoint: %v", err)
	}
	if ep.ServiceTier != "" {
		t.Errorf("ServiceTier = %q, want empty when unset", ep.ServiceTier)
	}
}
