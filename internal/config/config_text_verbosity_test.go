package config

import "testing"

func TestResolveEndpointTextVerbosity(t *testing.T) {
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

						TextVerbosity: " high ",
					},
				},
			},
		},
	}
	ep, err := c.ResolveEndpoint("openai", "default")
	if err != nil {
		t.Fatalf("ResolveEndpoint: %v", err)
	}
	if ep.TextVerbosity != "high" {
		t.Errorf("TextVerbosity = %q, want high (trimmed)", ep.TextVerbosity)
	}
}

func TestResolveEndpointTextVerbosityEmpty(t *testing.T) {
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
	if ep.TextVerbosity != "" {
		t.Errorf("TextVerbosity = %q, want empty when unset", ep.TextVerbosity)
	}
}
