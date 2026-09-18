package config

// sa-72: text_verbosity endpoint field plumbing tests. The provider wire
// behavior is covered in internal/provider; these assert the yaml tag parses
// and the resolved endpoint carries the trimmed hint.

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
						// Whitespace proves the resolver trims like it does
						// for reasoning_effort/tool_choice.
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
