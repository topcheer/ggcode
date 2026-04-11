package provider

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/config"
)

// NewProvider creates a protocol adapter from a resolved endpoint.
func NewProvider(resolved *config.ResolvedEndpoint) (Provider, error) {
	if resolved == nil {
		return nil, fmt.Errorf("resolved endpoint is nil")
	}

	switch resolved.Protocol {
	case "anthropic":
		return NewAnthropicProviderWithBaseURL(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL), nil

	case "openai":
		return NewOpenAIProviderWithBaseURL(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL), nil

	case "copilot":
		if err := validateCopilotResolved(resolved.BaseURL, resolved.APIKey); err != nil {
			return nil, err
		}
		return NewCopilotProvider(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL), nil

	case "gemini":
		prov, err := NewGeminiProviderWithBaseURL(resolved.APIKey, resolved.Model, resolved.MaxTokens, resolved.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("creating gemini provider: %w", err)
		}
		return prov, nil

	default:
		return nil, fmt.Errorf("unsupported protocol: %s (supported: anthropic, openai, gemini, copilot)", resolved.Protocol)
	}
}
