package config

// Adaptive thinking (sa77): thinking_mode endpoint field plumbing tests. The
// provider wire behavior is covered in internal/provider; these assert the
// yaml tag parses and the resolved endpoint carries the trimmed mode.

import "testing"

func TestResolveEndpointThinkingMode(t *testing.T) {
	c := &Config{
		Language: "en",
		Vendors: map[string]VendorConfig{
			"anthropic": {
				Endpoints: map[string]EndpointConfig{
					"default": {
						Protocol:     "anthropic",
						BaseURL:      "https://api.anthropic.com",
						APIKey:       "sk-ant-test",
						MaxTokens:    32000,
						DefaultModel: "claude-opus-4-6",
						// Whitespace proves the resolver trims like it does
						// for reasoning_effort/tool_choice/text_verbosity.
						ThinkingMode: " adaptive ",
					},
				},
			},
		},
	}
	ep, err := c.ResolveEndpoint("anthropic", "default")
	if err != nil {
		t.Fatalf("ResolveEndpoint: %v", err)
	}
	if ep.ThinkingMode != "adaptive" {
		t.Errorf("ThinkingMode = %q, want adaptive (trimmed)", ep.ThinkingMode)
	}
}

func TestResolveEndpointThinkingModeEmpty(t *testing.T) {
	c := &Config{
		Language: "en",
		Vendors: map[string]VendorConfig{
			"anthropic": {
				Endpoints: map[string]EndpointConfig{
					"default": {
						Protocol:     "anthropic",
						BaseURL:      "https://api.anthropic.com",
						APIKey:       "sk-ant-test",
						MaxTokens:    32000,
						DefaultModel: "claude-opus-4-6",
					},
				},
			},
		},
	}
	ep, err := c.ResolveEndpoint("anthropic", "default")
	if err != nil {
		t.Fatalf("ResolveEndpoint: %v", err)
	}
	if ep.ThinkingMode != "" {
		t.Errorf("ThinkingMode = %q, want empty (auto) when unset", ep.ThinkingMode)
	}
}
